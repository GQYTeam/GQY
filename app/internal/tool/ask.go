// ask_user：核心工具，让模型主动询问用户。
//
// 这是一个「会中断 loop」的工具：Execute 返回 ErrAskUser（带问题内容），
// agent loop 捕获后暂停，等上层拿到用户回答再回填继续。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

// Ask 是 ask_user 的载荷：模型要问用户的问题。
type Ask struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

// ErrAskUser 是 ask_user 的特殊返回值，标记「需要用户输入」。
type ErrAskUser struct {
	Ask Ask
}

func (e *ErrAskUser) Error() string { return "ask_user: 需要用户输入" }

type AskUser struct{}

func (AskUser) Name() string { return "ask_user" }
func (AskUser) Kind() Kind   { return KindCore }
func (AskUser) Description() string {
	return "向用户提问，获取必要信息后再继续。当任务目标不明确、有多个可行方案需要用户选择、或需要用户授权时使用。不要问用户能自己推断的事。"
}
func (AskUser) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question": map[string]any{"type": "string", "description": "要问用户的问题"},
			"options":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "可选答案（2-4 个），无则省略让用户自由回答"},
		},
		"required": []any{"question"},
	}
}
func (AskUser) Execute(ctx context.Context, args map[string]any) (string, error) {
	q, _ := args["question"].(string)
	if q == "" {
		return "", fmt.Errorf("question 不能为空")
	}
	var opts []string
	if raw, ok := args["options"].([]any); ok {
		for _, o := range raw {
			if s, ok := o.(string); ok {
				opts = append(opts, s)
			}
		}
	}
	return "", &ErrAskUser{Ask: Ask{Question: q, Options: opts}}
}

// ParseAsk 从 SSE 事件载荷里解析 ask_user（供上层序列化用）。
func (a Ask) JSON() []byte {
	b, _ := json.Marshal(a)
	return b
}
