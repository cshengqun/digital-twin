// Package tools 提供工具注册表与执行器：LLM 只能看到已注册工具的
// name/description/JSON Schema，系统负责白名单校验、参数解析、超时执行与结果截断。
package tools

import (
	"context"
	"encoding/json"
	"sync"

	"digital-twin/internal/llm"
)

// Tool 一个可注册的工具。
type Tool struct {
	Name        string          // 工具名（LLM 调用时使用）
	Description string          // 功能描述（给 LLM 判断何时使用）
	Parameters  json.RawMessage // JSON Schema 描述入参
	Handler     func(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry 工具注册表（白名单）：启动时注册，LLM 只能调用已注册工具。
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name] = t
}

func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Defs 返回给 LLM 的工具声明列表。
func (r *Registry) Defs() []llm.ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]llm.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, llm.ToolDef{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	return out
}
