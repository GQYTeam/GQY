package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseManifestValidation(t *testing.T) {
	valid := `{"id":"echo","name":"Echo","version":"1.0.0","protocol":"gqy-jsonrpc-v1","command":["node","i.js"],"actions":[{"name":"echo","description":"回显","schema":{"type":"object"}}]}`
	if _, err := ParseManifest([]byte(valid)); err != nil {
		t.Fatalf("合法 manifest 应通过: %v", err)
	}
	cases := map[string]string{
		"非法 id":                  `{"id":"Echo!","name":"E","version":"1.0.0","protocol":"gqy-jsonrpc-v1","command":["x"]}`,
		"缺 version":              `{"id":"echo","name":"E","protocol":"gqy-jsonrpc-v1","command":["x"]}`,
		"未知协议":                   `{"id":"echo","name":"E","version":"1.0.0","protocol":"grpc","command":["x"]}`,
		"缺 command":              `{"id":"echo","name":"E","version":"1.0.0","protocol":"gqy-jsonrpc-v1"}`,
		"action schema 非 object": `{"id":"echo","name":"E","version":"1.0.0","protocol":"gqy-jsonrpc-v1","command":["x"],"actions":[{"name":"a","description":"d","schema":{"type":"string"}}]}`,
	}
	for name, data := range cases {
		if _, err := ParseManifest([]byte(data)); err == nil {
			t.Fatalf("%s 应被拒绝", name)
		}
	}
}

func TestTransportMultiplex(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	tr := newTransport(client)
	go tr.start()

	// 服务端：按 id 回显。
	go func() {
		sc := bufio.NewScanner(server)
		for sc.Scan() {
			var msg struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
				Params struct {
					N int `json:"n"`
				} `json:"params"`
			}
			if json.Unmarshal(sc.Bytes(), &msg) != nil {
				continue
			}
			reply := map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"n": msg.Params.N * 2}}
			b, _ := json.Marshal(reply)
			_, _ = server.Write(append(b, '\n'))
		}
	}()

	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			result, err := tr.call(context.Background(), "double", map[string]any{"n": n})
			if err != nil {
				errs <- err
				return
			}
			var out struct {
				N int `json:"n"`
			}
			if err := json.Unmarshal(result, &out); err != nil || out.N != n*2 {
				errs <- fmt.Errorf("n=%d 结果 %v", n, out)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

type fakeProc struct {
	io rw
}

type rw interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Close() error
}

func (f *fakeProc) Read(p []byte) (int, error)  { return f.io.Read(p) }
func (f *fakeProc) Write(p []byte) (int, error) { return f.io.Write(p) }
func (f *fakeProc) Close() error                { return f.io.Close() }
func (f *fakeProc) PID() int                    { return 0 }

func pipePair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return a, b
}

func TestNativeProtocolOverPipe(t *testing.T) {
	client, server := pipePair(t)
	tr := newTransport(client)
	go tr.start()

	go func() {
		sc := bufio.NewScanner(server)
		for sc.Scan() {
			var msg rpcMessage
			if json.Unmarshal(sc.Bytes(), &msg) != nil {
				continue
			}
			reply := map[string]any{"jsonrpc": "2.0", "id": msg.ID}
			switch msg.Method {
			case "initialize":
				reply["result"] = map[string]any{"ok": true}
			case "call":
				reply["result"] = map[string]any{"content": "hello"}
			case "shutdown":
				reply["result"] = map[string]any{"ok": true}
				b, _ := json.Marshal(reply)
				_, _ = server.Write(append(b, '\n'))
				return
			default:
				reply["error"] = map[string]any{"code": -32601, "message": "unknown action"}
			}
			b, _ := json.Marshal(reply)
			_, _ = server.Write(append(b, '\n'))
		}
	}()

	inst := &nativeInstance{proc: &fakeProc{io: client}, tr: tr, manifest: &Manifest{ID: "echo", Actions: []Action{{Name: "echo", Description: "d", Schema: map[string]any{"type": "object"}}}}}
	content, err := inst.Call(context.Background(), "echo", map[string]any{"text": "hi"})
	if err != nil || content != "hello" {
		t.Fatalf("Call = %q, %v", content, err)
	}
	_ = inst.Close(context.Background())
}

func TestMCPProtocolOverPipe(t *testing.T) {
	client, server := pipePair(t)
	tr := newTransport(client)
	go tr.start()

	go func() {
		sc := bufio.NewScanner(server)
		for sc.Scan() {
			var msg rpcMessage
			if json.Unmarshal(sc.Bytes(), &msg) != nil {
				continue
			}
			reply := map[string]any{"jsonrpc": "2.0", "id": msg.ID}
			switch msg.Method {
			case "tools/call":
				reply["result"] = map[string]any{"content": []map[string]any{{"type": "text", "text": "mcp ok"}}, "isError": false}
			case "shutdown":
				reply["result"] = nil
				b, _ := json.Marshal(reply)
				_, _ = server.Write(append(b, '\n'))
				return
			default:
				reply["result"] = map[string]any{}
			}
			b, _ := json.Marshal(reply)
			_, _ = server.Write(append(b, '\n'))
		}
	}()

	inst := &mcpInstance{proc: &fakeProc{io: client}, tr: tr, actions: []Action{{Name: "echo", Description: "d", Schema: map[string]any{"type": "object"}}}}
	content, err := inst.Call(context.Background(), "echo", map[string]any{"text": "hi"})
	if err != nil || content != "mcp ok" {
		t.Fatalf("Call = %q, %v", content, err)
	}
}

func TestRemoteUnixDialNoProcess(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "p.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			var msg rpcMessage
			if json.Unmarshal(sc.Bytes(), &msg) != nil {
				continue
			}
			reply := map[string]any{"jsonrpc": "2.0", "id": msg.ID}
			switch msg.Method {
			case "initialize":
				reply["result"] = map[string]any{"ok": true}
			case "call":
				reply["result"] = map[string]any{"content": "remote"}
			case "shutdown":
				reply["result"] = map[string]any{"ok": true}
				b, _ := json.Marshal(reply)
				_, _ = conn.Write(append(b, '\n'))
				return
			default:
				reply["error"] = map[string]any{"code": -32601, "message": "no"}
			}
			b, _ := json.Marshal(reply)
			_, _ = conn.Write(append(b, '\n'))
		}
	}()

	adapter := &RemoteAdapter{}
	inst, err := adapter.Load(context.Background(), &Manifest{Protocol: "unix", Address: sock, TimeoutMS: 2000, Actions: []Action{{Name: "echo", Description: "d", Schema: map[string]any{"type": "object"}}}}, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if inst.PID() != 0 {
		t.Fatal("远程插件不应拉起进程")
	}
	content, err := inst.Call(context.Background(), "echo", nil)
	if err != nil || content != "remote" {
		t.Fatalf("Call = %q, %v", content, err)
	}
	_ = inst.Close(context.Background())
}

func TestToolName(t *testing.T) {
	got := ToolName("my-plugin", "do_thing")
	if got != "my_plugin__do_thing" {
		t.Fatalf("ToolName = %q", got)
	}
	if len(ToolName("a", strings.Repeat("b", 100))) > 64 {
		t.Fatal("工具名应截断到 64")
	}
}

type fakeAdapter struct {
	protocol string
	loadFn   func(ctx context.Context, m *Manifest, dir string) (adapterInstance, error)
}

func (f *fakeAdapter) Protocol() string            { return f.protocol }
func (f *fakeAdapter) Match(community string) bool { return false }
func (f *fakeAdapter) Load(ctx context.Context, m *Manifest, dir string) (adapterInstance, error) {
	return f.loadFn(ctx, m, dir)
}

type fakeInst struct {
	actions []Action
	calls   int
	closed  bool
}

func (f *fakeInst) Call(ctx context.Context, action string, args map[string]any) (string, error) {
	f.calls++
	return "ok", nil
}
func (f *fakeInst) Actions() []Action               { return f.actions }
func (f *fakeInst) Close(ctx context.Context) error { f.closed = true; return nil }
func (f *fakeInst) PID() int                        { return 42 }

func TestManagerLifecycleAndDeps(t *testing.T) {
	home := t.TempDir()
	pluginsDir := filepath.Join(home, "plugins")
	writeManifest(t, pluginsDir, "a", `{"id":"a","name":"A","version":"1.0.0","protocol":"gqy-jsonrpc-v1","command":["x"],"actions":[{"name":"a1","description":"d","schema":{"type":"object"}}],"provides":["svc-a"]}`)
	writeManifest(t, pluginsDir, "b", `{"id":"b","name":"B","version":"1.0.0","protocol":"gqy-jsonrpc-v1","command":["x"],"actions":[],"requires":["svc-a"]}`)

	m := NewManager(home)
	m.adapters[ProtocolNative] = &fakeAdapter{protocol: ProtocolNative, loadFn: func(ctx context.Context, mf *Manifest, dir string) (adapterInstance, error) {
		return &fakeInst{actions: mf.Actions}, nil
	}}
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}

	// 缺依赖拒载
	if err := m.Load(context.Background(), "b"); err == nil || !strings.Contains(err.Error(), "缺依赖") {
		t.Fatalf("b 缺依赖应拒载: %v", err)
	}
	// 加载 a 后再载 b
	if err := m.Load(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	// 幂等重复加载
	if err := m.Load(context.Background(), "a"); err != nil {
		t.Fatalf("重复 Load 应幂等: %v", err)
	}
	// 有依赖拒卸载
	if err := m.Unload(context.Background(), "a"); err == nil || !strings.Contains(err.Error(), "依赖") {
		t.Fatalf("a 有依赖应拒卸载: %v", err)
	}
	if err := m.Unload(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	if err := m.Unload(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	// 重复卸载幂等（卸载只回 discovered，不摘除；删除走 Delete）
	if err := m.Unload(context.Background(), "a"); err != nil {
		t.Fatalf("重复卸载应幂等: %v", err)
	}

	// 卸载后仍留在列表里，但都不是 ready
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	for _, st := range m.Status() {
		if st.State == string(StateReady) {
			t.Fatalf("卸载后不应有 ready 插件: %+v", st)
		}
	}
}

func writeManifest(t *testing.T, pluginsDir, id, content string) {
	t.Helper()
	dir := filepath.Join(pluginsDir, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

var _ = time.Now

func TestManagerTCPAdapterRegistered(t *testing.T) {
	m := NewManager(t.TempDir())
	if _, ok := m.adapters[ProtocolTCP]; !ok {
		t.Fatal("tcp 适配器未注册")
	}
	if _, ok := m.adapters[ProtocolUnix]; !ok {
		t.Fatal("unix 适配器未注册")
	}
}

func TestRemoteTCPDialNoProcess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			var msg rpcMessage
			if json.Unmarshal(sc.Bytes(), &msg) != nil {
				continue
			}
			reply := map[string]any{"jsonrpc": "2.0", "id": msg.ID}
			switch msg.Method {
			case "initialize":
				reply["result"] = map[string]any{"ok": true}
			case "call":
				reply["result"] = map[string]any{"content": "tcp-remote"}
			case "shutdown":
				reply["result"] = map[string]any{"ok": true}
				b, _ := json.Marshal(reply)
				_, _ = conn.Write(append(b, '\n'))
				return
			default:
				reply["error"] = map[string]any{"code": -32601, "message": "no"}
			}
			b, _ := json.Marshal(reply)
			_, _ = conn.Write(append(b, '\n'))
		}
	}()

	adapter := &RemoteAdapter{}
	inst, err := adapter.Load(context.Background(), &Manifest{Protocol: "tcp", Address: ln.Addr().String(), TimeoutMS: 2000, Actions: []Action{{Name: "echo", Description: "d", Schema: map[string]any{"type": "object"}}}}, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if inst.PID() != 0 {
		t.Fatal("tcp 远程插件不应拉起进程")
	}
	content, err := inst.Call(context.Background(), "echo", nil)
	if err != nil || content != "tcp-remote" {
		t.Fatalf("Call = %q, %v", content, err)
	}
	_ = inst.Close(context.Background())
}

func TestLoadFailureRollsBackAndRetry(t *testing.T) {
	home := t.TempDir()
	pluginsDir := filepath.Join(home, "plugins")
	writeManifest(t, pluginsDir, "p", `{"id":"p","name":"P","version":"1.0.0","protocol":"gqy-jsonrpc-v1","command":["x"],"actions":[{"name":"a","description":"d","schema":{"type":"object"}}]}`)
	m := NewManager(home)
	adapter := &fakeAdapter{protocol: ProtocolNative, loadFn: func(ctx context.Context, mf *Manifest, dir string) (adapterInstance, error) {
		return nil, fmt.Errorf("load failed")
	}}
	m.adapters[ProtocolNative] = adapter
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(context.Background(), "p"); err == nil {
		t.Fatal("加载失败应返回错误")
	}
	st := m.Status()
	if len(st) != 1 || st[0].State != "discovered" || st[0].PID != 0 {
		t.Fatalf("失败应回滚到 discovered 且无残留 pid: %+v", st)
	}

	// 同 id 可重试加载
	adapter.loadFn = func(ctx context.Context, mf *Manifest, dir string) (adapterInstance, error) {
		return &fakeInst{actions: mf.Actions}, nil
	}
	if err := m.Load(context.Background(), "p"); err != nil {
		t.Fatalf("重试加载失败: %v", err)
	}
	st = m.Status()
	if len(st) != 1 || st[0].State != "ready" || st[0].PID != 42 {
		t.Fatalf("重试后应 ready: %+v", st)
	}

	// failed 实例 Unload 应清理
	m.mu.Lock()
	inst := m.plugins["p"]
	m.mu.Unlock()
	inst.mu.Lock()
	inst.state = StateFailed
	inst.mu.Unlock()
	if err := m.Unload(context.Background(), "p"); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	_, ok := m.plugins["p"]
	m.mu.Unlock()
	if ok {
		t.Log("failed 实例 Unload 后应回到 discovered（不再摘除）")
	}
}

// TestPluginProcessSurvivesLoadContextCancel 回归：插件进程不能绑在 load 请求的 context 上。
// 真实 bug：exec.CommandContext(请求 ctx) 会在 HTTP 响应结束、请求 ctx 取消时杀掉插件进程，
// 表现为 load 成功、后续 call 报 broken pipe。
func TestPluginProcessSurvivesLoadContextCancel(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node 不可用，跳过真实进程回归测试")
	}
	home := t.TempDir()
	dir := filepath.Join(home, "plugins", "echo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := `#!/usr/bin/env node
const readline = require('readline');
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
rl.on('line', (line) => {
  let m; try { m = JSON.parse(line); } catch { return; }
  const r = { jsonrpc: '2.0', id: m.id };
  if (m.method === 'initialize') r.result = { ok: true };
  else if (m.method === 'call') r.result = { content: 'hi' };
  else if (m.method === 'shutdown') { r.result = { ok: true }; process.stdout.write(JSON.stringify(r) + '\n'); process.exit(0); }
  else r.error = { code: -32601, message: 'unknown' };
  process.stdout.write(JSON.stringify(r) + '\n');
});
rl.on('close', () => process.exit(0));
`
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, filepath.Join(home, "plugins"), "echo",
		`{"id":"echo","name":"Echo","version":"1.0.0","protocol":"gqy-jsonrpc-v1","command":["node","index.js"],"cwd":".","timeout_ms":10000,"actions":[{"name":"echo","description":"d","schema":{"type":"object"}}]}`)

	m := NewManager(home)
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := m.Load(ctx, "echo"); err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	cancel() // 模拟 HTTP 请求结束、请求 ctx 取消
	time.Sleep(200 * time.Millisecond)

	content, err := m.Call(context.Background(), "echo", "echo", map[string]any{"text": "x"})
	if err != nil {
		t.Fatalf("load 请求 ctx 取消后调用应仍成功: %v", err)
	}
	if content != "hi" {
		t.Fatalf("content = %q，期望 hi", content)
	}
	if err := m.Unload(context.Background(), "echo"); err != nil {
		t.Fatal(err)
	}
}

// TestManagerCloseAll 验证关闭时把所有已加载插件卸载并回收（019，P8）。
func TestManagerCloseAll(t *testing.T) {
	home := t.TempDir()
	pluginsDir := filepath.Join(home, "plugins")
	writeManifest(t, pluginsDir, "a", `{"id":"a","name":"A","version":"1.0.0","protocol":"gqy-jsonrpc-v1","command":["x"],"actions":[]}`)
	writeManifest(t, pluginsDir, "b", `{"id":"b","name":"B","version":"1.0.0","protocol":"gqy-jsonrpc-v1","command":["x"],"actions":[]}`)

	m := NewManager(home)
	m.adapters[ProtocolNative] = &fakeAdapter{protocol: ProtocolNative, loadFn: func(ctx context.Context, mf *Manifest, dir string) (adapterInstance, error) {
		return &fakeInst{actions: mf.Actions}, nil
	}}
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	if got := len(m.Status()); got != 2 {
		t.Fatalf("加载后应有 2 个插件，实际 %d", got)
	}
	if err := m.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for _, st := range m.Status() {
		if st.State == string(StateReady) {
			t.Fatalf("关闭后不应有 ready 插件，实际 %+v", st)
		}
	}
	// 幂等：再关一次不报错。
	if err := m.Close(context.Background()); err != nil {
		t.Fatalf("重复 Close 应幂等: %v", err)
	}
}
