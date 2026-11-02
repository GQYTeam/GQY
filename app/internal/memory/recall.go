package memory

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// asciiWordRe 是 ASCII 词法切分规则（MEM4：按词、大小写不敏感）。
var asciiWordRe = regexp.MustCompile(`[a-z0-9]+`)

// recallWeights 是词法与余弦的混合权重（MEM4）。
const (
	lexicalWeight = 0.65
	cosineWeight  = 0.35
)

// Vector 把 embedding 解成 float64 向量。
//
// 编码**只按 BLOB 长度判断**（memory.md §2.1 的向量编码陷阱）：
// len == dim → int8，len == 4*dim → float32；不按 model 名或 encoding 字段
// 反量化，名字会骗人。dim 缺失时按长度兜底：4 的倍数按 float32（旧库实测
// 都是 512 维 float32，L2 归一 ≈1.0），否则按 int8。
func (e *Embedding) Vector() ([]float64, bool) {
	if e == nil || e.Data == "" {
		return nil, false
	}
	raw, err := base64.StdEncoding.DecodeString(e.Data)
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	dim := e.Dim
	switch {
	case dim > 0 && len(raw) == dim:
		return decodeInt8Vector(raw), true
	case dim > 0 && len(raw) == 4*dim:
		return decodeFloat32Vector(raw), true
	case dim > 0:
		return nil, false
	case len(raw)%4 == 0 && e.Encoding != "int8":
		return decodeFloat32Vector(raw), true
	default:
		return decodeInt8Vector(raw), true
	}
}

// decodeInt8Vector 按字节补码还原 int8 向量。
func decodeInt8Vector(raw []byte) []float64 {
	out := make([]float64, len(raw))
	for i, b := range raw {
		out[i] = float64(int8(b)) // #nosec G115 -- 字节补码还原 int8 是有意行为
	}
	return out
}

// decodeFloat32Vector 按小端序还原 float32 向量。
func decodeFloat32Vector(raw []byte) []float64 {
	out := make([]float64, len(raw)/4)
	for i := range out {
		out[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:])))
	}
	return out
}

// Tokenize 把查询切成去重后的词：ASCII 按 `[a-z0-9]+`，CJK 用双字滑窗（MEM4）。
func Tokenize(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	seen := make(map[string]bool, 8)
	out := make([]string, 0, 8)
	add := func(tok string) {
		if tok == "" || seen[tok] {
			return
		}
		seen[tok] = true
		out = append(out, tok)
	}
	for _, word := range asciiWordRe.FindAllString(query, -1) {
		add(word)
	}
	for _, run := range cjkRuns(query) {
		if len(run) == 1 {
			add(string(run))
			continue
		}
		for i := 0; i+1 < len(run); i++ {
			add(string(run[i : i+2]))
		}
	}
	return out
}

// cjkRuns 把字符串切成连续的 CJK 段。
func cjkRuns(s string) [][]rune {
	var runs [][]rune
	var cur []rune
	for _, r := range s {
		if isCJK(r) {
			cur = append(cur, r)
			continue
		}
		if len(cur) > 0 {
			runs = append(runs, cur)
			cur = nil
		}
	}
	if len(cur) > 0 {
		runs = append(runs, cur)
	}
	return runs
}

// isCJK 判断是否为汉字 / 假名 / 谚文。
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// haystack 把参与词法匹配的字段拼成一个可搜索的小写文本。
func haystack(rec Record) string {
	var b strings.Builder
	b.WriteString(rec.Content)
	for _, tag := range rec.Tags {
		b.WriteString(" ")
		b.WriteString(tag)
	}
	for _, subject := range rec.Subjects {
		if name, ok := subject["name"].(string); ok && name != "" {
			b.WriteString(" ")
			b.WriteString(name)
		}
	}
	return strings.ToLower(b.String())
}

// matchQuery 判断记录是否覆盖查询里的全部词（列表 q 过滤用）。
func matchQuery(rec Record, query string) bool {
	tokens := Tokenize(query)
	if len(tokens) == 0 {
		return true
	}
	hay := haystack(rec)
	for _, tok := range tokens {
		if !strings.Contains(hay, tok) {
			return false
		}
	}
	return true
}

// lexicalScore 是查询词命中率（0–1，空查询 0）。
func lexicalScore(tokens []string, rec Record) float64 {
	if len(tokens) == 0 {
		return 0
	}
	hay := haystack(rec)
	hits := 0
	for _, tok := range tokens {
		if strings.Contains(hay, tok) {
			hits++
		}
	}
	return float64(hits) / float64(len(tokens))
}

// cosine 计算余弦相似度；零向量返回 0。
func cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// recencyFactor 是时间衰减：max(0.3, exp(-ageDays/30))，ageDays 由 updated_at 算。
func recencyFactor(updatedAt string, now time.Time) float64 {
	t, ok := parseTime(updatedAt)
	if !ok {
		return 1
	}
	ageDays := now.Sub(t).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	factor := math.Exp(-ageDays / 30)
	if factor < 0.3 {
		factor = 0.3
	}
	return factor
}

// importanceFactor 是重要度权重：0.5 + importance/10（1→0.6 … 5→1.0）。
func importanceFactor(importance int) float64 {
	return 0.5 + float64(importance)/10
}

// strengthOf 取记录强度；缺失或非正数按 1 处理。
func strengthOf(rec Record) float64 {
	if rec.Strength <= 0 {
		return 1
	}
	return rec.Strength
}

// round4 把分数四舍五入到 4 位小数（memory.md §4）。
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }

// scoreRecord 按 MEM4 打分；返回 ok=false 表示该记录不参与结果。
func scoreRecord(rec Record, tokens []string, queryEmbedding *Embedding, now time.Time) (float64, bool) {
	lexical := lexicalScore(tokens, rec)
	base := lexical
	if queryEmbedding != nil {
		if qv, ok := queryEmbedding.Vector(); ok {
			if rv, ok := rec.Embedding.Vector(); ok {
				similarity := cosine(qv, rv)
				if similarity < 0 {
					similarity = 0
				}
				if similarity > 1 {
					similarity = 1
				}
				base = lexicalWeight*lexical + cosineWeight*similarity
			}
		}
	}
	if base <= 0 {
		return 0, false
	}
	score := base * importanceFactor(rec.Importance) * strengthOf(rec) * recencyFactor(rec.UpdatedAt, now)
	return round4(score), true
}

// Recall 按 MEM4 打分并排序（score 降序 → updated_at 降序 → id 升序）；
// 读路径不写盘，不回写 recall_count。
func (l *Library) Recall(req RecallRequest) ([]Scored, error) {
	req = req.normalize()
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	tokens := Tokenize(req.Query)
	if len(tokens) == 0 && req.QueryEmbedding == nil {
		return []Scored{}, nil
	}

	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	if err := l.refreshLocked(); err != nil {
		return []Scored{}, err
	}

	out := make([]Scored, 0, req.Limit)
	consider := func(m map[int64]Record) {
		for _, rec := range m {
			if !req.include(rec) {
				continue
			}
			score, ok := scoreRecord(rec, tokens, req.QueryEmbedding, now)
			if !ok {
				continue
			}
			out = append(out, Scored{Record: rec, Score: score})
		}
	}
	if req.wantKind(KindFact) {
		consider(l.facts)
	}
	if req.wantKind(KindEpisode) {
		consider(l.episodes)
	}
	if req.wantKind(KindEvicted) {
		consider(l.evicted)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		at, aok := parseTime(out[i].Record.UpdatedAt)
		bt, bok := parseTime(out[j].Record.UpdatedAt)
		if aok && bok && !at.Equal(bt) {
			return at.After(bt)
		}
		if aok != bok {
			return aok
		}
		return out[i].Record.ID < out[j].Record.ID
	})
	if len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return out, nil
}
