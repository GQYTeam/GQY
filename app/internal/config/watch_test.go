package config

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestWatchReloadsOnFileChange 验证配置文件变化后自动整份热加载（HR1）。
func TestWatchReloadsOnFileChange(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, home, "providers.json", `{"providers":[{"id":"p1","base_url":"https://p1.test/v1","default_model":"m1","models":["m1"],"accounts":[{"id":"a1","api_key":"k1"}]}]}`)
	writeTestFile(t, home, "routing.json", `{"default":{"provider":"p1","model":"m1"}}`)
	writeTestFile(t, home, "settings.json", `{}`)

	m, err := NewManager(testStartup(home), noEnv)
	if err != nil {
		t.Fatal(err)
	}
	var notified atomic.Int64
	m.Subscribe(func(*Snapshot) { notified.Add(1) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Watch(ctx, 20*time.Millisecond)

	// 加一个 provider，模拟用户改配置后「选择一次模型」就能用。
	// 注意：监听协程的初始快照可能晚于第一次写入（-race / 慢机器调度更慢），
	// 那次写入不算「变化」。所以重试写入，保证至少一次变化发生在初始化之后。
	providers2 := `{"providers":[
		{"id":"p1","base_url":"https://p1.test/v1","default_model":"m1","models":["m1"],"accounts":[{"id":"a1","api_key":"k1"}]},
		{"id":"p2","base_url":"https://p2.test/v1","default_model":"m2","models":["m2"],"accounts":[{"id":"a2","api_key":"k2"}]}]}`
	deadline := time.Now().Add(10 * time.Second)
	for attempts := 0; time.Now().Before(deadline); attempts++ {
		writeTestFile(t, home, "providers.json", providers2)
		for i := 0; i < 10; i++ {
			if m.Current().Version > 1 && len(m.Current().Providers) == 2 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if m.Current().Version > 1 && len(m.Current().Providers) == 2 {
			break
		}
	}
	if len(m.Current().Providers) != 2 {
		t.Fatalf("配置未热加载：version=%d providers=%d", m.Current().Version, len(m.Current().Providers))
	}
	if notified.Load() == 0 {
		t.Fatal("订阅回调应被触发")
	}

	// 写坏配置：保留旧快照，不 panic，等下一次修改再试。
	version := m.Current().Version
	writeTestFile(t, home, "providers.json", `{not json`)
	time.Sleep(150 * time.Millisecond)
	if m.Current().Version != version || len(m.Current().Providers) != 2 {
		t.Fatalf("非法配置不应切换快照（version=%d）", m.Current().Version)
	}
	if _, err := os.Stat(filepath.Join(home, "providers.json")); err != nil {
		t.Fatal(err)
	}
}
