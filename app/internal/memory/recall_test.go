package memory

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"testing"
	"time"
)

// float32Base64 把 float32 向量编码成小端 base64，模拟旧库 BLOB。
func float32Base64(t *testing.T, vals ...float32) string {
	t.Helper()
	buf := make([]byte, 4*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// int8Base64 把 int8 向量编码成 base64。
func int8Base64(vals ...int8) string {
	buf := make([]byte, len(vals))
	for i, v := range vals {
		buf[i] = byte(v)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// sampleTime 是固定时钟，让衰减与排序可断言。
var sampleTime = time.Date(2026, 11, 2, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))

func TestRecallDeterministic(t *testing.T) {
	home := newHome(t)
	lib := mustLibrary(t, home, DefaultPersona)
	for i, content := range []string{
		"用户喜欢美式咖啡",
		"用户住在杭州，周末常去西湖",
		"用户不吃香菜",
		"用户喜欢手冲咖啡",
		"coffee is great in the morning",
	} {
		if _, err := lib.Remember(RememberInput{Content: content, Importance: (i % 5) + 1}); err != nil {
			t.Fatalf("Remember: %v", err)
		}
	}

	req := RecallRequest{Query: "咖啡 用户", Limit: 10}
	first, err := lib.Recall(req)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	second, err := lib.Recall(req)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("召回结果为空")
	}
	if len(first) != len(second) {
		t.Fatalf("两次召回条数不同: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Record.ID != second[i].Record.ID {
			t.Fatalf("第 %d 条顺序不一致: %d vs %d", i, first[i].Record.ID, second[i].Record.ID)
		}
		if first[i].Score != second[i].Score {
			t.Fatalf("第 %d 条分数不一致: %v vs %v", i, first[i].Score, second[i].Score)
		}
		// 分数保留 4 位小数。
		if scaled := first[i].Score * 10000; math.Abs(scaled-math.Round(scaled)) > 1e-6 {
			t.Fatalf("分数未按 4 位小数取整: %v", first[i].Score)
		}
	}
}

func TestRecallCJKBigram(t *testing.T) {
	home := newHome(t)
	lib := mustLibrary(t, home, DefaultPersona)
	if _, err := lib.Remember(RememberInput{Content: "我喜欢喝手冲咖啡"}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Remember(RememberInput{Content: "I like COFFEE a lot"}); err != nil {
		t.Fatal(err)
	}

	// 中文双字命中。
	scored, err := lib.Recall(RecallRequest{Query: "咖啡"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(scored) != 1 || scored[0].Record.Content != "我喜欢喝手冲咖啡" {
		t.Fatalf("中文双字召回不对: %+v", scored)
	}
	// 单字查询也命中。
	scored, err = lib.Recall(RecallRequest{Query: "咖"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(scored) != 1 {
		t.Fatalf("单字召回 = %d 条", len(scored))
	}
	// 英文大小写不敏感。
	scored, err = lib.Recall(RecallRequest{Query: "CoFfEe"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(scored) != 1 || scored[0].Record.Content != "I like COFFEE a lot" {
		t.Fatalf("英文召回不对: %+v", scored)
	}
	// 查询词一个都不命中时返回空。
	scored, err = lib.Recall(RecallRequest{Query: "量子力学"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(scored) != 0 {
		t.Fatalf("无命中应为空: %+v", scored)
	}

	tokens := Tokenize("咖啡 coffee 咖啡")
	want := map[string]bool{"咖啡": true, "coffee": true}
	if len(tokens) != len(want) {
		t.Fatalf("分词应去重: %v", tokens)
	}
	for _, tok := range tokens {
		if !want[tok] {
			t.Fatalf("意外分词 %q（全部分词 %v）", tok, tokens)
		}
	}
	// 5 个汉字 → 4 个双字滑窗。
	if got := Tokenize("记忆子系统"); len(got) != 4 {
		t.Fatalf("CJK 双字滑窗 = %v, want 4 个", got)
	}
}

func TestRecallImportanceDecay(t *testing.T) {
	home := newHome(t)
	lib := mustLibrary(t, home, DefaultPersona)

	now := sampleTime.Format(time.RFC3339)
	old := sampleTime.Add(-180 * 24 * time.Hour).Format(time.RFC3339)

	base, err := lib.Remember(RememberInput{Content: "记忆衰减测试", Importance: 3, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	high, err := lib.Remember(RememberInput{Content: "记忆衰减测试", Importance: 5, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	aged, err := lib.Remember(RememberInput{Content: "记忆衰减测试", Importance: 3, CreatedAt: old})
	if err != nil {
		t.Fatal(err)
	}

	scored, err := lib.Recall(RecallRequest{Query: "记忆", Limit: 10, Now: sampleTime})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	scores := make(map[int64]float64, len(scored))
	for _, item := range scored {
		scores[item.Record.ID] = item.Score
	}
	if len(scores) != 3 {
		t.Fatalf("召回条数 = %d, want 3", len(scores))
	}

	wantBase := 0.8  // lexical 1 × (0.5+3/10) × strength 1 × recency 1
	wantHigh := 1.0  // lexical 1 × (0.5+5/10)
	wantAged := 0.24 // 0.8 × recency 下限 0.3
	if scores[base.ID] != wantBase {
		t.Fatalf("基准分 = %v, want %v", scores[base.ID], wantBase)
	}
	if scores[high.ID] != wantHigh {
		t.Fatalf("高重要度分 = %v, want %v", scores[high.ID], wantHigh)
	}
	if scores[aged.ID] != wantAged {
		t.Fatalf("衰减分 = %v, want %v", scores[aged.ID], wantAged)
	}
	if !(scores[high.ID] > scores[base.ID] && scores[base.ID] > scores[aged.ID]) {
		t.Fatalf("重要度/衰减单调性不成立: %v", scores)
	}
	if scored[0].Record.ID != high.ID {
		t.Fatalf("排序第一名 = %d, want 高重要度记录 %d", scored[0].Record.ID, high.ID)
	}
}

func TestRecallArchivedAndVisibility(t *testing.T) {
	home := newHome(t)
	lib := mustLibrary(t, home, DefaultPersona)

	keep, err := lib.Remember(RememberInput{Content: "归档召回测试保留项"})
	if err != nil {
		t.Fatal(err)
	}
	drop, err := lib.Remember(RememberInput{Content: "归档召回测试归档项"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.SetFactStatus(drop.ID, StatusArchived); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Remember(RememberInput{Content: "归档召回测试私有项", Visibility: VisibilityNormal}); err != nil {
		t.Fatal(err)
	}

	scored, err := lib.Recall(RecallRequest{Query: "归档召回测试", Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(scored) != 1 || scored[0].Record.ID != keep.ID {
		t.Fatalf("默认召回应排除归档与 normal: %+v", scored)
	}

	scored, err = lib.Recall(RecallRequest{Query: "归档召回测试", Limit: 10, IncludeArchived: true})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(scored) != 2 {
		t.Fatalf("include_archived 召回条数 = %d, want 2", len(scored))
	}

	scored, err = lib.Recall(RecallRequest{Query: "归档召回测试", Limit: 10, IncludeArchived: true, IncludeNormal: true})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(scored) != 3 {
		t.Fatalf("IncludeNormal 召回条数 = %d, want 3", len(scored))
	}
}

func TestRecallKinds(t *testing.T) {
	home := newHome(t)
	lib := mustLibrary(t, home, DefaultPersona)
	if _, err := lib.Remember(RememberInput{Content: "周末去露营要带帐篷"}); err != nil {
		t.Fatal(err)
	}
	raw := `{"id":100,"kind":"episode","content":"用户提到周末去露营","retention":"long_term",` +
		`"created_at":"2026-01-01T00:00:00+08:00","updated_at":"2026-01-01T00:00:00+08:00"}`
	appendRawLine(t, home, episodesFileName, raw)
	evicted := `{"id":200,"kind":"evicted","content":"被挤出的露营原话","role":"user",` +
		`"created_at":"2026-01-01T00:00:00+08:00","updated_at":"2026-01-01T00:00:00+08:00"}`
	appendRawLine(t, home, evictedFileName, evicted)

	scored, err := lib.Recall(RecallRequest{Query: "露营", Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	kinds := make(map[string]int)
	for _, item := range scored {
		kinds[item.Record.Kind]++
	}
	if kinds[KindFact] != 1 || kinds[KindEpisode] != 1 || kinds[KindEvicted] != 1 {
		t.Fatalf("默认三类全参与: %v", kinds)
	}

	scored, err = lib.Recall(RecallRequest{Query: "露营", Kinds: []string{KindEpisode}})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(scored) != 1 || scored[0].Record.Kind != KindEpisode {
		t.Fatalf("kinds 过滤失败: %+v", scored)
	}
}

func TestRecallLimitAndEmptyQuery(t *testing.T) {
	home := newHome(t)
	lib := mustLibrary(t, home, DefaultPersona)
	for i := 0; i < 3; i++ {
		if _, err := lib.Remember(RememberInput{Content: "限量召回测试"}); err != nil {
			t.Fatal(err)
		}
	}
	scored, err := lib.Recall(RecallRequest{Query: "限量", Limit: 2})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(scored) != 2 {
		t.Fatalf("limit 未生效: %d", len(scored))
	}
	scored, err = lib.Recall(RecallRequest{Query: "   "})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(scored) != 0 {
		t.Fatalf("空查询应返回空: %+v", scored)
	}
}

// TestRecallEmbedding 校验「按 BLOB 长度判断编码」与混合打分公式。
func TestRecallEmbedding(t *testing.T) {
	home := newHome(t)
	now := sampleTime.Format(time.RFC3339)
	aligned := float32Base64(t, 1, 0)
	orthogonal := float32Base64(t, 0, 1)
	// model 名故意写 int8：解码必须按长度（8 字节 = 4×2 维）判成 float32。
	appendRawLine(t, home, factsFileName,
		`{"id":1,"kind":"fact","content":"向量对齐测试","memory_type":"fact","embedding":`+
			`{"model":"local:bge-small-zh-v1.5-int8","dim":2,"encoding":"int8","data":"`+aligned+`"},`+
			`"created_at":"`+now+`","updated_at":"`+now+`"}`)
	appendRawLine(t, home, factsFileName,
		`{"id":2,"kind":"fact","content":"向量对齐测试","memory_type":"fact","embedding":`+
			`{"model":"local:bge-small-zh-v1.5-int8","dim":2,"encoding":"int8","data":"`+orthogonal+`"},`+
			`"created_at":"`+now+`","updated_at":"`+now+`"}`)

	lib := mustLibrary(t, home, DefaultPersona)
	query := &Embedding{Dim: 2, Data: aligned, Encoding: "float32"}

	scored, err := lib.Recall(RecallRequest{Query: "向量", Limit: 10, QueryEmbedding: query, Now: sampleTime})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(scored) != 2 {
		t.Fatalf("召回条数 = %d, want 2", len(scored))
	}
	if scored[0].Record.ID != 1 {
		t.Fatalf("对齐向量应排第一: %+v", scored)
	}
	// aligned: (0.65×1 + 0.35×1) × 0.8 = 0.8；orthogonal: (0.65×1 + 0.35×0) × 0.8 = 0.52。
	if scored[0].Score != 0.8 || scored[1].Score != 0.52 {
		t.Fatalf("混合打分 = %v / %v, want 0.8 / 0.52", scored[0].Score, scored[1].Score)
	}

	// 查询不带向量时纯词法打分，不自己算向量。
	scored, err = lib.Recall(RecallRequest{Query: "向量", Limit: 10, Now: sampleTime})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(scored) != 2 || scored[0].Score != 0.8 {
		t.Fatalf("纯词法打分 = %+v", scored)
	}
}

func TestEmbeddingDecodeByLength(t *testing.T) {
	cases := []struct {
		name string
		emb  Embedding
		want int
		ok   bool
	}{
		{name: "dim 与长度相等按 int8", emb: Embedding{Dim: 4, Data: int8Base64(1, 0, -1, 127)}, want: 4, ok: true},
		{name: "四倍长度按 float32", emb: Embedding{Dim: 2, Data: float32Base64(t, 0.5, -0.5)}, want: 2, ok: true},
		{name: "model 名骗人仍按长度", emb: Embedding{Model: "local:bge-small-zh-v1.5-int8", Dim: 2, Data: float32Base64(t, 1, 0)}, want: 2, ok: true},
		{name: "长度与 dim 不符", emb: Embedding{Dim: 2, Data: int8Base64(1, 2, 3, 4)}, ok: false},
		{name: "无 dim 且 4 的倍数按 float32", emb: Embedding{Data: float32Base64(t, 1, 2)}, want: 2, ok: true},
		{name: "无 dim 非 4 的倍数按 int8", emb: Embedding{Data: int8Base64(1, 2, 3)}, want: 3, ok: true},
		{name: "空数据", emb: Embedding{}, ok: false},
	}
	for _, tc := range cases {
		got, ok := tc.emb.Vector()
		if ok != tc.ok {
			t.Fatalf("%s: ok = %v, want %v", tc.name, ok, tc.ok)
		}
		if ok && len(got) != tc.want {
			t.Fatalf("%s: 维度 = %d, want %d", tc.name, len(got), tc.want)
		}
	}

	// int8 按补码还原，float32 按小端还原。
	vec, _ := (&Embedding{Dim: 3, Data: int8Base64(1, 0, -1)}).Vector()
	if vec[0] != 1 || vec[1] != 0 || vec[2] != -1 {
		t.Fatalf("int8 解码 = %v", vec)
	}
	vec, _ = (&Embedding{Dim: 2, Data: float32Base64(t, 0.5, -0.25)}).Vector()
	if vec[0] != 0.5 || vec[1] != -0.25 {
		t.Fatalf("float32 解码 = %v", vec)
	}
}
