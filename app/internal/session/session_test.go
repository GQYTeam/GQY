package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreLifecycle(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)

	sess, err := store.Create("/tmp/workspace")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !ValidID(sess.ID()) {
		t.Fatalf("会话 id 不合法: %q", sess.ID())
	}
	for _, dir := range []string{
		sess.Dir(), sess.UploadsDir(), sess.GeneratedDir(),
		filepath.Join(sess.Dir(), "io", "context"),
		filepath.Join(sess.Dir(), "logs", "tools"),
	} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("目录 %s 不存在: %v", dir, err)
		}
	}
	if _, err := os.Stat(filepath.Join(sess.Dir(), "manifest.json")); err != nil {
		t.Fatalf("manifest 不存在: %v", err)
	}

	opened, err := store.Open(sess.ID())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened.Manifest.Workspace != "/tmp/workspace" || opened.ID() != sess.ID() {
		t.Fatalf("manifest 不一致: %+v", opened.Manifest)
	}

	list, err := store.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %d, %v", len(list), err)
	}

	if err := store.Delete(sess.ID()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Open(sess.ID()); err == nil {
		t.Fatal("删除后 Open 应失败")
	}
}

func TestValidIDRejectsTraversal(t *testing.T) {
	for _, id := range []string{"../etc", "a/b", "A_B", "", strings.Repeat("a", 65)} {
		if ValidID(id) {
			t.Fatalf("%q 应被判非法", id)
		}
	}
	for _, id := range []string{"a", "abc-123", "0"} {
		if !ValidID(id) {
			t.Fatalf("%q 应合法", id)
		}
	}
}

func TestEventsAndRebuild(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	path := sess.EventsPath()
	for _, ev := range []Event{
		{Type: EventUser, Content: "hi"},
		{Type: EventThinking, Delta: "想"},
		{Type: EventText, Delta: "你"},
		{Type: EventText, Delta: "好"},
		{Type: EventTool, Phase: "start", ToolName: "read", Arguments: map[string]any{"path": "a.txt"}},
		{Type: EventTool, Phase: "result", ToolName: "read", Output: "ok", DurationMS: 5, Status: "ok"},
		{Type: EventDone, Usage: &Usage{PromptTokens: 1, CompletionTokens: 2}, StopReason: "end_turn"},
	} {
		if err := AppendEvent(path, ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("not-json\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	events, corrupt := ReadEvents(path)
	if corrupt != 1 || len(events) != 7 {
		t.Fatalf("ReadEvents = %d events, %d corrupt", len(events), corrupt)
	}
	messages := Rebuild(events)
	if len(messages) != 2 {
		t.Fatalf("消息数 = %d: %+v", len(messages), messages)
	}
	if messages[0].Role != "user" || messages[0].Content != "hi" {
		t.Fatalf("用户消息不对: %+v", messages[0])
	}
	assistant := messages[1]
	if assistant.Role != "assistant" || assistant.Thinking != "想" || assistant.Content != "你好" {
		t.Fatalf("助手消息不对: %+v", assistant)
	}
	if len(assistant.Tools) != 1 || assistant.Tools[0].Output != "ok" || assistant.Tools[0].DurationMS != 5 {
		t.Fatalf("工具轨迹不对: %+v", assistant.Tools)
	}
	if assistant.Usage == nil || assistant.Usage.PromptTokens != 1 || assistant.Usage.CompletionTokens != 2 {
		t.Fatalf("usage 不对: %+v", assistant.Usage)
	}
	if assistant.Status != "complete" || assistant.StopReason != "end_turn" {
		t.Fatalf("完成状态不对: %+v", assistant)
	}

	path2 := sess.Dir() + "/incomplete.jsonl"
	if err := AppendEvent(path2, Event{Type: EventUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendEvent(path2, Event{Type: EventText, Delta: "半截"}); err != nil {
		t.Fatal(err)
	}
	events, _ = ReadEvents(path2)
	msgs := Rebuild(events)
	if len(msgs) != 2 || msgs[1].Status != "incomplete" {
		t.Fatalf("未完成消息应标记 incomplete: %+v", msgs)
	}
}

func TestDraft(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draft.txt")
	if got, err := LoadDraft(path); err != nil || got != "" {
		t.Fatalf("缺失草稿应为空: %q, %v", got, err)
	}
	if err := SaveDraft(path, "草稿内容"); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadDraft(path); err != nil || got != "草稿内容" {
		t.Fatalf("草稿 = %q, %v", got, err)
	}
}

func TestSaveUploadNoOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uploads")
	path, err := SaveUpload(dir, "a.txt", []byte("one"))
	if err != nil {
		t.Fatalf("SaveUpload: %v", err)
	}
	if path != filepath.Join(dir, "a.txt") {
		t.Fatalf("路径 = %q", path)
	}
	if _, err := SaveUpload(dir, "a.txt", []byte("two")); err == nil {
		t.Fatal("同名附件不应覆盖")
	}
	if _, err := SaveUpload(dir, "../evil.txt", []byte("x")); err != nil {
		t.Fatalf("穿越文件名应只取 basename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil.txt")); err == nil {
		t.Fatal("附件逃逸到父目录")
	}
	if _, err := SaveUpload(dir, "..", []byte("x")); err == nil {
		t.Fatal(".. 应被拒绝")
	}
}

func TestStopKeepsIncrementsCanceled(t *testing.T) {
	msgs := Rebuild([]Event{
		{Type: EventUser, Content: "hi"},
		{Type: EventText, Delta: "已产出"},
		{Type: EventDone, StopReason: "canceled"},
	})
	if len(msgs) != 2 || msgs[1].Status != "canceled" || msgs[1].Content != "已产出" {
		t.Fatalf("停止应保留增量并标 canceled: %+v", msgs)
	}
}

func TestRetryKeepsHistorySuperseded(t *testing.T) {
	msgs := Rebuild([]Event{
		{Type: EventUser, Content: "第一问"},
		{Type: EventText, Delta: "旧答案"},
		{Type: EventDone, StopReason: "end_turn"},
		{Type: EventUser, Content: "第一问"},
		{Type: EventText, Delta: "新答案"},
		{Type: EventDone, StopReason: "end_turn"},
	})
	if len(msgs) != 4 {
		t.Fatalf("消息数 = %d: %+v", len(msgs), msgs)
	}
	if msgs[1].Status != "superseded" || msgs[1].Content != "旧答案" {
		t.Fatalf("旧回复应折叠为 superseded: %+v", msgs[1])
	}
	if msgs[3].Status != "complete" || msgs[3].Content != "新答案" {
		t.Fatalf("新回复 = %+v", msgs[3])
	}
}

func TestArtifactFinalOnly(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AddArtifact("", "/out/report.md"); err != nil {
		t.Fatal(err)
	}
	if err := sess.AddArtifact("intermediate", "/tmp/generated/x.go"); err != nil {
		t.Fatal(err)
	}
	if len(sess.Manifest.Artifacts) != 1 || sess.Manifest.Artifacts[0].Path != "/out/report.md" {
		t.Fatalf("manifest 只记最终产物: %+v", sess.Manifest.Artifacts)
	}
}
