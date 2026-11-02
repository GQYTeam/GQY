package tool

import (
	"context"
	"fmt"
)

// Question 是 ask_user 的提问（T7）。
type Question struct {
	Text        string   `json:"question"`
	Options     []string `json:"options,omitempty"`
	AllowCustom bool     `json:"allow_custom,omitempty"`
}

// AskError 表示一次 ask_user 调用：loop 捕获后暂停，等答案回填。
type AskError struct {
	Q Question
}

func (e *AskError) Error() string { return "tool: ask_user 需要用户回答" }

// AskUserTool 向用户提问；选项 2–4 个，数字键或自定义答案（T7）。
type AskUserTool struct{}

func (t AskUserTool) Name() string        { return "ask_user" }
func (t AskUserTool) Description() string { return "需要用户拍板时提问" }
func (t AskUserTool) Source() Source      { return SourceCore }
func (t AskUserTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question": map[string]any{"type": "string"},
			"options":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"question"},
	}
}

func (t AskUserTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	question, _ := args["question"].(string)
	if question == "" {
		return "", fmt.Errorf("%w: question 必填", ErrInvalidArgument)
	}
	q := Question{Text: question, AllowCustom: true}
	if raw, ok := args["options"].([]any); ok {
		for _, item := range raw {
			if text, ok := item.(string); ok && text != "" {
				q.Options = append(q.Options, text)
			}
		}
	}
	if len(q.Options) > 0 && (len(q.Options) < 2 || len(q.Options) > 4) {
		return "", fmt.Errorf("%w: options 必须为 2–4 个", ErrInvalidArgument)
	}
	return "", &AskError{Q: q}
}
