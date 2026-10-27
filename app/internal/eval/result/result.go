// 包 result 定义评测运行的可落盘结果类型。
//
// Runner 产出 Trace，Engine 补充 Judge，Recorder 负责序列化。
// 这里不依赖 recorder / runner，避免循环引用。
package result

import (
	"fmt"
	"strings"
	"time"

	"gqy/internal/eval/cases"
	"gqy/internal/provider"
)

// GatewayVersion 是评测网关自身的版本，写进每份运行记录。
const GatewayVersion = "1"

// JudgeVersion 是规则判定器的版本；判定规则变化必须递增。
const JudgeVersion = "rules-1"

// Status 是一次运行的健康状态。运行时错误与模型决策错误分开统计。
type Status string

const (
	StatusOK           Status = "ok"
	StatusNotConverged Status = "not_converged"
	StatusTimeout      Status = "timeout"
	StatusGatewayError Status = "gateway_error"
	StatusExecError    Status = "exec_error"
)

func (s Status) Valid() bool {
	switch s {
	case StatusOK, StatusNotConverged, StatusTimeout, StatusGatewayError, StatusExecError:
		return true
	}
	return false
}

func (s Status) Runtime() bool { return s != "" && s != StatusOK }

func statusRank(s Status) int {
	switch s {
	case StatusTimeout:
		return 4
	case StatusGatewayError:
		return 3
	case StatusNotConverged:
		return 2
	case StatusExecError:
		return 1
	}
	return 0
}

// Escalate 只把状态升级到更严重的一类，避免后面的小错误覆盖前面的超时。
func (t *Trace) Escalate(s Status) {
	if s == "" {
		return
	}
	if t.Status == "" || statusRank(s) > statusRank(t.Status) {
		t.Status = s
	}
}

// AddError 追加一条运行期错误（去重，保持首次出现顺序）。
func (t *Trace) AddError(msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	for _, old := range strings.Split(t.Error, "; ") {
		if old == msg {
			return
		}
	}
	if t.Error == "" {
		t.Error = msg
		return
	}
	t.Error += "; " + msg
}

// Tokens 统一 tokens 字段形状：in / out / total。
type Tokens struct {
	In    int `json:"in"`
	Out   int `json:"out"`
	Total int `json:"total"`
}

func (t *Tokens) Add(prompt, completion, total int) {
	t.In += prompt
	t.Out += completion
	t.Total += total
}

// RunConfig 是每次运行必须携带的配置快照部分。
type RunConfig struct {
	Prompt       string `json:"prompt"`
	Schema       string `json:"schema"`
	Return       string `json:"return"`
	Model        string `json:"model"`
	ModelVer     string `json:"model_ver,omitempty"`
	GatewayVer   string `json:"gateway_ver"`
	Tool         string `json:"tool"`
	CaseSetHash  string `json:"case_set_hash"`
	StrictSchema bool   `json:"strict_schema"`
	JudgeVer     string `json:"judge_ver"`
}

// ToolCallRecord 是一次工具调用的完整记录。
type ToolCallRecord struct {
	// Name 是解析后的真实工具名，判定器用它对比期望工具。
	Name string `json:"name"`
	// CalledAs 是模型实际请求的名字；alias 变体下可能不同。
	CalledAs     string         `json:"called_as,omitempty"`
	ArgsRaw      string         `json:"args_raw"`
	ArgsParsed   map[string]any `json:"args_parsed,omitempty"`
	SchemaValid  bool           `json:"schema_valid"`
	SchemaErrors []string       `json:"schema_errors,omitempty"`
	ExecOK       bool           `json:"exec_ok"`
	TimedOut     bool           `json:"timed_out,omitempty"`
	Injected     bool           `json:"injected,omitempty"`
	RetRaw       string         `json:"ret_raw,omitempty"`
	RetRendered  string         `json:"ret_rendered,omitempty"`
	LatencyMS    int64          `json:"latency_ms"`
	Error        string         `json:"error,omitempty"`
}

// Exchange 是一轮模型请求/响应。Replay 用它重建请求轨迹。
type Exchange struct {
	Round           int                `json:"round"`
	RequestMessages []provider.Message `json:"request_messages"`
	Response        provider.Message   `json:"response"`
	Usage           Tokens             `json:"tokens"`
	LatencyMS       int64              `json:"latency_ms"`
	Attempts        int                `json:"attempts"`
	Error           string             `json:"error,omitempty"`
}

// RetryRecord 记录一次模型请求重试；RetryOf 指向被重试的尝试 ID。
type RetryRecord struct {
	Attempt int    `json:"attempt"`
	RetryOf string `json:"retry_of"`
	DelayMS int64  `json:"delay_ms"`
	Error   string `json:"error"`
}

// Verdict 是规则判定结果。Rules 保留每条规则是否通过，便于报告解释。
type Verdict struct {
	Pass    bool            `json:"pass"`
	Reasons []string        `json:"reasons"`
	Rules   map[string]bool `json:"rules"`
	Runtime bool            `json:"runtime"`
	// Model 是可选模型裁判；规则判定与模型裁判分开记录，不互相覆盖。
	Model *ModelVerdict `json:"model,omitempty"`
}

// ModelVerdict 是模型裁判的结果。
type ModelVerdict struct {
	Pass    bool     `json:"pass"`
	Reasons []string `json:"reasons"`
	Model   string   `json:"model"`
	Error   string   `json:"error,omitempty"`
}

// Trace 是一次「用例 × 配置 × 重复」的完整轨迹。
type Trace struct {
	RunID     string     `json:"run_id"`
	CaseID    string     `json:"case_id"`
	RepeatIdx int        `json:"repeat_idx"`
	Case      cases.Case `json:"case"`
	Config    RunConfig  `json:"config"`
	Round     string     `json:"round"`
	Axis      string     `json:"axis"`

	Messages  []provider.Message `json:"messages"`
	Exchanges []Exchange         `json:"exchanges,omitempty"`
	ToolCalls []ToolCallRecord   `json:"tool_calls"`

	FinalAnswer string        `json:"final_answer"`
	RoundsUsed  int           `json:"rounds_used"`
	Tokens      Tokens        `json:"tokens"`
	TotalMS     int64         `json:"total_ms"`
	Status      Status        `json:"status"`
	Error       string        `json:"error,omitempty"`
	RetryOf     string        `json:"retry_of,omitempty"`
	Retries     []RetryRecord `json:"retries,omitempty"`
	StartedAt   time.Time     `json:"started_at"`

	Judge *Verdict `json:"judge,omitempty"`
}

// RunRecord 是 runs.jsonl 每行的形状；judge 按设计文档包在 rule 下。
type RunRecord struct {
	RunID       string             `json:"run_id"`
	CaseID      string             `json:"case_id"`
	RepeatIdx   int                `json:"repeat_idx"`
	Config      RunConfig          `json:"config"`
	Round       string             `json:"round"`
	Axis        string             `json:"axis"`
	Messages    []provider.Message `json:"messages"`
	ToolCalls   []ToolCallRecord   `json:"tool_calls"`
	FinalAnswer string             `json:"final_answer"`
	RoundsUsed  int                `json:"rounds_used"`
	Tokens      Tokens             `json:"tokens"`
	TotalMS     int64              `json:"total_ms"`
	Judge       JudgeField         `json:"judge"`
	Status      Status             `json:"status"`
	Error       string             `json:"error,omitempty"`
	RetryOf     string             `json:"retry_of,omitempty"`
	Retries     []RetryRecord      `json:"retries,omitempty"`
	StartedAt   time.Time          `json:"started_at"`
}

// JudgeField 对齐设计文档：judge.rule.{pass,reasons}。
type JudgeField struct {
	Rule *Verdict `json:"rule"`
}

// NewRunRecord 从 Trace 构造一行记录；调用方负责脱敏。
func NewRunRecord(t *Trace) RunRecord {
	rec := RunRecord{
		RunID:       t.RunID,
		CaseID:      t.CaseID,
		RepeatIdx:   t.RepeatIdx,
		Config:      t.Config,
		Round:       t.Round,
		Axis:        t.Axis,
		Messages:    t.Messages,
		ToolCalls:   t.ToolCalls,
		FinalAnswer: t.FinalAnswer,
		RoundsUsed:  t.RoundsUsed,
		Tokens:      t.Tokens,
		TotalMS:     t.TotalMS,
		Status:      t.Status,
		Error:       t.Error,
		RetryOf:     t.RetryOf,
		Retries:     t.Retries,
		StartedAt:   t.StartedAt,
	}
	if t.Judge != nil {
		rec.Judge.Rule = t.Judge
	}
	return rec
}

// NewRunID 生成可安全用于文件名的 run_id。
func NewRunID(round, caseID string, seq int) string {
	round = SafeToken(round, "r1")
	caseID = SafeToken(caseID, "case")
	id := fmt.Sprintf("%s-%04d-%s", round, seq, caseID)
	if len(id) > 120 {
		id = id[:120]
	}
	return id
}

// SafeToken 把任意标签收敛成文件名安全的形式。
func SafeToken(s, fallback string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		return fallback
	}
	return out
}
