package emote

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"gqy/internal/plugin"
	"gqy/internal/session"
)

// fakePluginScript 是一个最小的 gqy-jsonrpc-v1 假插件：
// - initialize → {ok:true}
// - call suggest → 把 reply.json 的内容原样当作 result.content 返回
// - 每次 call 把 action/arguments 追加进 calls.log，供测试断言是否真的调用了插件
const fakePluginScript = `#!/usr/bin/env node
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

// fakeManifest 与已安装的 emote-pack-default v1.2.0 的 suggest/settings 形状一致。
const fakeManifest = `{
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
    "schema": {"type": "object", "properties": {
      "context": {"type": "string", "minLength": 1, "maxLength": 2000},
      "limit": {"type": "integer", "minimum": 1, "maximum": 5}
    }}
  }],
  "settings": [
    {"key": "auto_emote", "title": "回复后自动发表情", "type": "boolean", "default": true},
    {"key": "min_score", "title": "最低置信度", "type": "number", "default": 0}
  ]
}`

const emoteReply = `{"decision":"emote","none_score":0.021208,"predictions":[{"name":"23-比心","label":"比心","score":0.204695,"file":"emotes/23-比心.png"}]}`

// newFakeManager 在 t.TempDir() 下造 $HOME/plugins/emote-pack-default/（manifest + index.js + reply.json），
// 返回已 Scan 的插件管理器；调用方再决定是否 Load。
func newFakeManager(t *testing.T, reply string) (*plugin.Manager, string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node 不可用，跳过真实进程插件测试")
	}
	home := t.TempDir()
	dir := filepath.Join(home, "plugins", PluginID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"manifest.json": fakeManifest,
		"index.js":      fakePluginScript,
		"reply.json":    reply,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("写入 %s: %v", name, err)
		}
	}
	m := plugin.NewManager(home)
	if err := m.Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	t.Cleanup(func() { _ = m.Unload(context.Background(), PluginID) })
	return m, home
}

// loadFake 加载假插件；失败直接 Fatal。
func loadFake(t *testing.T, m *plugin.Manager) {
	t.Helper()
	if err := m.Load(context.Background(), PluginID); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func newPicker(m *plugin.Manager) *Picker {
	return &Picker{Plugins: m, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// calls 读假插件的调用记录；没有调用时返回空切片。
func calls(t *testing.T, home string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "plugins", PluginID, "calls.log"))
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

func saveSettings(t *testing.T, m *plugin.Manager, values map[string]any) {
	t.Helper()
	if err := m.SaveSettings(PluginID, values); err != nil {
		t.Fatalf("SaveSettings(%v): %v", values, err)
	}
}

// TestPickDefaultSettings 覆盖默认设置（auto_emote 缺省 true、min_score 缺省 0）下的成功路径。
func TestPickDefaultSettings(t *testing.T) {
	m, home := newFakeManager(t, emoteReply)
	loadFake(t, m)

	got, ok := newPicker(m).Pick(context.Background(), "今天好累，想休息")
	if !ok {
		t.Fatal("默认设置下应挑出一张表情")
	}
	want := session.Emote{Name: "23-比心", Label: "比心", File: "emotes/23-比心.png", Score: 0.204695, NoneScore: 0.021208}
	if got != want {
		t.Fatalf("表情 = %+v，期望 %+v", got, want)
	}
	records := calls(t, home)
	if len(records) != 1 || records[0]["action"] != "suggest" {
		t.Fatalf("应恰好调用一次 suggest，实际 %+v", records)
	}
	args, _ := records[0]["arguments"].(map[string]any)
	if args["context"] != "今天好累，想休息" || args["limit"] != float64(3) {
		t.Fatalf("suggest 参数 = %+v", args)
	}
}

// TestPickAutoEmoteDisabled 覆盖 auto_emote=false 时不调用插件也不发。
func TestPickAutoEmoteDisabled(t *testing.T) {
	m, home := newFakeManager(t, emoteReply)
	saveSettings(t, m, map[string]any{"auto_emote": false})
	loadFake(t, m)

	if got, ok := newPicker(m).Pick(context.Background(), "今天好累"); ok {
		t.Fatalf("auto_emote=false 不应发，得到 %+v", got)
	}
	if records := calls(t, home); len(records) != 0 {
		t.Fatalf("关闭时不应调用插件，实际 %+v", records)
	}
}

// TestPickMinScoreFilter 覆盖 min_score 过滤与 [0,1] clamp。
func TestPickMinScoreFilter(t *testing.T) {
	m, _ := newFakeManager(t, emoteReply)
	loadFake(t, m)
	p := newPicker(m)
	ctx := context.Background()

	cases := []struct {
		name     string
		minScore float64
		want     bool
	}{
		{"高于 top1", 0.5, false},
		{"等于 top1", 0.204695, true},
		{"低于 top1", 0.1, true},
		{"负数 clamp 到 0", -1, true},
		{"大于 1 clamp 到 1", 5, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			saveSettings(t, m, map[string]any{"min_score": tc.minScore})
			_, ok := p.Pick(ctx, "今天好累")
			if ok != tc.want {
				t.Fatalf("min_score=%v 时 ok=%v，期望 %v", tc.minScore, ok, tc.want)
			}
		})
	}
}

// TestPickNotLoaded 覆盖插件已发现但未加载（state != ready）。
func TestPickNotLoaded(t *testing.T) {
	m, home := newFakeManager(t, emoteReply) // 只 Scan，不 Load
	if got, ok := newPicker(m).Pick(context.Background(), "今天好累"); ok {
		t.Fatalf("未加载不应发，得到 %+v", got)
	}
	if records := calls(t, home); len(records) != 0 {
		t.Fatalf("未加载不应调用插件，实际 %+v", records)
	}
}

// TestPickPluginMissing 覆盖插件未安装。
func TestPickPluginMissing(t *testing.T) {
	m := plugin.NewManager(t.TempDir())
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	if got, ok := newPicker(m).Pick(context.Background(), "今天好累"); ok {
		t.Fatalf("插件缺失不应发，得到 %+v", got)
	}
}

// TestPickEmptyContext 覆盖语境过短（空/全空白）时直接跳过、不调用插件。
func TestPickEmptyContext(t *testing.T) {
	m, home := newFakeManager(t, emoteReply)
	loadFake(t, m)
	p := newPicker(m)
	for _, text := range []string{"", "   ", "\n\t "} {
		if got, ok := p.Pick(context.Background(), text); ok {
			t.Fatalf("语境 %q 过短仍发了 %+v", text, got)
		}
	}
	if records := calls(t, home); len(records) != 0 {
		t.Fatalf("语境过短不应调用插件，实际 %+v", records)
	}
}

// TestPickDecisionAndErrors 覆盖 decision=none、预测为空、JSON 解析失败、缺 name。
func TestPickDecisionAndErrors(t *testing.T) {
	cases := []struct {
		name  string
		reply string
	}{
		{"decision none", `{"decision":"none","none_score":0.35938,"predictions":[]}`},
		{"emote 但预测为空", `{"decision":"emote","none_score":0.1,"predictions":[]}`},
		{"不是 JSON", `not-json`},
		{"缺少 name", `{"decision":"emote","none_score":0.1,"predictions":[{"label":"比心","score":0.9}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newFakeManager(t, tc.reply)
			loadFake(t, m)
			if got, ok := newPicker(m).Pick(context.Background(), "今天好累"); ok {
				t.Fatalf("%s 不应发，得到 %+v", tc.name, got)
			}
		})
	}
}

// TestPickCallFailure 覆盖插件调用失败（这里让假插件读不到 reply.json 而返回 error）时静默跳过。
func TestPickCallFailure(t *testing.T) {
	m, home := newFakeManager(t, emoteReply)
	loadFake(t, m)
	if err := os.Remove(filepath.Join(home, "plugins", PluginID, "reply.json")); err != nil {
		t.Fatal(err)
	}
	if got, ok := newPicker(m).Pick(context.Background(), "今天好累"); ok {
		t.Fatalf("调用失败不应发，得到 %+v", got)
	}
}

// TestPickTop1NotOrdered 覆盖 top1 取分数最高者，不依赖插件返回顺序。
func TestPickTop1NotOrdered(t *testing.T) {
	reply := `{"decision":"emote","none_score":0.02,"predictions":[
		{"name":"01-低分","label":"低","score":0.1,"file":"emotes/01-低分.png"},
		{"name":"02-高分","label":"高","score":0.8,"file":"emotes/02-高分.png"}
	]}`
	m, _ := newFakeManager(t, reply)
	loadFake(t, m)
	got, ok := newPicker(m).Pick(context.Background(), "今天好累")
	if !ok || got.Name != "02-高分" || got.Score != 0.8 {
		t.Fatalf("top1 = %+v, ok=%v，期望 02-高分", got, ok)
	}
}

// TestPickContextTruncated 覆盖超长语境按 2000 字符截断后传给插件。
func TestPickContextTruncated(t *testing.T) {
	m, home := newFakeManager(t, emoteReply)
	loadFake(t, m)

	long := strings.Repeat("长", maxContextRunes+300)
	if _, ok := newPicker(m).Pick(context.Background(), long); !ok {
		t.Fatal("超长语境应截断后正常挑表情")
	}
	records := calls(t, home)
	if len(records) != 1 {
		t.Fatalf("应调用一次 suggest，实际 %d", len(records))
	}
	args, _ := records[0]["arguments"].(map[string]any)
	contextText, _ := args["context"].(string)
	if runes := utf8.RuneCountInString(contextText); runes != maxContextRunes {
		t.Fatalf("语境长度 = %d，期望 %d", runes, maxContextRunes)
	}
}

// TestPickNilManager 覆盖未装配插件运行时。
func TestPickNilManager(t *testing.T) {
	if _, ok := (&Picker{}).Pick(context.Background(), "今天好累"); ok {
		t.Fatal("插件运行时缺失不应发")
	}
}

// TestAutoEmoteDefault 覆盖 auto_emote 缺省 true、只有显式 false 才关闭。
func TestAutoEmoteDefault(t *testing.T) {
	cases := []struct {
		name     string
		settings map[string]any
		want     bool
	}{
		{"缺省", map[string]any{}, true},
		{"nil", nil, true},
		{"显式 true", map[string]any{"auto_emote": true}, true},
		{"显式 false", map[string]any{"auto_emote": false}, false},
		{"类型不对回落默认", map[string]any{"auto_emote": "false"}, true},
	}
	for _, tc := range cases {
		if got := autoEmote(tc.settings); got != tc.want {
			t.Fatalf("%s: autoEmote = %v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// TestMinScoreClamp 覆盖 min_score 缺省 0 与 clamp [0,1]。
func TestMinScoreClamp(t *testing.T) {
	cases := []struct {
		name     string
		settings map[string]any
		want     float64
	}{
		{"缺省", map[string]any{}, 0},
		{"正常值", map[string]any{"min_score": 0.35}, 0.35},
		{"负数", map[string]any{"min_score": -3.0}, 0},
		{"大于 1", map[string]any{"min_score": 7.0}, 1},
		{"整数", map[string]any{"min_score": 1}, 1},
		{"非法类型", map[string]any{"min_score": "0.5"}, 0},
		{"NaN", map[string]any{"min_score": math.NaN()}, 0},
	}
	for _, tc := range cases {
		if got := minScore(tc.settings); got != tc.want {
			t.Fatalf("%s: minScore = %v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// Emote 与 session.Emote 的字段契约一致（name/label/file/score + 内部 none_score）。
func TestEmoteShape(t *testing.T) {
	m, _ := newFakeManager(t, emoteReply)
	loadFake(t, m)
	got, ok := newPicker(m).Pick(context.Background(), "今天好累")
	if !ok {
		t.Fatal("应挑出表情")
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]any
	if err := json.Unmarshal(data, &shape); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"name", "label", "file", "score"} {
		if _, ok := shape[key]; !ok {
			t.Fatalf("表情 JSON 缺少 %q: %s", key, data)
		}
	}
	if _, ok := shape["none_score"]; ok {
		t.Fatalf("none_score 不应随消息回放: %s", data)
	}
}
