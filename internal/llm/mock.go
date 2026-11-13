package llm

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// MockClient 在未配置 ARK_API_KEY 时提供模拟流式回复，保证全链路可跑通。
type MockClient struct{}

func NewMockClient() *MockClient { return &MockClient{} }

func (m *MockClient) Stream(ctx context.Context, _ []Message, onDelta func(string)) (string, Usage, error) {
	reply := "（Mock 模式）对话闭环已跑通。配置 ARK_API_KEY 与 ARK_MODEL_ID 后，这里会变成火山方舟的真实回复。\n\n当前处于 Phase 0 骨架阶段：登录认证、会话管理、消息持久化、SSE 流式均已就绪。下一步将接入记忆体系与知识库 RAG。"
	var out strings.Builder
	for _, r := range []rune(reply) {
		select {
		case <-ctx.Done():
			return out.String(), Usage{}, ctx.Err()
		case <-time.After(18 * time.Millisecond):
		}
		out.WriteRune(r)
		if onDelta != nil {
			onDelta(string(r))
		}
	}
	return out.String(), Usage{InputTokens: 10, OutputTokens: len([]rune(reply))}, nil
}

// Complete 非流式：Mock 直接返回固定 JSON 记忆，便于无 Key 时联调抽取流程。
func (m *MockClient) Complete(ctx context.Context, messages []Message) (string, Usage, error) {
	select {
	case <-ctx.Done():
		return "", Usage{}, ctx.Err()
	case <-time.After(10 * time.Millisecond):
	}
	return `{"facts":["用户正在开发个人数字分身Agent项目"],"episodic":[],"procedural":[]}`, Usage{InputTokens: 20, OutputTokens: 30}, nil
}

// CompleteWithTools Mock：不发起工具调用，直接返回固定文本（模拟模型直接回答）。
func (m *MockClient) CompleteWithTools(ctx context.Context, messages []Message, tools []ToolDef) (*Completion, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(10 * time.Millisecond):
	}
	return &Completion{Text: "（Mock 模式）工具调用未启用，直接回答。", Usage: Usage{InputTokens: 20, OutputTokens: 20}}, nil
}

var _ Client = (*MockClient)(nil)

func (m *MockClient) String() string { return fmt.Sprintf("mock") }
