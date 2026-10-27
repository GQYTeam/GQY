package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"gqy/internal/tool"
)

func requireNode(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("没有 node，跳过插件进程测试")
	}
}

func writePlugin(t *testing.T, home, id string, m Manifest, script string) {
	t.Helper()
	dir := filepath.Join(home, "plugins", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m.ID, m.Protocol = id, "gqy-jsonrpc-v1"
	if m.Command == nil {
		m.Command = []string{"node", "index.js"}
	}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
}

const echoScript = `
'use strict';
const readline = require('readline');
const rl = readline.createInterface({ input: process.stdin });
function send(o){ process.stdout.write(JSON.stringify(o)+'\n'); }
rl.on('line', (line) => {
  let r; try { r = JSON.parse(line); } catch (e) { return; }
  if (r.method === 'initialize') return send({jsonrpc:'2.0', id:r.id, result:{ok:true}});
  if (r.method === 'shutdown') { send({jsonrpc:'2.0', id:r.id, result:{ok:true}}); setTimeout(()=>process.exit(0), 10); return; }
  if (r.method === 'call') {
    const a = (r.params && r.params.arguments) || {};
    if (r.params.action === 'slow') { setTimeout(()=>send({jsonrpc:'2.0', id:r.id, result:{content:'late'}}), 2000); return; }
    return send({jsonrpc:'2.0', id:r.id, result:{content: a.text || 'empty'}});
  }
  send({jsonrpc:'2.0', id:r.id, error:{code:-32601, message:'unknown'}});
});
rl.on('close', ()=>process.exit(0));
`

func TestNativePluginLoadCallUnload(t *testing.T) {
	requireNode(t)
	home := t.TempDir()
	writePlugin(t, home, "echo", Manifest{
		Name: "Echo", TimeoutMS: 3000,
		Actions: []Action{{Name: "echo", Description: "echo", Schema: map[string]any{"type": "object"}}},
	}, echoScript)

	reg := tool.NewRegistry()
	m := NewManager(home, reg)
	if errs := m.Discover(); len(errs) != 0 {
		t.Fatalf("discover: %v", errs)
	}
	if got := m.List(); len(got) != 1 || got[0].State != "discovered" {
		t.Fatalf("list: %+v", got)
	}
	if err := m.Load(context.Background(), "echo"); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := reg.Get("echo__echo"); !ok {
		t.Fatal("工具未注册")
	}
	out, err := m.Call(context.Background(), "echo", "echo", map[string]any{"text": "hi"})
	if err != nil || out != "hi" {
		t.Fatalf("call: %q %v", out, err)
	}
	if err := m.Unload(context.Background(), "echo"); err != nil {
		t.Fatalf("unload: %v", err)
	}
	if _, ok := reg.Get("echo__echo"); ok {
		t.Fatal("工具未注销")
	}
	if err := m.Unload(context.Background(), "echo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复卸载应返回 not found: %v", err)
	}
	m.Close()
}

func TestPluginTimeout(t *testing.T) {
	requireNode(t)
	home := t.TempDir()
	writePlugin(t, home, "slow", Manifest{
		Name: "Slow", TimeoutMS: 150,
		Actions: []Action{{Name: "slow"}},
	}, echoScript)
	m := NewManager(home, tool.NewRegistry())
	_ = m.Discover()
	if err := m.Load(context.Background(), "slow"); err != nil {
		t.Fatalf("load: %v", err)
	}
	start := time.Now()
	_, err := m.Call(context.Background(), "slow", "slow", map[string]any{})
	if err == nil {
		t.Fatal("期望超时")
	}
	if time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("超时过慢：%s", time.Since(start))
	}
	_ = m.Unload(context.Background(), "slow")
	m.Close()
}

func TestDuplicateManifest(t *testing.T) {
	requireNode(t)
	home := t.TempDir()
	writePlugin(t, home, "a", Manifest{Name: "A", Actions: []Action{{Name: "x"}}}, echoScript)
	// 第二个目录使用同一个 id。
	dir := filepath.Join(home, "plugins", "b")
	_ = os.MkdirAll(dir, 0o700)
	raw, _ := json.Marshal(Manifest{ID: "a", Name: "B", Protocol: "gqy-jsonrpc-v1", Command: []string{"node", "index.js"}, Actions: []Action{{Name: "y"}}})
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o600)
	_ = os.WriteFile(filepath.Join(dir, "index.js"), []byte(echoScript), 0o600)

	m := NewManager(home, tool.NewRegistry())
	errs := m.Discover()
	if len(errs) == 0 {
		t.Fatal("期望重复 id 报错")
	}
	if got := m.List(); len(got) != 1 {
		t.Fatalf("只应保留一个插件：%+v", got)
	}
	m.Close()
}

const mcpScript = `
'use strict';
const readline = require('readline');
const rl = readline.createInterface({ input: process.stdin });
function send(o){ process.stdout.write(JSON.stringify(o)+'\n'); }
rl.on('line', (line) => {
  let r; try { r = JSON.parse(line); } catch (e) { return; }
  if (r.method === 'initialize') return send({jsonrpc:'2.0', id:r.id, result:{protocolVersion:'2024-11-05', capabilities:{}, serverInfo:{name:'mcp-test'}}});
  if (r.method === 'notifications/initialized') return;
  if (r.method === 'tools/list') return send({jsonrpc:'2.0', id:r.id, result:{tools:[{name:'add', description:'add two', inputSchema:{type:'object', properties:{a:{type:'number'},b:{type:'number'}}, required:['a','b']}}]}});
  if (r.method === 'tools/call') {
    const a = r.params.arguments || {};
    return send({jsonrpc:'2.0', id:r.id, result:{content:[{type:'text', text:String((a.a||0)+(a.b||0))}]}});
  }
  if (r.method === 'shutdown') { send({jsonrpc:'2.0', id:r.id, result:{}}); setTimeout(()=>process.exit(0), 10); return; }
});
rl.on('close', ()=>process.exit(0));
`

func TestRemoteUnixPlugin(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "plugin.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveRemote(conn)
		}
	}()
	m := NewManager(t.TempDir(), tool.NewRegistry())
	dirPlugin := filepath.Join(m.Dir(), "remote")
	_ = os.MkdirAll(dirPlugin, 0o700)
	raw, _ := json.Marshal(Manifest{
		ID: "remote", Name: "Remote", Protocol: "unix", Socket: sock, TimeoutMS: 3000,
		Actions: []Action{{Name: "echo", Description: "remote echo"}},
	})
	_ = os.WriteFile(filepath.Join(dirPlugin, "manifest.json"), raw, 0o600)
	if errs := m.Discover(); len(errs) != 0 {
		t.Fatalf("discover: %v", errs)
	}
	if err := m.Load(context.Background(), "remote"); err != nil {
		t.Fatalf("load: %v", err)
	}
	out, err := m.Call(context.Background(), "remote", "echo", map[string]any{"text": "socket"})
	if err != nil || out != "socket" {
		t.Fatalf("call: %q %v", out, err)
	}
	_ = m.Unload(context.Background(), "remote")
	m.Close()
}

func serveRemote(conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		var req struct {
			JSONRPC string `json:"jsonrpc"`
			ID      *int64 `json:"id"`
			Method  string `json:"method"`
			Params  struct {
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || req.ID == nil {
			continue
		}
		var res any
		switch req.Method {
		case "initialize":
			res = map[string]any{"ok": true}
		case "call":
			res = map[string]any{"content": req.Params.Arguments["text"]}
		case "shutdown":
			res = map[string]any{"ok": true}
		default:
			_ = json.NewEncoder(conn).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "unknown"}})
			continue
		}
		_ = json.NewEncoder(conn).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": res})
	}
}

func TestServiceDependencies(t *testing.T) {
	requireNode(t)
	home := t.TempDir()
	writePlugin(t, home, "base", Manifest{Name: "Base", Provides: []string{"cache"}, Actions: []Action{{Name: "echo"}}}, echoScript)
	writePlugin(t, home, "app", Manifest{Name: "App", Requires: []string{"cache"}, Actions: []Action{{Name: "echo"}}}, echoScript)
	m := NewManager(home, tool.NewRegistry())
	_ = m.Discover()

	if err := m.Load(context.Background(), "app"); !errors.Is(err, ErrMissingService) {
		t.Fatalf("缺依赖应失败: %v", err)
	}
	if err := m.Load(context.Background(), "base"); err != nil {
		t.Fatalf("base: %v", err)
	}
	if err := m.Load(context.Background(), "app"); err != nil {
		t.Fatalf("app: %v", err)
	}
	if err := m.Unload(context.Background(), "base"); !errors.Is(err, ErrHasDependents) {
		t.Fatalf("应阻止卸载有依赖的 base: %v", err)
	}
	if err := m.Unload(context.Background(), "app"); err != nil {
		t.Fatalf("app unload: %v", err)
	}
	if err := m.Unload(context.Background(), "base"); err != nil {
		t.Fatalf("base unload: %v", err)
	}
	m.Close()
}

func TestMCPPlugin(t *testing.T) {
	requireNode(t)
	home := t.TempDir()
	m := NewManager(home, tool.NewRegistry())
	dir := filepath.Join(home, "plugins", "mcp-add")
	_ = os.MkdirAll(dir, 0o700)
	raw, _ := json.Marshal(Manifest{
		ID: "mcp-add", Name: "MCP Add", Protocol: "mcp",
		Command: []string{"node", "index.js"}, TimeoutMS: 3000,
	})
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o600)
	_ = os.WriteFile(filepath.Join(dir, "index.js"), []byte(mcpScript), 0o600)

	if errs := m.Discover(); len(errs) != 0 {
		t.Fatalf("discover: %v", errs)
	}
	if err := m.Load(context.Background(), "mcp-add"); err != nil {
		t.Fatalf("load: %v", err)
	}
	list := m.List()
	if len(list) != 1 || len(list[0].Actions) != 1 || list[0].Actions[0].Name != "add" {
		t.Fatalf("actions: %+v", list)
	}
	out, err := m.Call(context.Background(), "mcp-add", "add", map[string]any{"a": 2, "b": 3})
	if err != nil || out != "5" {
		t.Fatalf("call: %q %v", out, err)
	}
	_ = m.Unload(context.Background(), "mcp-add")
	m.Close()
}
