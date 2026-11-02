package gateway

import (
	"errors"
	"reflect"
	"testing"

	"gqy/internal/config"
)

func testSnapshot() *config.Snapshot {
	return &config.Snapshot{
		Providers: []config.Provider{
			{
				ID:           "p1",
				DisplayName:  "Provider One",
				DefaultModel: "m1",
				Models:       []string{"m1", "m2"},
			},
			{
				ID:           "p2",
				DefaultModel: "x",
				Models:       []string{"x"},
			},
		},
		Routing: config.Routing{
			Default: &config.ModelRef{Provider: "p1", Model: "m1"},
			Groups: []config.Group{
				{ID: "g", Name: "日常", Strategy: config.StrategyOrder, Members: []string{"p2/x", "p1/m1", "p2/x"}},
				{ID: "nested", Name: "嵌套", Strategy: config.StrategyRotate, Members: []string{"group/g", "p1/m2"}},
			},
		},
	}
}

func TestCatalogShape(t *testing.T) {
	entries := Catalog(testSnapshot())
	wantIDs := []string{"p1/m1", "p1/m2", "p2/x", "group/g", "group/nested"}
	if len(entries) != len(wantIDs) {
		t.Fatalf("条目数 = %d, want %d: %+v", len(entries), len(wantIDs), entries)
	}
	for i, id := range wantIDs {
		if entries[i].ID != id {
			t.Fatalf("第 %d 项 = %q, want %q", i, entries[i].ID, id)
		}
		if entries[i].Object != "model" || entries[i].Created != 0 {
			t.Fatalf("固定字段不对: %+v", entries[i])
		}
	}
	if !entries[0].Default {
		t.Fatal("provider 默认模型应标记 default")
	}
	if entries[1].Default {
		t.Fatal("非默认模型不应标记 default")
	}
	if entries[0].OwnedBy != "p1" || entries[0].DisplayName != "Provider One · m1" {
		t.Fatalf("provider 模型元数据不对: %+v", entries[0])
	}
	if entries[3].OwnedBy != "group" || entries[3].DisplayName != "日常" {
		t.Fatalf("路由组元数据不对: %+v", entries[3])
	}
	if !reflect.DeepEqual(entries[3].Members, []string{"p2/x", "p1/m1", "p2/x"}) {
		t.Fatalf("路由组 members 不对: %+v", entries[3].Members)
	}
}

func TestCandidates(t *testing.T) {
	snap := testSnapshot()
	cases := []struct {
		name string
		in   string
		want []Candidate
	}{
		{
			name: "provider/model",
			in:   "p1/m2",
			want: []Candidate{{Provider: "p1", Model: "m2"}},
		},
		{
			name: "裸模型名只命中默认模型",
			in:   "m1",
			want: []Candidate{{Provider: "p1", Model: "m1"}},
		},
		{
			name: "裸模型名第二个 provider",
			in:   "x",
			want: []Candidate{{Provider: "p2", Model: "x"}},
		},
		{
			name: "空模型走默认",
			in:   "",
			want: []Candidate{{Provider: "p1", Model: "m1"}},
		},
		{
			name: "路由组展开并去重",
			in:   "group/g",
			want: []Candidate{
				{Provider: "p2", Model: "x"},
				{Provider: "p1", Model: "m1"},
			},
		},
		{
			name: "嵌套路由组",
			in:   "group/nested",
			want: []Candidate{
				{Provider: "p2", Model: "x"},
				{Provider: "p1", Model: "m1"},
				{Provider: "p1", Model: "m2"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Candidates(snap, tc.in)
			if err != nil {
				t.Fatalf("Candidates(%q): %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Candidates(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestCandidatesNotFound(t *testing.T) {
	snap := testSnapshot()
	for _, in := range []string{"nope", "nope/m", "p1/", "group/nope", "m2"} {
		t.Run(in, func(t *testing.T) {
			_, err := Candidates(snap, in)
			if err == nil {
				t.Fatal("应返回未找到")
			}
			if !errors.Is(err, ErrModelNotFound) {
				t.Fatalf("错误应可判定为 ErrModelNotFound: %v", err)
			}
		})
	}

	empty := &config.Snapshot{}
	if _, err := Candidates(empty, ""); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("没有 default 时应返回 ErrModelNotFound: %v", err)
	}
}
