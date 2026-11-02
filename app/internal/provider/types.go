// Package provider 定义统一的模型调用模型与适配器边界。
//
// 核心层只认这里的类型；OpenAI / Anthropic / Gemini 的专有字段
// 只存在于各自的适配器包内（G3、C1、C2）。
package provider

// Role 是内部消息角色，只有四种（C1）。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message 是一条内部消息。图片挂在 user 消息上，使用 data URL 或 http(s) URL。
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	Images     []string   `json:"images,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall 是一次工具调用；Arguments 在内部始终是对象（C1）。
type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// Tool 是声明给模型的工具。
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Request 是内部唯一请求模型（C1）。
type Request struct {
	Model              string
	Messages           []Message
	Tools              []Tool
	ToolChoice         any
	Stream             bool
	MaxTokens          int
	Temperature        *float64
	TopP               *float64
	Stop               []string
	StreamIncludeUsage bool
}

// StopReason 是终止原因，值域固定为六种（C2）。
type StopReason string

const (
	StopEndTurn      StopReason = "end_turn"
	StopToolUse      StopReason = "tool_use"
	StopMaxTokens    StopReason = "max_tokens"
	StopStopSequence StopReason = "stop_sequence"
	StopSafety       StopReason = "safety"
	StopOther        StopReason = "other"
)

// EventType 是统一响应事件类型（C2）。
type EventType string

const (
	EventThinking EventType = "thinking"
	EventText     EventType = "text"
	EventToolCall EventType = "tool_call"
	EventUsage    EventType = "usage"
	EventDone     EventType = "done"
)

// Event 是适配器产生的统一流式事件。
type Event struct {
	Type           EventType
	Delta          string
	Index          int
	ID             string
	Name           string
	ArgumentsDelta string
	Usage          *Usage
	StopReason     StopReason
}

// Usage 是用量。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Total 返回总 token 数。
func (u Usage) Total() int {
	return u.PromptTokens + u.CompletionTokens
}

// Response 是聚合后的完整回复。
type Response struct {
	Thinking   string
	Text       string
	ToolCalls  []ToolCall
	Usage      Usage
	StopReason StopReason
}
