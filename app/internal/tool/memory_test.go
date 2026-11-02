package tool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"gqy/internal/memory"
)

func TestToolMemoryRecallRemember(t *testing.T) {
	home := t.TempDir()
	recall := MemoryRecallTool{Home: home}
	remember := MemoryRememberTool{Home: home}

	if recall.Name() != "memory_recall" || recall.Source() != SourceCore {
		t.Fatalf("memory_recall 元信息不对: %s", recall.Name())
	}
	if remember.Name() != "memory_remember" || remember.Source() != SourceCore {
		t.Fatalf("memory_remember 元信息不对: %s", remember.Name())
	}
	if recall.Schema()["required"] == nil || remember.Schema()["required"] == nil {
		t.Fatal("两个工具都应有 required 字段")
	}

	// 空库召回是空数组，不是错误。
	out, err := recall.Execute(context.Background(), map[string]any{"query": "咖啡"})
	if err != nil {
		t.Fatalf("空库召回: %v", err)
	}
	var empty []map[string]any
	if err := json.Unmarshal([]byte(out), &empty); err != nil {
		t.Fatalf("召回结果不是 JSON 数组: %v\n%s", err, out)
	}
	if len(empty) != 0 {
		t.Fatalf("空库召回 = %s", out)
	}

	// 写入后立即可召回（内存索引同步更新）。
	out, err = remember.Execute(context.Background(), map[string]any{
		"content":     "用户喜欢美式咖啡，不加糖",
		"memory_type": "preference",
		"importance":  float64(4),
		"tags":        []any{"偏好", "饮品"},
	})
	if err != nil {
		t.Fatalf("memory_remember: %v", err)
	}
	var saved struct {
		OK bool  `json:"ok"`
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &saved); err != nil || !saved.OK || saved.ID <= 0 {
		t.Fatalf("memory_remember 返回不对: %s", out)
	}

	out, err = recall.Execute(context.Background(), map[string]any{"query": "咖啡"})
	if err != nil {
		t.Fatalf("memory_recall: %v", err)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("召回结果不是 JSON 数组: %v\n%s", err, out)
	}
	if len(items) != 1 {
		t.Fatalf("召回条数 = %d, want 1（写入后应立即可召回）: %s", len(items), out)
	}
	if items[0]["id"].(float64) != float64(saved.ID) || items[0]["kind"] != "fact" {
		t.Fatalf("召回条目不对: %v", items[0])
	}
	if _, ok := items[0]["score"].(float64); !ok {
		t.Fatalf("召回条目缺 score: %v", items[0])
	}

	// 落盘到 memory/<persona>/facts.jsonl。
	lib, err := memory.Open(home).Library(memory.DefaultPersona)
	if err != nil {
		t.Fatal(err)
	}
	counts, err := lib.Counts()
	if err != nil {
		t.Fatal(err)
	}
	if counts.Facts != 1 {
		t.Fatalf("facts = %d, want 1", counts.Facts)
	}

	// 参数校验：都按 ErrInvalidArgument 包装。
	cases := []struct {
		name string
		tool Tool
		args map[string]any
	}{
		{"召回缺 query", recall, map[string]any{}},
		{"召回 query 空", recall, map[string]any{"query": "   "}},
		{"召回 limit 越界", recall, map[string]any{"query": "x", "limit": float64(21)}},
		{"召回 limit 负数", recall, map[string]any{"query": "x", "limit": float64(-1)}},
		{"召回 limit 非整数", recall, map[string]any{"query": "x", "limit": 1.5}},
		{"召回 kinds 非法", recall, map[string]any{"query": "x", "kinds": []any{"revision"}}},
		{"写入缺 content", remember, map[string]any{}},
		{"写入 content 空", remember, map[string]any{"content": "  "}},
		{"写入 content 超长", remember, map[string]any{"content": string(make([]rune, memoryMaxContentRunes+1))}},
		{"写入 memory_type 非法", remember, map[string]any{"content": "x", "memory_type": "bogus"}},
		{"写入 importance 越界", remember, map[string]any{"content": "x", "importance": float64(6)}},
		{"写入 tags 类型错", remember, map[string]any{"content": "x", "tags": "偏好"}},
	}
	for _, tc := range cases {
		if _, err := tc.tool.Execute(context.Background(), tc.args); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("%s: err = %v, want ErrInvalidArgument", tc.name, err)
		}
	}

	// 没有数据根时明确报错，不 panic。
	if _, err := (MemoryRecallTool{}).Execute(context.Background(), map[string]any{"query": "x"}); err == nil {
		t.Fatal("缺少数据根应报错")
	}
	if _, err := (MemoryRememberTool{}).Execute(context.Background(), map[string]any{"content": "x"}); err == nil {
		t.Fatal("缺少数据根应报错")
	}
}

func TestToolMemoryRecallKindsAndLimit(t *testing.T) {
	home := t.TempDir()
	recall := MemoryRecallTool{Home: home}
	remember := MemoryRememberTool{Home: home}
	for _, content := range []string{"喜欢拿铁", "喜欢美式", "喜欢手冲"} {
		if _, err := remember.Execute(context.Background(), map[string]any{"content": content, "importance": float64(5)}); err != nil {
			t.Fatalf("写入: %v", err)
		}
	}

	out, err := recall.Execute(context.Background(), map[string]any{"query": "喜欢", "limit": float64(2)})
	if err != nil {
		t.Fatalf("召回: %v", err)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("解析: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("limit 未生效: %d", len(items))
	}

	// kinds 只影响参与打分的类型；只有 facts 时应只有 fact。
	out, err = recall.Execute(context.Background(), map[string]any{
		"query": "喜欢",
		"kinds": []any{"episode"},
	})
	if err != nil {
		t.Fatalf("召回: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("解析: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("kinds=episode 应为空: %s", out)
	}
}
