package httpapi

// 表情包自动发表情（task-13）集成测试：
// 真实 handler / agent loop / session + t.TempDir() 下的假 emote-pack-default 插件进程。

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gqy/internal/agent"
	"gqy/internal/emote"
	"gqy/internal/plugin"
	"gqy/internal/session"
	"gqy/internal/tool"
)

// emoteFakeReply 是假插件 suggest 的 result.content（与 v1.2.0 契约一致）。
const emoteFakeReply = `{"decision":"emote","none_score":0.021208,"predictions":[{"name":"23-比心","label":"比心","score":0.204695,"file":"emotes/23-比心.png"}]}`

// emoteFakeScript 是只实现 initialize/call(suggest)/shutdown 的 gqy-jsonrpc-v1 假插件，
// 每次 suggest 把 action 与 arguments 记进 calls.log，供断言插件确实被调用过。
const emoteFakeScript = `#!/usr/bin/env node
const fs = require('fs');
const path = require('path');
const readline = require('readline');

const dir = __dirname;
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });

function send(id, payload) {
  process.stdout.write(JSON.stringify(Object.assign({ jsonrpc: '2.0', id: id }, payload)) + '\n');
}

rl.on('line', (line) => {
  let msg;
  try { msg = JSON.parse(line); } catch (err) { return; }
  if (msg.method === 'initialize') {
    send(msg.id, { result: { ok: true, name: 'emote-pack-default', version: '1.2.0' } });
    return;
  }
  if (msg.method === 'call') {
    const params = msg.params || {};
    const args = params.arguments || {};
    fs.appendFileSync(path.join(dir, 'calls.log'), JSON.stringify({ action: params.action, arguments: args }) + '\n');
    if (params.action !== 'suggest') {
      send(msg.id, { error: { code: -32601, message: 'unknown action' } });
      return;
    }
    try {
      const content = fs.readFileSync(path.join(dir, 'reply.json'), 'utf8').trim();
      send(msg.id, { result: { content: content } });
    } catch (err) {
      send(msg.id, { error: { code: -32603, message: String(err) } });
    }
    return;
  }
  if (msg.method === 'shutdown') {
    send(msg.id, { result: { ok: true } });
    process.exit(0);
  }
  send(msg.id, { error: { code: -32601, message: 'unknown method' } });
});

rl.on('close', () => process.exit(0));
`

const emoteFakeManifest = `{
  "id": "emote-pack-default",
  "name": "表情包插件",
  "version": "1.2.0",
  "protocol": "gqy-jsonrpc-v1",
  "command": ["node", "index.js"],
  "cwd": ".",
  "timeout_ms": 10000,
  "concurrency": "concurrent",
  "actions": [{
    "name": "suggest",
    "description": "按对话语境推荐一张表情",
    "schema": {"type": "object", "properties": {"context": {"type": "string"}, "limit": {"type": "integer"}}}
  }],
  "settings": [
    {"key": "auto_emote", "title": "回复后自动发表情", "type": "boolean", "default": true},
    {"key": "min_score", "title": "最低置信度", "type": "number", "default": 0}
  ]
}`

// newEmotePlugin 在 home/plugins/emote-pack-default/ 造 manifest+index.js+reply.json，
// Scan 并 Load，返回插件运行时；测试结束自动卸载。
func newEmotePlugin(t *testing.T, home, reply string) *plugin.Manager {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node 不可用，跳过真实进程插件集成测试")
	}
	dir := filepath.Join(home, "plugins", emote.PluginID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"manifest.json": emoteFakeManifest,
		"index.js":      emoteFakeScript,
		"reply.json":    reply,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("写入 %s: %v", name, err)
		}
	}
	m := plugin.NewManager(home)
	if err := m.Scan(); err != nil {
		t.Fatalf("插件 Scan: %v", err)
	}
	t.Cleanup(func() { _ = m.Unload(context.Background(), emote.PluginID) })
	if err := m.Load(context.Background(), emote.PluginID); err != nil {
		t.Fatalf("插件 Load: %v", err)
	}
	return m
}

// emoteCalls 读假插件的调用记录。
func emoteCalls(t *testing.T, home string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "plugins", emote.PluginID, "calls.log"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("读 calls.log: %v", err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("calls.log 行不是 JSON: %q", line)
		}
		out = append(out, rec)
	}
	return out
}

// assertEmoteBeforeDone 断言 SSE 里有 emote 事件、字段齐全，且顺序在 done 之前。
func assertEmoteBeforeDone(t *testing.T, body string) {
	t.Helper()
	emoteIdx := strings.Index(body, `"type":"emote"`)
	doneIdx := strings.Index(body, `"type":"done"`)
	if emoteIdx < 0 {
		t.Fatalf("SSE 缺少 emote 事件:\n%s", body)
	}
	if doneIdx < 0 {
		t.Fatalf("SSE 缺少 done 事件:\n%s", body)
	}
	if emoteIdx > doneIdx {
		t.Fatalf("emote 应在 done 之前:\n%s", body)
	}
	for _, want := range []string{
		`"emote_name":"23-比心"`,
		`"emote_label":"比心"`,
		`"emote_file":"emotes/23-比心.png"`,
		`"emote_score":0.204695`,
		`"emote_none_score":0.021208`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("emote 事件缺少 %q:\n%s", want, body)
		}
	}
}

// assertEmotePersisted 断言事件流已落盘，且重建出的 assistant 消息带 emote。
func assertEmotePersisted(t *testing.T, home, sessionID string) {
	t.Helper()
	path := filepath.Join(home, "sessions", sessionID, "io", "context", "messages.jsonl")
	events, corrupt := session.ReadEvents(path)
	if corrupt != 0 {
		t.Fatalf("事件流有 %d 行损坏", corrupt)
	}
	var found bool
	for _, ev := range events {
		if ev.Type == session.EventEmote {
			found = true
			if ev.EmoteName != "23-比心" || ev.EmoteFile != "emotes/23-比心.png" ||
				ev.EmoteScore == nil || *ev.EmoteScore != 0.204695 ||
				ev.EmoteNoneScore == nil || *ev.EmoteNoneScore != 0.021208 {
				t.Fatalf("落盘的 emote 事件字段不对: %+v", ev)
			}
		}
	}
	if !found {
		t.Fatalf("事件流未持久化 emote: %+v", events)
	}
	messages := session.Rebuild(events)
	if len(messages) < 2 {
		t.Fatalf("消息数 = %d", len(messages))
	}
	last := messages[len(messages)-1]
	if last.Role != "assistant" || last.Emote == nil {
		t.Fatalf("重建后 assistant 消息应带 emote: %+v", last)
	}
	if last.Emote.Name != "23-比心" || last.Emote.Label != "比心" ||
		last.Emote.File != "emotes/23-比心.png" || last.Emote.Score != 0.204695 {
		t.Fatalf("重建的 emote = %+v", last.Emote)
	}
	data, err := json.Marshal(last)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"emote":{"name":"23-比心","label":"比心","file":"emotes/23-比心.png","score":0.204695}`) {
		t.Fatalf("消息 JSON 的 emote 形状不对: %s", data)
	}
}

// TestAgentChatEmoteEvent 覆盖 loop 路径：最终文本之后、done 之前发 emote 并落盘。
func TestAgentChatEmoteEvent(t *testing.T) {
	s := newChatServer(t, &chatFakeAdapter{})
	s.SetAgent(agent.NewLoop(s.gw, tool.NewRegistry()))
	home := s.mgr.Current().Home
	s.SetPlugins(newEmotePlugin(t, home, emoteFakeReply))

	rec := postPath(s, "/api/chat", `{"model":"p/m1","messages":[{"role":"user","content":"今天好累"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	assertEmoteBeforeDone(t, body)
	if !strings.Contains(body, `"type":"text"`) || !strings.Contains(body, `"你好"`) {
		t.Fatalf("既有 text 事件丢失:\n%s", body)
	}

	// 插件确实收到了本轮用户文本。
	records := emoteCalls(t, home)
	if len(records) != 1 {
		t.Fatalf("应调用一次 suggest，实际 %+v", records)
	}
	args, _ := records[0]["arguments"].(map[string]any)
	if args["context"] != "今天好累" {
		t.Fatalf("suggest 语境 = %v，期望本轮用户文本", args["context"])
	}

	sessionID := rec.Header().Get("X-GQY-Session")
	if sessionID == "" {
		t.Fatal("缺少 X-GQY-Session")
	}
	assertEmotePersisted(t, home, sessionID)
}

// TestAgentChatEmoteDisabled 覆盖 auto_emote=false：SSE 与事件流都不出现 emote，且不调用插件。
func TestAgentChatEmoteDisabled(t *testing.T) {
	s := newChatServer(t, &chatFakeAdapter{})
	s.SetAgent(agent.NewLoop(s.gw, tool.NewRegistry()))
	home := s.mgr.Current().Home
	m := newEmotePlugin(t, home, emoteFakeReply)
	if err := m.SaveSettings(emote.PluginID, map[string]any{"auto_emote": false}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	rec := postPath(s, "/api/chat", `{"model":"p/m1","messages":[{"role":"user","content":"今天好累"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, `"type":"emote"`) {
		t.Fatalf("auto_emote=false 不应有 emote 事件:\n%s", body)
	}
	if !strings.Contains(body, `"type":"done"`) {
		t.Fatalf("关闭表情不应影响 done:\n%s", body)
	}
	if records := emoteCalls(t, home); len(records) != 0 {
		t.Fatalf("关闭时不应调用插件，实际 %+v", records)
	}
	path := filepath.Join(home, "sessions", rec.Header().Get("X-GQY-Session"), "io", "context", "messages.jsonl")
	events, _ := session.ReadEvents(path)
	for _, ev := range events {
		if ev.Type == session.EventEmote {
			t.Fatalf("事件流不应有 emote: %+v", ev)
		}
	}
}

// TestDirectAgentChatEmoteEvent 覆盖未装配 loop 的直通路径：行为与 loop 路径一致。
func TestDirectAgentChatEmoteEvent(t *testing.T) {
	s := newChatServer(t, &chatFakeAdapter{}) // 不 SetAgent → streamAgentChat 直通
	home := s.mgr.Current().Home
	s.SetPlugins(newEmotePlugin(t, home, emoteFakeReply))

	rec := postPath(s, "/api/chat", `{"model":"p/m1","messages":[{"role":"user","content":"今天好累"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	assertEmoteBeforeDone(t, rec.Body.String())
	assertEmotePersisted(t, home, rec.Header().Get("X-GQY-Session"))
}

// newTestSink 直接装配 agentSink，用于覆盖 Done 前的判定（不必起完整 loop）。
func newTestSink(t *testing.T, home string, plugins *plugin.Manager) (*agentSink, *httptest.ResponseRecorder) {
	t.Helper()
	sess, err := session.NewStore(home).Create("")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	started := false
	sink := &agentSink{
		w:            rec,
		flusher:      rec,
		sess:         sess,
		started:      &started,
		writeHeaders: func() { started = true; rec.Header().Set("Content-Type", "text/event-stream; charset=utf-8") },
		ctx:          context.Background(),
		plugins:      plugins,
		log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		lastUserText: "今天好累",
	}
	return sink, rec
}

// TestAgentSinkEmoteGuards 覆盖同一次回复最多一条、问过问题/没写正文不发。
func TestAgentSinkEmoteGuards(t *testing.T) {
	t.Run("问过问题不发", func(t *testing.T) {
		home := t.TempDir()
		m := newEmotePlugin(t, home, emoteFakeReply)
		sink, rec := newTestSink(t, home, m)
		if err := sink.Text("你好"); err != nil {
			t.Fatal(err)
		}
		// 未提供 answer 时 Question 返回 ErrAskPending，但 question 事件已落盘。
		if _, err := sink.Question(tool.Question{Text: "要继续吗", Options: []string{"是", "否"}}); !errors.Is(err, agent.ErrAskPending) {
			t.Fatalf("Question err = %v，期望 ErrAskPending", err)
		}
		if !sink.questionAsked {
			t.Fatal("Question 应标记 questionAsked")
		}
		if err := sink.Done(nil, "end_turn"); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(rec.Body.String(), `"type":"emote"`) {
			t.Fatalf("问过问题不应发 emote:\n%s", rec.Body.String())
		}
	})

	t.Run("没写正文不发", func(t *testing.T) {
		home := t.TempDir()
		m := newEmotePlugin(t, home, emoteFakeReply)
		sink, rec := newTestSink(t, home, m)
		if err := sink.Done(nil, "end_turn"); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(rec.Body.String(), `"type":"emote"`) {
			t.Fatalf("没写正文不应发 emote:\n%s", rec.Body.String())
		}
	})

	t.Run("同一次回复最多一条", func(t *testing.T) {
		home := t.TempDir()
		m := newEmotePlugin(t, home, emoteFakeReply)
		sink, rec := newTestSink(t, home, m)
		if err := sink.Text("你好"); err != nil {
			t.Fatal(err)
		}
		if err := sink.Done(nil, "end_turn"); err != nil {
			t.Fatal(err)
		}
		if err := sink.Done(nil, "end_turn"); err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(rec.Body.String(), `"type":"emote"`); got != 1 {
			t.Fatalf("emote 事件数 = %d，期望 1:\n%s", got, rec.Body.String())
		}
	})
}
