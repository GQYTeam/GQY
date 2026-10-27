package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gqy/internal/config"
)

func newAdminRouter(t *testing.T) *Router {
	t.Helper()
	home := t.TempDir()
	r := New(config.Config{Home: home}, nil)
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestProviderCreateUpdateDelete(t *testing.T) {
	r := newAdminRouter(t)
	if err := r.UpsertProvider(Provider{
		ID: "a", Name: "A", BaseURL: "https://a.example/v1", DefaultModel: "ma", APIKey: "ka",
	}); err != nil {
		t.Fatal(err)
	}
	views := r.ProviderViews()
	if len(views) != 1 || !views[0].KeySet || views[0].DefaultModel != "ma" {
		t.Fatalf("views: %+v", views)
	}
	if r.Status().Default.Provider != "a" {
		t.Fatalf("default: %+v", r.Status().Default)
	}

	// 更新时不传 key，应保留原 key。
	if err := r.UpsertProvider(Provider{
		ID: "a", Name: "A2", BaseURL: "https://a.example/v1", DefaultModel: "ma2", MaxConcurrency: 3,
	}); err != nil {
		t.Fatal(err)
	}
	views = r.ProviderViews()
	if !views[0].KeySet || views[0].Name != "A2" || views[0].DefaultModel != "ma2" {
		t.Fatalf("updated views: %+v", views)
	}
	raw, err := os.ReadFile(filepath.Join(r.home, "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Providers []Provider `json:"providers"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Providers) != 1 || data.Providers[0].APIKey != "ka" {
		t.Fatalf("persisted: %+v", data.Providers)
	}

	if err := r.DeleteProvider("a"); err != nil {
		t.Fatal(err)
	}
	if len(r.ProviderViews()) != 0 {
		t.Fatalf("delete failed: %+v", r.ProviderViews())
	}
	if err := r.DeleteProvider("env"); err == nil {
		t.Fatal("env provider 应不可删")
	}
}

func TestProviderValidation(t *testing.T) {
	r := newAdminRouter(t)
	for _, p := range []Provider{
		{ID: "Bad ID", BaseURL: "https://a/v1", DefaultModel: "m"},
		{ID: "ok", BaseURL: "file:///etc/passwd", DefaultModel: "m"},
		{ID: "ok", BaseURL: "https://a/v1"},
	} {
		if err := r.UpsertProvider(p); err == nil {
			t.Fatalf("应拒绝：%+v", p)
		}
	}
}

func TestGroupsAndDefault(t *testing.T) {
	r := newAdminRouter(t)
	for _, p := range []Provider{
		{ID: "a", BaseURL: "https://a/v1", DefaultModel: "ma", APIKey: "k"},
		{ID: "b", BaseURL: "https://b/v1", DefaultModel: "mb", APIKey: "k"},
	} {
		if err := r.UpsertProvider(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.UpsertGroup(Group{ID: "daily", Strategy: "rotate", Members: []string{"a/ma", "b/mb"}}); err != nil {
		t.Fatal(err)
	}
	st := r.Status()
	if len(st.Groups) != 1 || st.Groups[0].Strategy != "rotate" {
		t.Fatalf("groups: %+v", st.Groups)
	}
	if err := r.SetDefault("b", "mb"); err != nil {
		t.Fatal(err)
	}
	if st := r.Status(); st.Default.Provider != "b" || st.Default.Model != "mb" {
		t.Fatalf("default: %+v", st.Default)
	}
	if err := r.DeleteGroup("daily"); err != nil {
		t.Fatal(err)
	}
	if len(r.Status().Groups) != 0 {
		t.Fatalf("group delete failed")
	}
	if err := r.UpsertGroup(Group{ID: "bad", Strategy: "nope", Members: []string{"a/ma"}}); err == nil {
		t.Fatal("非法 strategy 应拒绝")
	}
}
