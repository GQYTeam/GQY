// Package eval 实现评测子命令的闭环：矩阵、单次对话循环、判定、落盘、回放与报告。
//
// 设计依据：docs/designs/07-评测.md（E1–E10）。
package eval

import "time"

// Status 是样本终态（E5）。
const (
	StatusOK          = "ok"
	StatusNotConverge = "not_converged"
	StatusGatewayErr  = "gateway_error"
	StatusExecErr     = "exec_error"
)

// Combo 是一组三维变体取值。
type Combo struct {
	Prompt string `json:"prompt"`
	Schema string `json:"schema"`
	Return string `json:"return"`
}

// ToolCallRecord 是一次工具调用记录（E6）。
type ToolCallRecord struct {
	Name        string `json:"name"`
	ArgsRaw     string `json:"args_raw"`
	SchemaValid bool   `json:"schema_valid"`
	ExecOK      bool   `json:"exec_ok"`
	RetRendered string `json:"ret_rendered"`
	LatencyMS   int64  `json:"latency_ms"`
}

// JudgeResult 是判定结果（E5）。
type JudgeResult struct {
	Rule  RuleJudge  `json:"rule"`
	Model ModelJudge `json:"model,omitempty"`
}

// RuleJudge 是规则判定。
type RuleJudge struct {
	Pass    bool     `json:"pass"`
	Reasons []string `json:"reasons"`
}

// ModelJudge 是模型裁判（单独记录来源）。
type ModelJudge struct {
	Pass        bool     `json:"pass"`
	Reasons     []string `json:"reasons"`
	JudgeModel  string   `json:"judge_model,omitempty"`
	Temperature float64  `json:"temperature"`
}

// RunResult 是一次运行的全量记录（E6）。
type RunResult struct {
	RunID       string            `json:"run_id"`
	CaseID      string            `json:"case_id"`
	RepeatIdx   int               `json:"repeat_idx"`
	Round       string            `json:"round"`
	Axis        string            `json:"axis"`
	Replay      bool              `json:"replay,omitempty"`
	Config      map[string]string `json:"config"`
	Messages    []MaskedMessage   `json:"messages_masked"`
	ToolCalls   []ToolCallRecord  `json:"tool_calls"`
	FinalAnswer string            `json:"final_answer"`
	RoundsUsed  int               `json:"rounds_used"`
	Tokens      Tokens            `json:"tokens"`
	TotalMS     int64             `json:"total_ms"`
	Judge       JudgeResult       `json:"judge"`
	Status      string            `json:"status"`
}

// MaskedMessage 是脱敏后的消息。
type MaskedMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Tokens 是用量。
type Tokens struct {
	In  int `json:"in"`
	Out int `json:"out"`
}

func (r *RunResult) TotalMSNow(start time.Time) { r.TotalMS = time.Since(start).Milliseconds() }
