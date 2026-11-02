package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newHome 造一个真实的数据根（含 memory/），测试只走生产代码路径。
func newHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "memory"), 0o700); err != nil {
		t.Fatalf("建 memory 目录: %v", err)
	}
	return home
}

// countLines 数 JSONL 里的非空行。
func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("读 %s: %v", path, err)
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// mustLibrary 取人格库。
func mustLibrary(t *testing.T, home, persona string) *Library {
	t.Helper()
	lib, err := NewStore(home).Library(persona)
	if err != nil {
		t.Fatalf("Library(%s): %v", persona, err)
	}
	return lib
}

// appendRawLine 直接追加一行 JSONL，模拟迁移器写文件（走真实读路径）。
func appendRawLine(t *testing.T, home, file, line string) {
	t.Helper()
	dir := filepath.Join(home, "memory", DefaultPersona)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, file), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("打开 %s: %v", file, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatalf("写 %s: %v", file, err)
	}
}

func TestStoreAppendLoad(t *testing.T) {
	home := newHome(t)
	lib := mustLibrary(t, home, DefaultPersona)

	rec, err := lib.Remember(RememberInput{
		Content:          "用户喜欢美式咖啡，不加糖",
		MemoryType:       TypePreference,
		Importance:       4,
		Tags:             []string{"偏好", "饮品"},
		OwnerPrincipal:   "bob@example.com",
		OwnerDisplayName: "Bob",
		Source:           "api",
	})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if rec.ID <= 0 || rec.Status != StatusActive || rec.TruthStatus != TruthAccepted {
		t.Fatalf("记录默认值不对: %+v", rec)
	}

	factsPath := filepath.Join(home, "memory", DefaultPersona, factsFileName)
	if got := countLines(t, factsPath); got != 1 {
		t.Fatalf("facts.jsonl 行数 = %d, want 1", got)
	}
	info, err := os.Stat(factsPath)
	if err != nil {
		t.Fatalf("stat facts.jsonl: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("facts.jsonl 权限 = %o, want 600", info.Mode().Perm())
	}
	data, err := os.ReadFile(factsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Fatal("JSONL 行应以 \\n 结尾")
	}
	var line map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSuffix(string(data), "\n")), &line); err != nil {
		t.Fatalf("落盘行不是合法 JSON: %v", err)
	}

	// 重新载入：记录数与字段一致（走真实文件）。
	fresh := mustLibrary(t, home, DefaultPersona)
	items, total, err := fresh.ListFacts(Filter{})
	if err != nil {
		t.Fatalf("ListFacts: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("重新加载后 total=%d len=%d, want 1", total, len(items))
	}
	got := items[0]
	if got.ID != rec.ID || got.Content != rec.Content || got.MemoryType != TypePreference ||
		got.Importance != 4 || got.OwnerPrincipal != "bob@example.com" ||
		len(got.Tags) != 2 || got.Tags[0] != "偏好" {
		t.Fatalf("重新加载的字段不一致: %+v", got)
	}

	// meta.json 在首次写入时建好。
	metaPath := filepath.Join(home, "memory", metaFileName)
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("meta.json 未创建: %v", err)
	}
}

func TestStoreIdempotentLegacyID(t *testing.T) {
	home := newHome(t)
	lib := mustLibrary(t, home, DefaultPersona)

	first, err := lib.Remember(RememberInput{Content: "旧库第一条", LegacyID: "legacy:facts:1"})
	if err != nil {
		t.Fatalf("首次写入: %v", err)
	}
	second, err := lib.Remember(RememberInput{Content: "重复写入不该覆盖", LegacyID: "legacy:facts:1"})
	if err != nil {
		t.Fatalf("重复写入: %v", err)
	}
	if second.ID != first.ID || second.Content != "旧库第一条" {
		t.Fatalf("legacy_id 幂等失败: first=%+v second=%+v", first, second)
	}
	factsPath := filepath.Join(home, "memory", DefaultPersona, factsFileName)
	if got := countLines(t, factsPath); got != 1 {
		t.Fatalf("重复写入产生第二行: %d", got)
	}

	// 换一个新实例重新载入后仍然幂等（索引来自文件）。
	fresh := mustLibrary(t, home, DefaultPersona)
	third, err := fresh.Remember(RememberInput{Content: "第三次", LegacyID: "legacy:facts:1"})
	if err != nil {
		t.Fatalf("重新载入后写入: %v", err)
	}
	if third.ID != first.ID {
		t.Fatalf("重新载入后幂等失败: %d != %d", third.ID, first.ID)
	}
}

func TestArchiveViaRevision(t *testing.T) {
	home := newHome(t)
	lib := mustLibrary(t, home, DefaultPersona)

	rec, err := lib.Remember(RememberInput{Content: "临时偏好：先喝茶", MemoryType: TypePreference})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}

	archived, err := lib.SetFactStatus(rec.ID, StatusArchived)
	if err != nil {
		t.Fatalf("SetFactStatus: %v", err)
	}
	if archived.ID != rec.ID || archived.Status != StatusArchived {
		t.Fatalf("归档返回不对: %+v", archived)
	}

	dir := filepath.Join(home, "memory", DefaultPersona)
	if got := countLines(t, filepath.Join(dir, factsFileName)); got != 2 {
		t.Fatalf("归档后 facts.jsonl 行数 = %d, want 2（追加不删行）", got)
	}
	if got := countLines(t, filepath.Join(dir, revisionsFileName)); got != 1 {
		t.Fatalf("归档后 revisions.jsonl 行数 = %d, want 1", got)
	}

	// 读时以该 id 最后一行有效。
	fresh := mustLibrary(t, home, DefaultPersona)
	active, total, err := fresh.ListFacts(Filter{})
	if err != nil {
		t.Fatalf("ListFacts: %v", err)
	}
	if total != 0 || len(active) != 0 {
		t.Fatalf("默认列表应排除归档: total=%d", total)
	}
	all, total, err := fresh.ListFacts(Filter{IncludeArchived: true})
	if err != nil {
		t.Fatalf("ListFacts include_archived: %v", err)
	}
	if total != 1 || len(all) != 1 || all[0].Status != StatusArchived || all[0].ID != rec.ID {
		t.Fatalf("归档记录读取不对: total=%d items=%+v", total, all)
	}

	revs, err := fresh.Revisions(rec.ID)
	if err != nil {
		t.Fatalf("Revisions: %v", err)
	}
	if len(revs) != 1 || revs[0].MemoryID != rec.ID || revs[0].OldContent != rec.Content || revs[0].Kind != KindRevision {
		t.Fatalf("修订链不对: %+v", revs)
	}

	// 恢复同样只追加。
	if _, err := fresh.SetFactStatus(rec.ID, StatusActive); err != nil {
		t.Fatalf("恢复: %v", err)
	}
	if got := countLines(t, filepath.Join(dir, factsFileName)); got != 3 {
		t.Fatalf("恢复后 facts.jsonl 行数 = %d, want 3", got)
	}
	if got := countLines(t, filepath.Join(dir, revisionsFileName)); got != 2 {
		t.Fatalf("恢复后 revisions.jsonl 行数 = %d, want 2", got)
	}
	restored := mustLibrary(t, home, DefaultPersona)
	items, _, err := restored.ListFacts(Filter{})
	if err != nil {
		t.Fatalf("ListFacts: %v", err)
	}
	if len(items) != 1 || items[0].Status != StatusActive {
		t.Fatalf("恢复后状态不对: %+v", items)
	}

	if _, err := lib.SetFactStatus(9999, StatusArchived); err == nil {
		t.Fatal("归档不存在记录应报错")
	}
	if _, err := lib.SetFactStatus(rec.ID, "bogus"); err == nil {
		t.Fatal("非法状态应报错")
	}
}

func TestCorruptLineSkipped(t *testing.T) {
	home := newHome(t)
	dir := filepath.Join(home, "memory", DefaultPersona)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := `{"id":1,"kind":"fact","content":"好行一","created_at":"2026-01-01T00:00:00+08:00","updated_at":"2026-01-01T00:00:00+08:00"}
{"id":2,"kind":"fact","content": 半行坏 JSON
{"id":3,"kind":"fact","content":"好行二","created_at":"2026-01-02T00:00:00+08:00","updated_at":"2026-01-02T00:00:00+08:00"}
`
	if err := os.WriteFile(filepath.Join(dir, factsFileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	lib := mustLibrary(t, home, DefaultPersona) // 不应 panic、不应失败
	counts, err := lib.Counts()
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Facts != 2 {
		t.Fatalf("facts = %d, want 2（坏行跳过）", counts.Facts)
	}
	warnings := lib.Warnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "facts.jsonl:2") {
		t.Fatalf("告警不对: %+v", warnings)
	}

	// 坏行不影响列表与召回。
	items, total, err := lib.ListFacts(Filter{})
	if err != nil {
		t.Fatalf("ListFacts: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("列表 total=%d len=%d, want 2", total, len(items))
	}
}

func TestPersonasList(t *testing.T) {
	home := newHome(t)
	if _, err := mustLibrary(t, home, "default").Remember(RememberInput{Content: "默认库一条"}); err != nil {
		t.Fatal(err)
	}
	if _, err := mustLibrary(t, home, "legacy-bot").Remember(RememberInput{Content: "旧库一条"}); err != nil {
		t.Fatal(err)
	}
	// personas.json 声明一个人格（迁移产物），与目录扫描结果合并。
	personas := `{"personas":[{"id":"legacy-bot","source":"legacy","subsystems":{"memory":true}}]}`
	if err := os.WriteFile(filepath.Join(home, "memory", personasFileName), []byte(personas), 0o600); err != nil {
		t.Fatal(err)
	}

	infos, err := NewStore(home).Personas()
	if err != nil {
		t.Fatalf("Personas: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("personas = %+v, want 2", infos)
	}
	if infos[0].ID != "default" || infos[1].ID != "legacy-bot" {
		t.Fatalf("personas 未按 id 排序: %+v", infos)
	}
	for _, info := range infos {
		if info.Counts.Facts != 1 {
			t.Fatalf("%s facts = %d, want 1", info.ID, info.Counts.Facts)
		}
		if info.UpdatedAt == "" {
			t.Fatalf("%s 缺 updated_at", info.ID)
		}
	}
	if infos[1].Source != "legacy" {
		t.Fatalf("persona source = %q, want legacy", infos[1].Source)
	}

	stats, err := mustLibrary(t, home, "default").Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Persona != "default" || stats.Counts.Facts != 1 || stats.ByType[TypeFact] != 1 || stats.ByTruth[TruthAccepted] != 1 {
		t.Fatalf("stats 不对: %+v", stats)
	}
}

func TestEmptyStore(t *testing.T) {
	home := t.TempDir() // 连 memory/ 都没有：模拟默认 home 的空库
	store := NewStore(home)

	infos, err := store.Personas()
	if err != nil {
		t.Fatalf("空库 Personas 应成功: %v", err)
	}
	if infos == nil {
		t.Fatal("空库清单应是非 nil 空切片")
	}
	if len(infos) != 0 {
		t.Fatalf("空库 personas = %+v", infos)
	}

	lib, err := store.Library(DefaultPersona)
	if err != nil {
		t.Fatalf("空库 Library: %v", err)
	}
	counts, err := lib.Counts()
	if err != nil || counts != (Counts{}) {
		t.Fatalf("空库 counts = %+v, err=%v", counts, err)
	}
	items, total, err := lib.ListFacts(Filter{})
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("空库列表 total=%d err=%v", total, err)
	}
	scored, err := lib.Recall(RecallRequest{Query: "任何"})
	if err != nil || len(scored) != 0 {
		t.Fatalf("空库召回 = %+v, err=%v", scored, err)
	}
	if store.SchemaVersion() != SchemaVersion {
		t.Fatalf("空库 schema_version = %d", store.SchemaVersion())
	}
}

func TestInvalidPersona(t *testing.T) {
	home := newHome(t)
	store := NewStore(home)
	for _, bad := range []string{"../etc", "Default", "a/b", "", "x"} {
		if bad == "" {
			// 空值按缺省人格处理，不算非法。
			if _, err := store.Library(bad); err != nil {
				t.Fatalf("空 persona 应回退默认: %v", err)
			}
			continue
		}
		if bad == "x" {
			// 单个小写字母合法。
			if _, err := store.Library(bad); err != nil {
				t.Fatalf("合法 persona %q 报错: %v", bad, err)
			}
			continue
		}
		if _, err := store.Library(bad); err == nil {
			t.Fatalf("非法 persona %q 应报错", bad)
		}
	}
}

func TestLegacyFieldShapes(t *testing.T) {
	home := newHome(t)
	dir := filepath.Join(home, "memory", DefaultPersona)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// subjects 是对象数组（未知字段保留）、tags 是字符串数组、
	// source_episode_ids 是整数数组；旧库偶尔把这些存成 JSON 文本。
	line := `{"id":1,"kind":"fact","content":"旧库字段形状","memory_type":"fact",` +
		`"tags":"[\"偏好\",\"饮品\"]",` +
		`"subjects":[{"name":"本人","role":"owner","extra":{"k":1}}],` +
		`"source_episode_ids":[11,12],` +
		`"created_at":"2026-01-01T00:00:00+08:00","updated_at":"2026-01-01T00:00:00+08:00"}`
	if err := os.WriteFile(filepath.Join(dir, factsFileName), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	items, total, err := mustLibrary(t, home, DefaultPersona).ListFacts(Filter{})
	if err != nil || total != 1 {
		t.Fatalf("ListFacts total=%d err=%v", total, err)
	}
	got := items[0]
	if len(got.Tags) != 2 || got.Tags[1] != "饮品" {
		t.Fatalf("tags 未还原: %v", got.Tags)
	}
	if len(got.Subjects) != 1 || got.Subjects[0]["name"] != "本人" || got.Subjects[0]["role"] != "owner" {
		t.Fatalf("subjects 不是对象数组或丢了字段: %#v", got.Subjects)
	}
	if len(got.SourceEpisodeIDs) != 2 || got.SourceEpisodeIDs[0] != 11 || got.SourceEpisodeIDs[1] != 12 {
		t.Fatalf("source_episode_ids 未还原成整数数组: %v", got.SourceEpisodeIDs)
	}

	// 视图输出的 subjects 仍是对象数组（不会被降级成字符串）。
	view := FactViewOf(got)
	if len(view.Subjects) != 1 || view.Subjects[0]["name"] != "本人" {
		t.Fatalf("视图 subjects = %#v", view.Subjects)
	}
}

func TestReadPathDoesNotWrite(t *testing.T) {
	home := newHome(t)
	lib := mustLibrary(t, home, DefaultPersona)
	if _, err := lib.Remember(RememberInput{Content: "读路径不写盘"}); err != nil {
		t.Fatal(err)
	}
	factsPath := filepath.Join(home, "memory", DefaultPersona, factsFileName)
	before, err := os.Stat(factsPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeBytes, err := os.ReadFile(factsPath)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if _, err := lib.Recall(RecallRequest{Query: "读路径"}); err != nil {
			t.Fatalf("Recall: %v", err)
		}
		if _, _, err := lib.ListFacts(Filter{}); err != nil {
			t.Fatalf("ListFacts: %v", err)
		}
		if _, err := lib.Stats(); err != nil {
			t.Fatalf("Stats: %v", err)
		}
	}

	after, err := os.Stat(factsPath)
	if err != nil {
		t.Fatal(err)
	}
	afterBytes, err := os.ReadFile(factsPath)
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() != after.Size() || string(beforeBytes) != string(afterBytes) {
		t.Fatalf("读路径改了文件：before=%d after=%d", before.Size(), after.Size())
	}
	items, _, err := lib.ListFacts(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].RecallCount != 0 {
		t.Fatalf("召回不应回写 recall_count: %d", items[0].RecallCount)
	}
}

func TestExternalAppendVisible(t *testing.T) {
	home := newHome(t)
	store := NewStore(home)
	lib, err := store.Library(DefaultPersona)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Remember(RememberInput{Content: "第一条"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lib.ListFacts(Filter{}); err != nil { // 先建索引
		t.Fatal(err)
	}

	// 模拟迁移器在另一个进程里追加了记录。
	path := filepath.Join(home, "memory", DefaultPersona, factsFileName)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":99,"kind":"fact","content":"外部追加","created_at":"2026-02-01T00:00:00+08:00","updated_at":"2026-02-01T00:00:00+08:00"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	items, total, err := lib.ListFacts(Filter{})
	if err != nil {
		t.Fatalf("ListFacts: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("外部追加未刷新: total=%d", total)
	}
}

func TestReload(t *testing.T) {
	home := newHome(t)
	store := NewStore(home)
	lib, err := store.Library(DefaultPersona)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Remember(RememberInput{Content: "一条"}); err != nil {
		t.Fatal(err)
	}
	store.Reload()
	again, err := store.Library(DefaultPersona)
	if err != nil {
		t.Fatal(err)
	}
	if _, total, err := again.ListFacts(Filter{}); err != nil || total != 1 {
		t.Fatalf("Reload 后 total=%d err=%v", total, err)
	}
}
