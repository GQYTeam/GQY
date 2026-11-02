package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// memoryFact 是测试里解析 FactView 的最小结构。
type memoryFact struct {
	ID          int64    `json:"id"`
	Kind        string   `json:"kind"`
	Content     string   `json:"content"`
	Truncated   bool     `json:"truncated"`
	MemoryType  string   `json:"memory_type"`
	Status      string   `json:"status"`
	Importance  int      `json:"importance"`
	Owner       string   `json:"owner"`
	Tags        []string `json:"tags"`
	LegacyID    string   `json:"legacy_id"`
	RecallCount int      `json:"recall_count"`
}

func TestHTTPMemoryEndpoints(t *testing.T) {
	e := newTestServer(t, "", nil)

	// 空库：200 + 空清单，不 500。
	rec := e.request(http.MethodGet, "/api/memory", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("空库 GET /api/memory = %d: %s", rec.Code, rec.Body.String())
	}
	var list struct {
		SchemaVersion int `json:"schema_version"`
		Personas      []struct {
			ID     string         `json:"id"`
			Counts map[string]int `json:"counts"`
		} `json:"personas"`
	}
	decodeJSON(t, rec, &list)
	if list.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d", list.SchemaVersion)
	}
	if list.Personas == nil || len(list.Personas) != 0 {
		t.Fatalf("空库 personas = %+v, want 空清单", list.Personas)
	}

	// 空库的库级端点也要 200。
	for _, path := range []string{
		"/api/memory/default/stats",
		"/api/memory/default/facts",
		"/api/memory/default/episodes",
		"/api/memory/default/evicted",
		"/api/memory/default/revisions",
	} {
		rec := e.request(http.MethodGet, path, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("空库 GET %s = %d: %s", path, rec.Code, rec.Body.String())
		}
	}
	rec = e.request(http.MethodPost, "/api/memory/default/recall", strings.NewReader(`{"query":"咖啡","limit":5}`), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("空库 recall = %d: %s", rec.Code, rec.Body.String())
	}

	// 写入事实。
	body := `{"content":"用户喜欢美式咖啡，不加糖","memory_type":"preference","truth_status":"accepted",` +
		`"importance":4,"confidence":0.9,"tags":["偏好","饮品"],"owner_principal":"bob@example.com"}`
	rec = e.request(http.MethodPost, "/api/memory/default/facts", strings.NewReader(body), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST facts = %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		OK   bool       `json:"ok"`
		Fact memoryFact `json:"fact"`
	}
	decodeJSON(t, rec, &created)
	if !created.OK || created.Fact.ID <= 0 || created.Fact.Status != "active" {
		t.Fatalf("写入返回不对: %+v", created)
	}
	if created.Fact.Owner != "b***m" {
		t.Fatalf("owner 未掩码: %q", created.Fact.Owner)
	}
	if strings.Contains(rec.Body.String(), "bob@example.com") || strings.Contains(rec.Body.String(), "owner_principal") {
		t.Fatalf("响应泄漏了 owner_principal 原值: %s", rec.Body.String())
	}

	// 列表。
	rec = e.request(http.MethodGet, "/api/memory/default/facts", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET facts = %d: %s", rec.Code, rec.Body.String())
	}
	var facts struct {
		Items  []memoryFact `json:"items"`
		Total  int          `json:"total"`
		Offset int          `json:"offset"`
		Limit  int          `json:"limit"`
	}
	decodeJSON(t, rec, &facts)
	if facts.Total != 1 || len(facts.Items) != 1 || facts.Limit != 20 || facts.Offset != 0 {
		t.Fatalf("列表不对: %+v", facts)
	}
	if facts.Items[0].ID != created.Fact.ID || facts.Items[0].MemoryType != "preference" {
		t.Fatalf("列表项不对: %+v", facts.Items[0])
	}

	// 词法过滤 q。
	rec = e.request(http.MethodGet, "/api/memory/default/facts?q=咖啡", nil, nil)
	decodeJSON(t, rec, &facts)
	if facts.Total != 1 {
		t.Fatalf("q 过滤后 total = %d", facts.Total)
	}
	rec = e.request(http.MethodGet, "/api/memory/default/facts?q=不存在", nil, nil)
	decodeJSON(t, rec, &facts)
	if facts.Total != 0 {
		t.Fatalf("q 过滤未生效: %d", facts.Total)
	}

	// limit 收敛：上限 200，非正值回落到默认 20。
	rec = e.request(http.MethodGet, "/api/memory/default/facts?limit=1000", nil, nil)
	decodeJSON(t, rec, &facts)
	if facts.Limit != 200 {
		t.Fatalf("limit 上限 = %d, want 200", facts.Limit)
	}
	rec = e.request(http.MethodGet, "/api/memory/default/facts?limit=-5", nil, nil)
	decodeJSON(t, rec, &facts)
	if facts.Limit != 20 {
		t.Fatalf("limit 负数回落 = %d, want 20", facts.Limit)
	}

	// 统计。
	rec = e.request(http.MethodGet, "/api/memory/default/stats", nil, nil)
	var stats struct {
		Persona string         `json:"persona"`
		Counts  map[string]int `json:"counts"`
		ByType  map[string]int `json:"by_type"`
		ByTruth map[string]int `json:"by_truth"`
	}
	decodeJSON(t, rec, &stats)
	if stats.Persona != "default" || stats.Counts["facts"] != 1 || stats.ByType["preference"] != 1 || stats.ByTruth["accepted"] != 1 {
		t.Fatalf("stats 不对: %+v", stats)
	}

	// 召回：同参数两次结果完全一致。
	recallBody := `{"query":"咖啡","limit":5}`
	first := e.request(http.MethodPost, "/api/memory/default/recall", strings.NewReader(recallBody), nil)
	second := e.request(http.MethodPost, "/api/memory/default/recall", strings.NewReader(recallBody), nil)
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("recall = %d / %d", first.Code, second.Code)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("同参数两次召回不一致:\n%s\n%s", first.Body.String(), second.Body.String())
	}
	var recalled struct {
		Query string `json:"query"`
		Items []struct {
			ID         int64   `json:"id"`
			Kind       string  `json:"kind"`
			Score      float64 `json:"score"`
			Content    string  `json:"content"`
			Importance int     `json:"importance"`
		} `json:"items"`
	}
	decodeJSON(t, first, &recalled)
	if recalled.Query != "咖啡" || len(recalled.Items) != 1 || recalled.Items[0].Kind != "fact" {
		t.Fatalf("召回结果不对: %+v", recalled)
	}
	if recalled.Items[0].Score <= 0 {
		t.Fatalf("召回分数应为正: %v", recalled.Items[0].Score)
	}

	// 归档：写 revisions + 新行；默认列表看不到，include_archived 能看到。
	patch := `{"status":"archived"}`
	path := "/api/memory/default/facts/" + strconv.FormatInt(created.Fact.ID, 10)
	rec = e.request(http.MethodPatch, path, strings.NewReader(patch), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH facts = %d: %s", rec.Code, rec.Body.String())
	}
	var patched struct {
		OK   bool       `json:"ok"`
		Fact memoryFact `json:"fact"`
	}
	decodeJSON(t, rec, &patched)
	if !patched.OK || patched.Fact.Status != "archived" || patched.Fact.ID != created.Fact.ID {
		t.Fatalf("归档返回不对: %+v", patched)
	}
	rec = e.request(http.MethodGet, "/api/memory/default/facts", nil, nil)
	decodeJSON(t, rec, &facts)
	if facts.Total != 0 {
		t.Fatalf("默认列表应排除归档: %d", facts.Total)
	}
	rec = e.request(http.MethodGet, "/api/memory/default/facts?include_archived=true", nil, nil)
	decodeJSON(t, rec, &facts)
	if facts.Total != 1 || facts.Items[0].Status != "archived" {
		t.Fatalf("include_archived 列表不对: %+v", facts)
	}

	// 修订链。
	rec = e.request(http.MethodGet, "/api/memory/default/revisions?memory_id="+strconv.FormatInt(created.Fact.ID, 10), nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET revisions = %d: %s", rec.Code, rec.Body.String())
	}
	var revisions struct {
		Items []struct {
			ID         int64  `json:"id"`
			Kind       string `json:"kind"`
			MemoryID   int64  `json:"memory_id"`
			OldContent string `json:"old_content"`
		} `json:"items"`
	}
	decodeJSON(t, rec, &revisions)
	if len(revisions.Items) != 1 || revisions.Items[0].MemoryID != created.Fact.ID || revisions.Items[0].Kind != "revision" {
		t.Fatalf("修订链不对: %+v", revisions)
	}

	// 有库时 GET /api/memory 返回人格清单与 counts。
	rec = e.request(http.MethodGet, "/api/memory", nil, nil)
	decodeJSON(t, rec, &list)
	if len(list.Personas) != 1 || list.Personas[0].ID != "default" || list.Personas[0].Counts["facts"] != 1 {
		t.Fatalf("有库清单不对: %+v", list.Personas)
	}

	// 参数校验。
	if rec := e.request(http.MethodGet, "/api/memory/Bad_Persona/stats", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 persona = %d, want 400", rec.Code)
	}
	if rec := e.request(http.MethodGet, "/api/memory/default/facts?limit=abc", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 limit = %d, want 400", rec.Code)
	}
	if rec := e.request(http.MethodPost, "/api/memory/default/facts", strings.NewReader(`{"content":"   "}`), nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("空 content = %d, want 400", rec.Code)
	}
	if rec := e.request(http.MethodPost, "/api/memory/default/recall", strings.NewReader(`{"query":"x","kinds":["revision"]}`), nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 kinds = %d, want 400", rec.Code)
	}
	if rec := e.request(http.MethodPatch, "/api/memory/default/facts/999999", strings.NewReader(patch), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("归档不存在记录 = %d, want 404", rec.Code)
	}
	if rec := e.request(http.MethodPatch, "/api/memory/default/facts/"+strconv.FormatInt(created.Fact.ID, 10), strings.NewReader(`{}`), nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("缺 status = %d, want 400", rec.Code)
	}
}

func TestHTTPMemoryAuthAndTruncation(t *testing.T) {
	e := newTestServer(t, "secret-token", nil)

	// 走现有鉴权中间件：无令牌 401，带令牌 200。
	if rec := e.request(http.MethodGet, "/api/memory", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("无令牌 = %d, want 401", rec.Code)
	}
	auth := map[string]string{"Authorization": "Bearer secret-token"}
	rec := e.request(http.MethodGet, "/api/memory", nil, auth)
	if rec.Code != http.StatusOK {
		t.Fatalf("带令牌 = %d: %s", rec.Code, rec.Body.String())
	}

	// 直接写一条超长记录（模拟迁移数据），列表视图要截断并标 truncated。
	dir := filepath.Join(e.home, "memory", "default")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("长", 4500)
	line := `{"id":1,"kind":"fact","content":"` + long + `","memory_type":"fact",` +
		`"created_at":"2026-01-01T00:00:00+08:00","updated_at":"2026-01-01T00:00:00+08:00"}`
	if err := os.WriteFile(filepath.Join(dir, "facts.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec = e.request(http.MethodGet, "/api/memory/default/facts", nil, auth)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET facts = %d: %s", rec.Code, rec.Body.String())
	}
	var facts struct {
		Items []memoryFact `json:"items"`
		Total int          `json:"total"`
	}
	decodeJSON(t, rec, &facts)
	if facts.Total != 1 || !facts.Items[0].Truncated {
		t.Fatalf("超长正文未截断: %+v", facts)
	}
	if got := len([]rune(facts.Items[0].Content)); got != 4000 {
		t.Fatalf("截断后长度 = %d, want 4000", got)
	}

	// 召回视图同样截断并保留 4 位小数分数。
	rec = e.request(http.MethodPost, "/api/memory/default/recall", strings.NewReader(`{"query":"长"}`), auth)
	if rec.Code != http.StatusOK {
		t.Fatalf("recall = %d: %s", rec.Code, rec.Body.String())
	}
	var recalled struct {
		Items []struct {
			Content   string  `json:"content"`
			Truncated bool    `json:"truncated"`
			Score     float64 `json:"score"`
		} `json:"items"`
	}
	decodeJSON(t, rec, &recalled)
	if len(recalled.Items) != 1 || !recalled.Items[0].Truncated || len([]rune(recalled.Items[0].Content)) != 4000 {
		t.Fatalf("召回未截断: %+v", recalled)
	}
}
