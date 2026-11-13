package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"digital-twin/internal/llm"
)

const (
	maxToolTimeout = 30 * time.Second // 单工具执行超时
	maxResultLen    = 4000             // 工具结果截断（字符）
)

// Execute 校验并执行一次工具调用：白名单 → 参数 JSON 合法性 → 超时执行 → 截断。
// 返回的结果文本将作为 role=tool 消息回填给 LLM。
func Execute(ctx context.Context, reg *Registry, tc llm.ToolCall) (string, error) {
	t, ok := reg.Get(tc.Name)
	if !ok {
		return "", fmt.Errorf("工具 %q 未注册（白名单拒绝）", tc.Name)
	}
	if tc.Arguments != "" {
		var probe any
		if err := json.Unmarshal([]byte(tc.Arguments), &probe); err != nil {
			return "", fmt.Errorf("工具参数不是合法 JSON：%v", err)
		}
	}
	cctx, cancel := context.WithTimeout(ctx, maxToolTimeout)
	defer cancel()
	out, err := t.Handler(cctx, json.RawMessage(tc.Arguments))
	if err != nil {
		return "", fmt.Errorf("工具执行失败：%w", err)
	}
	out = strings.TrimSpace(out)
	if r := []rune(out); len(r) > maxResultLen {
		out = string(r[:maxResultLen]) + "…（结果过长已截断）"
	}
	return out, nil
}
