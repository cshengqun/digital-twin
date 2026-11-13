package impl

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"digital-twin/internal/tools"
)

// CalculatorSchema 四则运算表达式计算器：支持 + - * / % ^ ( ) 与浮点数。
const CalculatorSchema = `{
  "type": "object",
  "properties": {
    "expression": {"type": "string", "description": "要计算的数学表达式，如 (12.5+3)*4/2 或 2^10"}
  },
  "required": ["expression"]
}`

func NewCalculator() tools.Tool {
	return tools.Tool{
		Name:        "calculator",
		Description: "计算数学表达式（支持 + - * / % ^ 和括号、浮点数）。适合需要精确数值计算的场景，如算收益、比例、概率。",
		Parameters:  json.RawMessage(CalculatorSchema),
		Handler: func(_ context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Expression string `json:"expression"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", fmt.Errorf("参数解析失败：%v", err)
			}
			in.Expression = strings.TrimSpace(in.Expression)
			if in.Expression == "" {
				return "", fmt.Errorf("expression 不能为空")
			}
			p := &calcParser{tokens: tokenizeCalc(in.Expression)}
			v, err := p.parseExpr()
			if err != nil {
				return "", err
			}
			if p.pos < len(p.tokens) {
				return "", fmt.Errorf("表达式在 %q 附近无法解析", p.tokens[p.pos])
			}
			return formatNum(v), nil
		},
	}
}

func formatNum(v float64) string {
	if math.Abs(v-math.Round(v)) < 1e-9 && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(math.Round(v)), 10)
	}
	return strconv.FormatFloat(v, 'f', 8, 64)
}

// ---------- 轻量表达式求值（递归下降：expr → term → factor） ----------

type calcParser struct {
	tokens []string
	pos    int
}

func (p *calcParser) peek() string {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return ""
}

func (p *calcParser) next() string {
	t := p.peek()
	p.pos++
	return t
}

func (p *calcParser) parseExpr() (float64, error) {
	v, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		op := p.peek()
		if op != "+" && op != "-" {
			return v, nil
		}
		p.next()
		r, err := p.parseTerm()
		if err != nil {
			return 0, err
		}
		if op == "+" {
			v += r
		} else {
			v -= r
		}
	}
}

func (p *calcParser) parseTerm() (float64, error) {
	v, err := p.parseFactor()
	if err != nil {
		return 0, err
	}
	for {
		op := p.peek()
		if op != "*" && op != "/" && op != "%" {
			return v, nil
		}
		p.next()
		r, err := p.parseFactor()
		if err != nil {
			return 0, err
		}
		switch op {
		case "*":
			v *= r
		case "/":
			if r == 0 {
				return 0, fmt.Errorf("除数为零")
			}
			v /= r
		case "%":
			if r == 0 {
				return 0, fmt.Errorf("模运算除数为零")
			}
			v = math.Mod(v, r)
		}
	}
}

func (p *calcParser) parseFactor() (float64, error) {
	t := p.next()
	switch t {
	case "-":
		v, err := p.parseFactor()
		return -v, err
	case "+":
		return p.parseFactor()
	case "(":
		v, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		if p.next() != ")" {
			return 0, fmt.Errorf("缺少右括号")
		}
		if p.peek() == "^" {
			p.next()
			exp, err := p.parseFactor()
			if err != nil {
				return 0, err
			}
			return math.Pow(v, exp), nil
		}
		return v, nil
	}
	if f, err := strconv.ParseFloat(t, 64); err == nil {
		if p.peek() == "^" {
			p.next()
			exp, err := p.parseFactor()
			if err != nil {
				return 0, err
			}
			return math.Pow(f, exp), nil
		}
		return f, nil
	}
	return 0, fmt.Errorf("无法识别的记号 %q", t)
}

func tokenizeCalc(s string) []string {
	var toks []string
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ':
			i++
		case c == '+' || c == '-' || c == '*' || c == '/' || c == '%' || c == '(' || c == ')':
			toks = append(toks, string(c))
			i++
		case c == '^':
			toks = append(toks, "^")
			i++
		case c >= '0' && c <= '9' || c == '.':
			j := i
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.' || s[j] == 'e' || s[j] == 'E' ||
				((s[j] == '-' || s[j] == '+') && j > i && (s[j-1] == 'e' || s[j-1] == 'E'))) {
				j++
			}
			toks = append(toks, s[i:j])
			i = j
		default:
			toks = append(toks, string(c)) // 交给解析器报错
			i++
		}
	}
	return toks
}
