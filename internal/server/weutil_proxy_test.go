package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsPublicIP(t *testing.T) {
	tests := map[string]bool{
		"8.8.8.8": true, "1.1.1.1": true, "2606:4700:4700::1111": true,
		"127.0.0.1": false, "10.0.0.1": false, "172.16.0.1": false,
		"192.168.1.1": false, "169.254.1.1": false, "100.64.0.1": false,
		"198.18.0.1": false, "192.0.2.1": false, "203.0.113.1": false,
		"::1": false, "fc00::1": false, "fe80::1": false, "2001:db8::1": false,
	}
	for raw, expected := range tests {
		if actual := isPublicIP(net.ParseIP(raw)); actual != expected {
			t.Errorf("isPublicIP(%s) = %v, want %v", raw, actual, expected)
		}
	}
}

func TestValidateWeutilTarget(t *testing.T) {
	if _, err := validateWeutilTarget(" https://example.com/v1/items?q=ok "); err != nil {
		t.Fatalf("valid target rejected: %v", err)
	}
	invalid := []string{
		"ftp://example.com/file", "http://127.0.0.1/admin", "http://[::1]/",
		"https://user:pass@example.com/", "https://example.com/#fragment", "https://example.com:99999/",
	}
	for _, raw := range invalid {
		if _, err := validateWeutilTarget(raw); err == nil {
			t.Errorf("invalid target accepted: %s", raw)
		}
	}
	if _, err := publicIPs(context.Background(), "127.0.0.1"); err == nil {
		t.Error("loopback literal was not blocked at connection resolution")
	}
}

type proxyDoerFunc func(*http.Request) (*http.Response, error)

func (f proxyDoerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestWeutilProxyHandler(t *testing.T) {
	client := proxyDoerFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.String() != "https://example.com/v1/items" {
			t.Fatalf("unexpected upstream request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("X-Test") != "preserved" || r.Header.Get("Connection") != "" || r.Header.Get("Accept-Encoding") != "" {
			t.Fatalf("header filtering failed: %#v", r.Header)
		}
		payload, _ := io.ReadAll(r.Body)
		if string(payload) != "request-body" {
			t.Fatalf("body = %q", payload)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Upstream": []string{"yes"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}, nil
	})
	handler := newWeutilProxyHandler(client)
	request := httptest.NewRequest(http.MethodPost, "/api/weutil/proxy", strings.NewReader(`{
		"method":"POST","url":"https://example.com/v1/items","body":"request-body",
		"headers":{"X-Test":"preserved","Connection":"close","Accept-Encoding":"gzip"}
	}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	for _, expected := range []string{`"status":201`, `"statusText":"Created"`, `"body":"{\"ok\":true}"`, `"content-type":"application/json"`, `"x-upstream":"yes"`} {
		if !strings.Contains(body, expected) {
			t.Errorf("response %s does not contain %s", body, expected)
		}
	}
}

func TestWeutilProxyRejectsOversizedResponse(t *testing.T) {
	client := proxyDoerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(strings.Repeat("x", weutilProxyMaxBody+1)))}, nil
	})
	handler := newWeutilProxyHandler(client)
	request := httptest.NewRequest(http.MethodPost, "/api/weutil/proxy", strings.NewReader(`{"url":"https://example.com"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), "10MB limit") {
		t.Fatalf("oversized response not rejected: %s", response.Body.String())
	}
}

func TestWeutilProxyRateLimit(t *testing.T) {
	limiter := &weutilProxyRateLimiter{}
	now := time.Now()
	for i := 0; i < weutilProxyRequestsPerMin; i++ {
		if !limiter.allow("client", now) {
			t.Fatalf("request %d unexpectedly limited", i+1)
		}
	}
	if limiter.allow("client", now) {
		t.Fatal("request above limit was accepted")
	}
	if !limiter.allow("client", now.Add(time.Minute)) {
		t.Fatal("rate-limit window did not reset")
	}
}
