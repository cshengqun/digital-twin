package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	weutilProxyTimeout        = 20 * time.Second
	weutilProxyMaxBody        = 10 << 20
	weutilProxyMaxRequest     = 1 << 20
	weutilProxyRequestsPerMin = 30
)

type proxyDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type weutilProxyHandler struct {
	client  proxyDoer
	limiter *weutilProxyRateLimiter
}

type weutilProxyRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    *string           `json:"body"`
}

type weutilProxyResponse struct {
	Status     int               `json:"status,omitempty"`
	StatusText string            `json:"statusText,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	BodySize   int               `json:"bodySize,omitempty"`
	Time       int64             `json:"time,omitempty"`
	Truncated  bool              `json:"truncated,omitempty"`
	Error      string            `json:"error,omitempty"`
}

type weutilProxyRateLimiter struct {
	mu      sync.Mutex
	windows map[string]weutilProxyWindow
}

type weutilProxyWindow struct {
	start time.Time
	count int
}

func (l *weutilProxyRateLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.windows == nil {
		l.windows = make(map[string]weutilProxyWindow)
	}
	window := l.windows[key]
	if window.start.IsZero() || now.Sub(window.start) >= time.Minute {
		window = weutilProxyWindow{start: now}
	}
	if window.count >= weutilProxyRequestsPerMin {
		l.windows[key] = window
		return false
	}
	window.count++
	l.windows[key] = window
	// Bound memory if a caller can present many source addresses.
	if len(l.windows) > 4096 {
		for key, candidate := range l.windows {
			if now.Sub(candidate.start) >= 2*time.Minute {
				delete(l.windows, key)
			}
		}
	}
	return true
}

func newWeutilProxyHandler(client proxyDoer) *weutilProxyHandler {
	return &weutilProxyHandler{client: client, limiter: &weutilProxyRateLimiter{}}
}

func newWeutilProxyClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		ForceAttemptHTTP2:      true,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  15 * time.Second,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: 1 << 20,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := publicIPs(ctx, host)
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, ip := range ips {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			if lastErr == nil {
				lastErr = errors.New("no public address available")
			}
			return nil, lastErr
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   weutilProxyTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func publicIPs(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return nil, errors.New("target resolves to a private or restricted address")
		}
		return []net.IP{ip}, nil
	}
	records, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}
	var ips []net.IP
	for _, record := range records {
		if isPublicIP(record.IP) {
			ips = append(ips, record.IP)
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("target resolves to a private or restricted address")
	}
	return ips, nil
}

func isPublicIP(ip net.IP) bool {
	address, err := netip.ParseAddr(ip.String())
	if err != nil || !address.IsGlobalUnicast() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsPrivate() {
		return false
	}
	// CGNAT, benchmarking and documentation ranges are global-unicast but not public targets.
	restricted := []string{
		"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32",
	}
	for _, raw := range restricted {
		if netip.MustParsePrefix(raw).Contains(address) {
			return false
		}
	}
	return true
}

func validateWeutilTarget(raw string) (*url.URL, error) {
	target, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, errors.New("invalid target URL")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, errors.New("only http:// and https:// targets are supported")
	}
	if target.Hostname() == "" || target.User != nil || target.Fragment != "" {
		return nil, errors.New("invalid target URL")
	}
	port := target.Port()
	if port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return nil, errors.New("invalid target port")
		}
	}
	if ip := net.ParseIP(target.Hostname()); ip != nil && !isPublicIP(ip) {
		return nil, errors.New("target resolves to a private or restricted address")
	}
	return target, nil
}

var blockedProxyHeaders = map[string]bool{
	"Accept-Encoding": true, "Connection": true, "Content-Length": true, "Host": true,
	"Keep-Alive": true, "Proxy-Authenticate": true, "Proxy-Authorization": true,
	"Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true,
}

func validProxyMethod(method string) bool {
	if method == "" {
		return false
	}
	for _, char := range method {
		if !strings.ContainsRune("!#$%&'*+-.^_`|~0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ", char) {
			return false
		}
	}
	return true
}

func (h *weutilProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !h.limiter.allow(r.RemoteAddr, time.Now()) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(weutilProxyResponse{Error: "proxy rate limit exceeded, retry later"})
		return
	}
	var input weutilProxyRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, weutilProxyMaxRequest))
	if err := decoder.Decode(&input); err != nil {
		_ = json.NewEncoder(w).Encode(weutilProxyResponse{Error: "invalid JSON request or request too large"})
		return
	}
	method := strings.ToUpper(input.Method)
	if method == "" {
		method = http.MethodGet
	}
	if !validProxyMethod(method) {
		_ = json.NewEncoder(w).Encode(weutilProxyResponse{Error: "invalid HTTP method"})
		return
	}
	target, err := validateWeutilTarget(input.URL)
	if err != nil {
		_ = json.NewEncoder(w).Encode(weutilProxyResponse{Error: err.Error()})
		return
	}
	var body io.Reader
	if input.Body != nil {
		body = bytes.NewBufferString(*input.Body)
	}
	outbound, err := http.NewRequestWithContext(r.Context(), method, target.String(), body)
	if err != nil {
		_ = json.NewEncoder(w).Encode(weutilProxyResponse{Error: "could not create upstream request"})
		return
	}
	for name, value := range input.Headers {
		canonical := http.CanonicalHeaderKey(name)
		if blockedProxyHeaders[canonical] || strings.HasPrefix(strings.ToLower(name), "proxy-") {
			continue
		}
		outbound.Header.Set(canonical, value)
	}
	started := time.Now()
	response, err := h.client.Do(outbound)
	if err != nil {
		_ = json.NewEncoder(w).Encode(weutilProxyResponse{Error: err.Error(), Time: time.Since(started).Milliseconds()})
		return
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, weutilProxyMaxBody+1))
	if err != nil {
		_ = json.NewEncoder(w).Encode(weutilProxyResponse{Error: "could not read upstream response", Time: time.Since(started).Milliseconds()})
		return
	}
	if len(payload) > weutilProxyMaxBody {
		_ = json.NewEncoder(w).Encode(weutilProxyResponse{Error: "response exceeds 10MB limit", Time: time.Since(started).Milliseconds()})
		return
	}
	headers := make(map[string]string, len(response.Header))
	for name, values := range response.Header {
		headers[strings.ToLower(name)] = strings.Join(values, ", ")
	}
	_ = json.NewEncoder(w).Encode(weutilProxyResponse{
		Status: response.StatusCode, StatusText: http.StatusText(response.StatusCode), Headers: headers,
		Body: string(payload), BodySize: len(payload), Time: time.Since(started).Milliseconds(),
	})
}
