package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// 六事件类型（S2，task-13 把 emote 纳入对外事件）；`user` 是内部日志类型，不属于对外事件。
const (
	EventUser     = "user"
	EventThinking = "thinking"
	EventTool     = "tool"
	EventText     = "text"
	EventQuestion = "question"
	EventDone     = "done"
	// EventBranch 是主线运行中排队的支线消息（013），不属于对外事件。
	EventBranch = "branch"
	// EventEmote 是回复完成后自动挑中的表情（task-13），排在 text 之后、done 之前，
	// 是对外六事件之一。
	EventEmote = "emote"
)

// Emote 是一次自动发表情的结果（task-13）。
// Message 里只回放 name/label/file/score；NoneScore 只进 emote 事件，不随消息回放。
type Emote struct {
	Name  string  `json:"name"`
	Label string  `json:"label,omitempty"`
	File  string  `json:"file,omitempty"`
	Score float64 `json:"score"`
	// NoneScore 是模型判断「该语境不该发表情」的概率（emote_none_score）。
	NoneScore float64 `json:"-"`
}

// Usage 是 done 事件携带的用量（沿用 03 C2 的形状）。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Event 是消息事件流的一行，也是 /api/chat 的 SSE 载荷（S2）。
type Event struct {
	Type string `json:"type"`

	// text / thinking / user
	Delta   string `json:"delta,omitempty"`
	Content string `json:"content,omitempty"`

	// tool
	ToolName   string `json:"tool_name,omitempty"`
	Phase      string `json:"phase,omitempty"` // start | result
	Arguments  any    `json:"arguments,omitempty"`
	Output     string `json:"output,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Status     string `json:"status,omitempty"`

	// question
	Question    string   `json:"question,omitempty"`
	Options     []string `json:"options,omitempty"`
	AllowCustom bool     `json:"allow_custom,omitempty"`

	// branch（013/014/017）
	BranchID string `json:"branch_id,omitempty"`

	// 工具调用的产物路径（022）
	ArtifactPaths []string `json:"artifact_paths,omitempty"`

	// emote（task-13）：回复完成后自动挑中的表情。
	// score/none_score 用指针，既保证零值也能按契约输出，又不会污染其他事件。
	EmoteName      string   `json:"emote_name,omitempty"`
	EmoteLabel     string   `json:"emote_label,omitempty"`
	EmoteFile      string   `json:"emote_file,omitempty"`
	EmoteScore     *float64 `json:"emote_score,omitempty"`
	EmoteNoneScore *float64 `json:"emote_none_score,omitempty"`

	// done
	Usage      *Usage `json:"usage,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}

// ToolTrace 是一条工具轨迹（S2）。
type ToolTrace struct {
	Name       string `json:"name"`
	Arguments  any    `json:"arguments,omitempty"`
	Output     string `json:"output,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Status     string `json:"status,omitempty"`
}

// Message 是从事件流重建出的消息（S2）。
type Message struct {
	Role       string      `json:"role"`
	Content    string      `json:"content"`
	Thinking   string      `json:"thinking,omitempty"`
	Tools      []ToolTrace `json:"tools,omitempty"`
	Usage      *Usage      `json:"usage,omitempty"`
	StopReason string      `json:"stop_reason,omitempty"`
	Status     string      `json:"status,omitempty"` // complete | incomplete

	Question        string   `json:"question,omitempty"`
	QuestionOptions []string `json:"question_options,omitempty"`
	QuestionCustom  bool     `json:"question_allow_custom,omitempty"`

	// Emote 是本条助手回复的自动表情（task-13）；旧会话没有该事件，保持缺省。
	Emote *Emote `json:"emote,omitempty"`
}

// AppendEvent 向事件流追加一行（完整行写，ST4）。
func AppendEvent(path string, ev Event) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 -- 路径来自会话目录
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = f.Write(data)
	return err
}

// ReadEvents 读取事件流；损坏行跳过并计数（ST4）。
func ReadEvents(path string) ([]Event, int) {
	f, err := os.Open(path) // #nosec G304 -- 路径来自会话目录
	if err != nil {
		return []Event{}, 0
	}
	defer func() { _ = f.Close() }()

	out := make([]Event, 0, 64)
	corrupt := 0
	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			var ev Event
			if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.Type == "" {
				corrupt++
			} else {
				out = append(out, ev)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			break
		}
	}
	return out, corrupt
}

// Rebuild 把事件流投影成消息列表：user 事件起一条用户消息，其余事件累积进当前
// 助手消息，直到 done 收尾；没有 done 的助手消息标 incomplete（S2、ST5）。
func Rebuild(events []Event) []Message {
	out := make([]Message, 0, len(events)/2)
	var cur *Message
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}

	for _, ev := range events {
		switch ev.Type {
		case EventUser:
			flush()
			if len(out) > 0 && out[len(out)-1].Role == "assistant" && out[len(out)-1].Status == "complete" {
				out[len(out)-1].Status = "superseded"
			}
			out = append(out, Message{Role: "user", Content: ev.Content})
		case EventThinking:
			if cur == nil {
				cur = &Message{Role: "assistant"}
			}
			cur.Thinking += ev.Delta
		case EventText:
			if cur == nil {
				cur = &Message{Role: "assistant"}
			}
			cur.Content += ev.Delta
		case EventTool:
			if cur == nil {
				cur = &Message{Role: "assistant"}
			}
			if ev.Phase == "start" {
				cur.Tools = append(cur.Tools, ToolTrace{Name: ev.ToolName, Arguments: ev.Arguments})
				break
			}
			if n := len(cur.Tools); n > 0 {
				last := &cur.Tools[n-1]
				if last.Name == ev.ToolName || last.Name == "" {
					last.Name = ev.ToolName
					last.Output = ev.Output
					last.DurationMS = ev.DurationMS
					last.Status = ev.Status
					break
				}
			}
			cur.Tools = append(cur.Tools, ToolTrace{
				Name:       ev.ToolName,
				Arguments:  ev.Arguments,
				Output:     ev.Output,
				DurationMS: ev.DurationMS,
				Status:     ev.Status,
			})
		case EventQuestion:
			if cur == nil {
				cur = &Message{Role: "assistant"}
			}
			cur.Question = ev.Question
			cur.QuestionOptions = append([]string(nil), ev.Options...)
			cur.QuestionCustom = ev.AllowCustom
		case EventEmote:
			if cur == nil {
				cur = &Message{Role: "assistant"}
			}
			if ev.EmoteName != "" {
				cur.Emote = &Emote{Name: ev.EmoteName, Label: ev.EmoteLabel, File: ev.EmoteFile}
				if ev.EmoteScore != nil {
					cur.Emote.Score = *ev.EmoteScore
				}
				if ev.EmoteNoneScore != nil {
					cur.Emote.NoneScore = *ev.EmoteNoneScore
				}
			}
		case EventDone:
			if cur == nil {
				cur = &Message{Role: "assistant"}
			}
			cur.Usage = ev.Usage
			cur.StopReason = ev.StopReason
			if ev.StopReason == "canceled" {
				cur.Status = "canceled"
			} else {
				cur.Status = "complete"
			}
			flush()
		}
	}
	if cur != nil {
		cur.Status = "incomplete"
		flush()
	}
	return out
}
