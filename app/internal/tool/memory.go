package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"gqy/internal/memory"
)

// 记忆工具：只写记忆库，不写会话文件，也不产生文件产物（memory.md §5）。

const (
	// memoryMaxContentRunes 是 memory_remember 的正文上限（memory.md §5）。
	memoryMaxContentRunes = 2000
	// memoryMaxTags 是一条记忆允许的标签数上限。
	memoryMaxTags = 32
)

// MemoryRecallTool 是核心工具 memory_recall：按关键词召回长期记忆。
type MemoryRecallTool struct {
	// Home 是数据根（$GQY_HOME）。
	Home string
	// Persona 是人格库，空值取 default（MEM2）。
	Persona string
}

func (t MemoryRecallTool) Name() string { return "memory_recall" }

func (t MemoryRecallTool) Description() string {
	return "从长期记忆里按关键词召回相关的已确认事实、对话片段与被挤出上下文的轮次"
}

func (t MemoryRecallTool) Source() Source { return SourceCore }

func (t MemoryRecallTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "检索关键词；中文按双字切词，英文不区分大小写",
			},
			"limit": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     memory.MaxRecallLimit,
				"description": fmt.Sprintf("返回条数，默认 %d，最多 %d", memory.DefaultRecallLimit, memory.MaxRecallLimit),
			},
			"kinds": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
					"enum": []string{memory.KindFact, memory.KindEpisode, memory.KindEvicted},
				},
				"description": "参与召回的记录类型，默认三类全参与",
			},
		},
		"required": []string{"query"},
	}
}

func (t MemoryRecallTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("%w: query 必填", ErrInvalidArgument)
	}

	limit, hasLimit, err := memoryIntArg(args, "limit")
	if err != nil {
		return "", err
	}
	if !hasLimit {
		limit = memory.DefaultRecallLimit
	}
	if limit < 1 || limit > memory.MaxRecallLimit {
		return "", fmt.Errorf("%w: limit 取值 1–%d", ErrInvalidArgument, memory.MaxRecallLimit)
	}

	kinds, err := memoryKindsArg(args, "kinds")
	if err != nil {
		return "", err
	}

	lib, err := memoryLibrary(t.Home, t.Persona)
	if err != nil {
		return "", err
	}
	scored, err := lib.Recall(memory.RecallRequest{Query: query, Limit: limit, Kinds: kinds})
	if err != nil {
		return "", fmt.Errorf("tool: 记忆召回失败: %w", err)
	}

	items := make([]map[string]any, 0, len(scored))
	for _, item := range scored {
		content, _ := memory.TruncateContent(item.Record.Content)
		items = append(items, map[string]any{
			"id":         item.Record.ID,
			"kind":       item.Record.Kind,
			"content":    content,
			"score":      item.Score,
			"importance": item.Record.Importance,
		})
	}
	data, err := json.Marshal(items)
	if err != nil {
		return "", fmt.Errorf("tool: 序列化召回结果: %w", err)
	}
	return string(data), nil
}

// MemoryRememberTool 是核心工具 memory_remember：把一条信息写入长期记忆。
type MemoryRememberTool struct {
	// Home 是数据根（$GQY_HOME）。
	Home string
	// Persona 是人格库，空值取 default（MEM2）。
	Persona string
}

func (t MemoryRememberTool) Name() string { return "memory_remember" }

func (t MemoryRememberTool) Description() string {
	return "把一条值得长期记住的信息（事实、偏好、自我设定、纠正、任务、关系）写入记忆库"
}

func (t MemoryRememberTool) Source() Source { return SourceCore }

func (t MemoryRememberTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": fmt.Sprintf("记忆正文，1–%d 字符", memoryMaxContentRunes),
			},
			"memory_type": map[string]any{
				"type": "string",
				"enum": []string{
					memory.TypeFact, memory.TypePreference, memory.TypeSelf,
					memory.TypeCorrection, memory.TypeTask, memory.TypeRelationship,
				},
				"description": "记忆类型，默认 fact",
			},
			"importance": map[string]any{
				"type":        "integer",
				"minimum":     memory.MinImportance,
				"maximum":     memory.MaxImportance,
				"description": fmt.Sprintf("重要度 %d–%d，默认 %d", memory.MinImportance, memory.MaxImportance, memory.DefaultImportance),
			},
			"tags": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "可选标签",
			},
		},
		"required": []string{"content"},
	}
}

func (t MemoryRememberTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	content, _ := args["content"].(string)
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("%w: content 必填", ErrInvalidArgument)
	}
	if runes := []rune(content); len(runes) > memoryMaxContentRunes {
		return "", fmt.Errorf("%w: content 最多 %d 字符", ErrInvalidArgument, memoryMaxContentRunes)
	}

	memoryType := memory.TypeFact
	if raw, ok := args["memory_type"]; ok && raw != nil {
		text, ok := raw.(string)
		if !ok || !memory.ValidMemoryType(strings.TrimSpace(text)) {
			return "", fmt.Errorf("%w: memory_type 取值 %s", ErrInvalidArgument,
				"fact/preference/self/correction/task/relationship")
		}
		memoryType = strings.TrimSpace(text)
	}

	importance := memory.DefaultImportance
	if value, hasValue, err := memoryIntArg(args, "importance"); err != nil {
		return "", err
	} else if hasValue {
		if value < memory.MinImportance || value > memory.MaxImportance {
			return "", fmt.Errorf("%w: importance 取值 %d–%d", ErrInvalidArgument, memory.MinImportance, memory.MaxImportance)
		}
		importance = value
	}

	tags, err := memoryStringSliceArg(args, "tags")
	if err != nil {
		return "", err
	}
	if len(tags) > memoryMaxTags {
		return "", fmt.Errorf("%w: tags 最多 %d 个", ErrInvalidArgument, memoryMaxTags)
	}

	lib, err := memoryLibrary(t.Home, t.Persona)
	if err != nil {
		return "", err
	}
	rec, err := lib.Remember(memory.RememberInput{
		Content:    content,
		MemoryType: memoryType,
		Importance: importance,
		Tags:       tags,
		Source:     "agent",
	})
	if err != nil {
		return "", fmt.Errorf("tool: 写入记忆失败: %w", err)
	}
	return fmt.Sprintf(`{"ok":true,"id":%d}`, rec.ID), nil
}

// memoryLibrary 打开工具绑定的记忆库。
func memoryLibrary(home, persona string) (*memory.Library, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		return nil, fmt.Errorf("tool: 记忆库未配置（缺少数据根）")
	}
	persona = strings.TrimSpace(persona)
	if persona == "" {
		persona = memory.DefaultPersona
	}
	lib, err := memory.Open(home).Library(persona)
	if err != nil {
		return nil, fmt.Errorf("tool: 打开记忆库: %w", err)
	}
	return lib, nil
}

// memoryIntArg 读取整数参数：JSON 数字是 float64，也接受 int / json.Number。
func memoryIntArg(args map[string]any, key string) (int, bool, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return 0, false, nil
	}
	switch value := raw.(type) {
	case int:
		return value, true, nil
	case int64:
		return int(value), true, nil
	case float64:
		if math.Trunc(value) != value || math.IsInf(value, 0) || math.IsNaN(value) {
			return 0, false, fmt.Errorf("%w: %s 必须是整数", ErrInvalidArgument, key)
		}
		return int(value), true, nil
	case json.Number:
		n, err := value.Int64()
		if err != nil {
			return 0, false, fmt.Errorf("%w: %s 必须是整数", ErrInvalidArgument, key)
		}
		return int(n), true, nil
	}
	return 0, false, fmt.Errorf("%w: %s 必须是整数", ErrInvalidArgument, key)
}

// memoryStringSliceArg 读取字符串数组参数。
func memoryStringSliceArg(args map[string]any, key string) ([]string, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, nil
	}
	switch value := raw.(type) {
	case []string:
		return normalizeStrings(value), nil
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%w: %s 必须是字符串数组", ErrInvalidArgument, key)
			}
			out = append(out, text)
		}
		return normalizeStrings(out), nil
	}
	return nil, fmt.Errorf("%w: %s 必须是字符串数组", ErrInvalidArgument, key)
}

// memoryKindsArg 读取并校验召回类型参数。
func memoryKindsArg(args map[string]any, key string) ([]string, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, nil
	}
	values, err := memoryStringSliceArg(args, key)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !memory.RecallableKind(value) {
			return nil, fmt.Errorf("%w: %s 只支持 fact/episode/evicted", ErrInvalidArgument, key)
		}
		out = append(out, value)
	}
	return out, nil
}

// normalizeStrings 去空白、去空项并按首次出现去重。
func normalizeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}
