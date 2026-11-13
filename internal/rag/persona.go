package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"digital-twin/internal/llm"
	"digital-twin/internal/store"
)

// SummarizeMessages 会话滚动摘要：把"既有摘要 + 新增溢出的原始消息"合并成一份新的摘要。
// 用关闭深度思考的客户端（结构化文本任务，秒级返回），纯文本输出。
func (e *MemoryExtractor) SummarizeMessages(ctx context.Context, existingSummary string, overflow []llm.Message) (string, error) {
	if e.llm == nil {
		return "", fmt.Errorf("摘要: LLM 未配置")
	}
	var sb strings.Builder
	sb.WriteString(`你是会话摘要器。把一次长对话的【既有摘要】与【新增原始消息】合并成一份新的简洁摘要。

要求：
1. 保留重要信息：主题、结论、约定、待办事项、关键事实与数字
2. 只输出合并后的摘要正文：一段中文，控制在 200 字左右
3. 不要输出 JSON、不要 markdown 标记、不要任何解释文字，不要复述消息原文

【既有摘要】
`)
	if strings.TrimSpace(existingSummary) == "" {
		sb.WriteString("（无）")
	} else {
		sb.WriteString(existingSummary)
	}
	sb.WriteString("\n\n【新增原始消息】\n")
	for _, m := range overflow {
		role := "用户"
		if m.Role == "assistant" {
			role = "分身"
		}
		sb.WriteString(role + ": " + strings.TrimSpace(m.Content) + "\n")
	}
	resp, _, err := e.llm.Complete(ctx, []llm.Message{{Role: "user", Content: sb.String()}})
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(resp)
	out = strings.TrimPrefix(out, "```")
	out = strings.TrimSuffix(out, "```")
	return strings.TrimSpace(out), nil
}

// ExtractJournal 消化一篇日志：把日志内容当作"主人自述"喂给三类记忆抽取管道，
// 抽取结果（facts / episodic / procedural）走与对话一致的处理逻辑入库。返回抽取条数。
func (e *MemoryExtractor) ExtractJournal(ctx context.Context, agentID int64, ownerDesc, journalText string) (int, error) {
	msgs := []llm.Message{{Role: "user", Content: journalText}}
	return e.ExtractConversation(ctx, agentID, ownerDesc, "journal", msgs)
}

// BuildProfile 人格建模：基于记忆提炼结构化人设 Profile 与动态 State，写回 agents 表。
// 输入 = 长期记忆（importance 降序，供 Profile）+ 近期记忆（时间倒序，供 State）；
// 并把上一次画像作为锚点传入，防止长期字段漂移。日志不直接参与（入库时已抽成记忆）。
func (e *MemoryExtractor) BuildProfile(ctx context.Context, agentID int64, ownerDesc string) error {
	mems, err := e.db.ListMemoriesForProfile(ctx, agentID, 60)
	if err != nil {
		return fmt.Errorf("profile mems: %w", err)
	}
	recent, err := e.db.ListRecentMemories(ctx, agentID, 60)
	if err != nil {
		return fmt.Errorf("profile recent: %w", err)
	}
	if len(mems) == 0 && len(recent) == 0 {
		return fmt.Errorf("profile rebuild: 还没有记忆，无法建模")
	}

	curProfile, curState, perr := e.db.GetAgentProfile(ctx, agentID)
	if perr != nil {
		slog.Warn("profile anchor read", "err", perr)
		curProfile, curState = "", ""
	}

	prompt := buildProfilePrompt(ownerDesc, mems, recent, curProfile, curState)
	resp, _, err := e.llm.Complete(ctx, []llm.Message{{Role: "user", Content: prompt}})
	if err != nil {
		return fmt.Errorf("profile llm: %w", err)
	}

	// 解析 {"profile":{...},"state":{...}}
	obj := map[string]json.RawMessage{}
	if err := parseProfileJSON(resp, &obj); err != nil {
		return fmt.Errorf("profile parse: %w", err)
	}
	profileRaw, ok := obj["profile"]
	if !ok {
		return fmt.Errorf("profile parse: 缺少 profile 字段")
	}
	stateRaw := obj["state"] // 允许缺省 → 空
	stateJSON := "{}"
	if len(stateRaw) > 0 {
		stateJSON = string(stateRaw)
	}

	if err := e.db.UpdateAgentProfile(ctx, agentID, string(profileRaw)); err != nil {
		return fmt.Errorf("profile save: %w", err)
	}
	if err := e.db.UpdateAgentState(ctx, agentID, stateJSON); err != nil {
		return fmt.Errorf("state save: %w", err)
	}
	slog.Info("profile rebuilt", "agent_id", agentID, "mems", len(mems), "recent", len(recent))
	return nil
}

// buildProfilePrompt 构造人格建模 prompt：
// 输入证据 = 长期记忆（importance 序）+ 近期记忆（时间序）+ 当前画像锚点。
func buildProfilePrompt(ownerDesc string, mems, recent []store.MemText, curProfile, curState string) string {
	var sb strings.Builder
	sb.WriteString(`你是个人数字分身的"人格建模引擎"。基于分身主人的长期记忆与近期记忆，提炼出他的结构化人设档案（Profile）与近期动态（State）。
Profile 描述"他是怎样的人"，State 描述"他最近的状态"。

分身主人是：` + ownerDesc + `

【当前画像（锚点，上一次建模结果；长期字段以它为准）】
Profile: ` + strings.TrimSpace(curProfile) + `
State: ` + strings.TrimSpace(curState) + `

【长期记忆证据】（按重要性降序，供 Profile 提炼；Fact 事实 / Procedural 方法）：
`)
	if len(mems) == 0 {
		sb.WriteString("（暂无）\n")
	}
	for _, m := range mems {
		sb.WriteString("- [" + m.Type + "] " + strings.TrimSpace(m.Text) + "\n")
	}
	sb.WriteString("\n【近期记忆证据】（按时间倒序最近" + fmt.Sprintf("%d", len(recent)) + `条，供 State 评估；含情景记忆）：
`)
	if len(recent) == 0 {
		sb.WriteString("（暂无）\n")
	}
	for _, m := range recent {
		sb.WriteString("- [" + m.Type + "] " + strings.TrimSpace(m.Text) + "\n")
	}

	sb.WriteString(`
要求：
0. 锚点规则：当前画像是对分身主人的长期特征提炼，是可靠基线。
   - identity / personality / values / tone 等长期字段：若当前画像仍成立，原样保留；只有出现足够强的新证据才修订；绝不因本次证据未提及而删减或改写
   - state 是动态状态：基于近期记忆证据重新评估
1. profile 字段：
   - identity：身份一句话（职业/角色/背景）
   - personality：性格特点（3-6 条）
   - tone：语气与措辞风格（1-2 句）
   - values：价值观（2-5 条，如看重什么、决策原则）
   - catchphrases：口头禅（有证据才列，否则空数组）
   - taboos：禁忌/不喜欢/边界（有证据才列，否则空数组）
   - knowledge_map：知识版图（2-6 个他熟悉的领域）
2. state 字段：
   - focus：当前关注的事
   - active_projects：进行中项目（有则列）
   - recent_mood：近期情绪/状态
   - recent_updates：近期的重要变化
3. 一切内容必须基于上述输入证据归纳，不要编造；证据不足的字段给空数组或简短描述
4. 只输出一个 JSON 对象，不要任何其他文字或 markdown 代码块标记
格式：{"profile":{"identity":"","personality":[],"tone":"","values":[],"catchphrases":[],"taboos":[],"knowledge_map":[]},"state":{"focus":"","active_projects":[],"recent_mood":"","recent_updates":[]}}`)
	return sb.String()
}

// parseProfileJSON 容错解析 LLM 输出的 JSON 对象（去掉 ```json 包裹与前后杂质）。
func parseProfileJSON(s string, out any) error {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i >= 0 {
		s = s[i:]
	}
	if i := strings.LastIndex(s, "}"); i >= 0 {
		s = s[:i+1]
	}
	return json.Unmarshal([]byte(s), out)
}
