package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"gqy/internal/config"
	"gqy/internal/provider"
)

func fakeUpstream(t *testing.T, status int, content string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}},
			"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 2, "total_tokens": 3},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newRouter(t *testing.T, providers []map[string]any, routing map[string]any) *Router {
	t.Helper()
	home := t.TempDir()
	raw, _ := json.Marshal(map[string]any{"providers": providers})
	if err := os.WriteFile(filepath.Join(home, "providers.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if routing != nil {
		raw, _ = json.Marshal(routing)
		if err := os.WriteFile(filepath.Join(home, "routing.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := New(config.Config{Home: home}, http.DefaultClient)
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestResolveProviderAndGroup(t *testing.T) {
	a, _ := fakeUpstream(t, 200, "a")
	b, _ := fakeUpstream(t, 200, "b")
	r := newRouter(t, []map[string]any{
		{"id": "a", "base_url": a.URL, "api_key": "k", "default_model": "ma", "models": []string{"ma"}},
		{"id": "b", "base_url": b.URL, "api_key": "k", "default_model": "mb", "models": []string{"mb"}},
	}, map[string]any{
		"groups": []map[string]any{{"id": "daily", "strategy": "order", "members": []string{"a/ma", "b/mb"}}},
	})

	if got := r.Candidates("a/ma", ""); len(got) != 1 || got[0].Provider.ID != "a" || got[0].Model != "ma" {
		t.Fatalf("provider/model: %+v", got)
	}
	if got := r.Candidates("mb", ""); len(got) != 1 || got[0].Provider.ID != "b" {
		t.Fatalf("bare model: %+v", got)
	}
	if got := r.Candidates("group/daily", ""); len(got) != 2 || got[0].Provider.ID != "a" || got[1].Provider.ID != "b" {
		t.Fatalf("group: %+v", got)
	}
	if got := r.Candidates("", ""); len(got) != 1 || got[0].Provider.ID != "a" {
		t.Fatalf("default: %+v", got)
	}
}

func TestFailoverAndCooldown(t *testing.T) {
	bad, badCalls := fakeUpstream(t, 500, "")
	good, goodCalls := fakeUpstream(t, 200, "ok")
	r := newRouter(t, []map[string]any{
		{"id": "bad", "base_url": bad.URL, "api_key": "k", "default_model": "m", "models": []string{"m"}},
		{"id": "good", "base_url": good.URL, "api_key": "k", "default_model": "m", "models": []string{"m"}},
	}, map[string]any{
		"default":          map[string]any{"provider": "bad", "model": "m"},
		"max_attempts":     2,
		"cooldown_seconds": 30,
		"groups":           []map[string]any{{"id": "both", "strategy": "order", "members": []string{"bad/m", "good/m"}}},
	})

	msg, usage, err := r.Complete(context.Background(), provider.Request{Model: "group/both", Messages: []provider.Message{{Role: "user", Content: "hi"}}})
	if err != nil || msg.Content != "ok" || usage == nil || usage.TotalTokens != 3 {
		t.Fatalf("complete: %+v %+v %v", msg, usage, err)
	}
	if badCalls.Load() != 1 || goodCalls.Load() != 1 {
		t.Fatalf("calls bad=%d good=%d", badCalls.Load(), goodCalls.Load())
	}
	st := r.Status()
	var badStatus *ProviderStatus
	for i := range st.Providers {
		if st.Providers[i].ID == "bad" {
			badStatus = &st.Providers[i]
		}
	}
	if badStatus == nil || badStatus.CooldownAt == nil {
		t.Fatalf("bad provider 应进入冷却: %+v", st.Providers)
	}
}

func TestNoRetryOn4xx(t *testing.T) {
	bad, badCalls := fakeUpstream(t, 401, "")
	good, goodCalls := fakeUpstream(t, 200, "ok")
	r := newRouter(t, []map[string]any{
		{"id": "bad", "base_url": bad.URL, "api_key": "k", "default_model": "m"},
		{"id": "good", "base_url": good.URL, "api_key": "k", "default_model": "m"},
	}, map[string]any{
		"groups": []map[string]any{{"id": "both", "members": []string{"bad/m", "good/m"}}},
	})
	_, _, err := r.Complete(context.Background(), provider.Request{Model: "group/both", Messages: []provider.Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("期望 401 失败")
	}
	if badCalls.Load() != 1 || goodCalls.Load() != 0 {
		t.Fatalf("401 不应重试: bad=%d good=%d", badCalls.Load(), goodCalls.Load())
	}
}

func TestAccountQuota(t *testing.T) {
	up, calls := fakeUpstream(t, 200, "ok")
	r := newRouter(t, []map[string]any{
		{"id": "q", "base_url": up.URL, "default_model": "m", "accounts": []map[string]any{
			{"id": "only", "api_key": "k", "daily_token_limit": 1},
		}},
	}, nil)
	_, _, err := r.Complete(context.Background(), provider.Request{Model: "q/m", Messages: []provider.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	first := calls.Load()
	_, _, err = r.Complete(context.Background(), provider.Request{Model: "q/m", Messages: []provider.Message{{Role: "user", Content: "hi"}}})
	if err == nil || calls.Load() != first {
		t.Fatalf("配额应阻止第二次请求: err=%v calls=%d", err, calls.Load())
	}
	st := r.Status()
	if len(st.Providers) != 1 || len(st.Providers[0].Accounts) != 1 || st.Providers[0].Accounts[0].UsedTokens == 0 {
		t.Fatalf("accounts status: %+v", st.Providers)
	}
}

func TestAccountAdmin(t *testing.T) {
	r := newAdminRouter(t)
	if err := r.UpsertProvider(Provider{ID: "p", BaseURL: "https://p/v1", DefaultModel: "m", APIKey: "top"}); err != nil {
		t.Fatal(err)
	}
	if err := r.UpsertAccount("p", Account{ID: "a", APIKey: "ka", DailyTokenLimit: 10}); err != nil {
		t.Fatal(err)
	}
	if err := r.UpsertAccount("p", Account{ID: "a", Name: "A", DailyTokenLimit: 20}); err != nil {
		t.Fatal(err)
	}
	r.mu.RLock()
	got := r.byID["p"].Accounts
	r.mu.RUnlock()
	if len(got) != 1 || got[0].APIKey != "ka" || got[0].DailyTokenLimit != 20 {
		t.Fatalf("accounts: %+v", got)
	}
	if err := r.DeleteAccount("p", "a"); err != nil {
		t.Fatal(err)
	}
	r.mu.RLock()
	left := len(r.byID["p"].Accounts)
	r.mu.RUnlock()
	if left != 0 {
		t.Fatalf("account 未删除: %d", left)
	}
}

func TestAffinity(t *testing.T) {
	a, _ := fakeUpstream(t, 200, "a")
	b, _ := fakeUpstream(t, 200, "b")
	r := newRouter(t, []map[string]any{
		{"id": "a", "base_url": a.URL, "api_key": "k", "default_model": "m"},
		{"id": "b", "base_url": b.URL, "api_key": "k", "default_model": "m"},
	}, map[string]any{
		"groups": []map[string]any{{"id": "both", "strategy": "order", "members": []string{"a/m", "b/m"}}},
	})

	ctx := WithSession(context.Background(), "s1")
	msg, _, err := r.Complete(ctx, provider.Request{Model: "group/both", Messages: []provider.Message{{Role: "user", Content: "hi"}}})
	if err != nil || msg.Content != "a" {
		t.Fatalf("first: %+v %v", msg, err)
	}
	got := r.Candidates("group/both", "s1")
	if len(got) != 2 || got[0].Provider.ID != "a" {
		t.Fatalf("affinity: %+v", got)
	}
}
