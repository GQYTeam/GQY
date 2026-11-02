package memory

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestValidHelpers(t *testing.T) {
	for _, id := range []string{"default", "a", "bob-01", "x_y", strings.Repeat("a", 32)} {
		if !ValidPersona(id) {
			t.Fatalf("合法 persona %q 被判非法", id)
		}
	}
	for _, id := range []string{"", strings.Repeat("a", 33), "A", "-a", "a/b", "../x", "a b"} {
		if ValidPersona(id) {
			t.Fatalf("非法 persona %q 被判合法", id)
		}
	}
	if !ValidMemoryType(TypePreference) || ValidMemoryType("bogus") {
		t.Fatal("memory_type 校验不对")
	}
	if !ValidTruthStatus(TruthReported) || ValidTruthStatus("maybe") {
		t.Fatal("truth_status 校验不对")
	}
	if !ValidStatus(StatusArchived) || ValidStatus("deleted") {
		t.Fatal("status 校验不对")
	}
	if !ValidRetention(RetentionLong) || ValidRetention("forever") {
		t.Fatal("retention 校验不对")
	}
	if !ValidVisibility(VisibilityNormal) || ValidVisibility("secret") {
		t.Fatal("visibility 校验不对")
	}
	if !RecallableKind(KindEvicted) || RecallableKind(KindRevision) {
		t.Fatal("可召回种类校验不对")
	}
}

func TestRememberInputValidation(t *testing.T) {
	bad := []RememberInput{
		{Content: "   "},
		{Content: strings.Repeat("长", MaxContentRunes+1)},
		{Content: "ok", MemoryType: "bogus"},
		{Content: "ok", TruthStatus: "maybe"},
		{Content: "ok", Importance: 6},
		{Content: "ok", Importance: -1},
		{Content: "ok", Confidence: 2},
		{Content: "ok", Visibility: "secret"},
	}
	for i, in := range bad {
		if _, err := in.normalize(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("第 %d 个非法入参未被拒绝: err=%v", i, err)
		}
	}

	got, err := (RememberInput{Content: "  默认值测试  "}).normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got.Content != "默认值测试" || got.MemoryType != TypeFact || got.TruthStatus != TruthAccepted ||
		got.Importance != DefaultImportance || got.Confidence != 1 || got.Visibility != VisibilityPrivileged ||
		got.Source != "api" {
		t.Fatalf("默认值不对: %+v", got)
	}
}

func TestMaskOwnerAndTruncate(t *testing.T) {
	cases := map[string]string{
		"":                "",
		"a":               "*",
		"bob":             "b***b",
		"bob@example.com": "b***m",
		"张三":              "张***三",
	}
	for in, want := range cases {
		if got := MaskOwner(in); got != want {
			t.Fatalf("MaskOwner(%q) = %q, want %q", in, got, want)
		}
	}

	exact := strings.Repeat("字", MaxContentRunes)
	if got, truncated := TruncateContent(exact); truncated || got != exact {
		t.Fatalf("正好 %d 字符不应截断", MaxContentRunes)
	}
	long := strings.Repeat("字", MaxContentRunes+10)
	got, truncated := TruncateContent(long)
	if !truncated {
		t.Fatal("超长正文应标 truncated")
	}
	if len([]rune(got)) != MaxContentRunes {
		t.Fatalf("截断长度 = %d, want %d", len([]rune(got)), MaxContentRunes)
	}
}

func TestViewsDesensitize(t *testing.T) {
	long := strings.Repeat("秘", MaxContentRunes+50)
	rec := Record{
		ID:               7,
		Kind:             KindFact,
		Content:          long,
		MemoryType:       TypeSelf,
		TruthStatus:      TruthAccepted,
		Status:           StatusActive,
		Importance:       3,
		Confidence:       0.8,
		Strength:         0.9,
		RecallCount:      2,
		CreatedAt:        "2026-01-01T00:00:00+08:00",
		UpdatedAt:        "2026-01-02T00:00:00+08:00",
		Visibility:       VisibilityPrivileged,
		OwnerPrincipal:   "bob@example.com",
		OwnerDisplayName: "",
		Tags:             StringList{"偏好"},
		LegacyID:         "legacy:facts:7",
	}
	view := FactViewOf(rec)
	if !view.Truncated || len([]rune(view.Content)) != MaxContentRunes {
		t.Fatalf("视图未截断: truncated=%v len=%d", view.Truncated, len([]rune(view.Content)))
	}
	if view.Owner != "b***m" {
		t.Fatalf("owner 未掩码: %q", view.Owner)
	}
	if view.ID != 7 || view.LegacyID != "legacy:facts:7" || len(view.Tags) != 1 {
		t.Fatalf("视图字段丢失: %+v", view)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("序列化视图: %v", err)
	}
	if strings.Contains(string(raw), "bob@example.com") {
		t.Fatalf("视图泄漏了 owner_principal 原值: %s", raw)
	}
	if strings.Contains(string(raw), "owner_principal") {
		t.Fatalf("视图不应输出 owner_principal 字段: %s", raw)
	}

	ep := EpisodeViewOf(Record{
		ID:               8,
		Kind:             KindEpisode,
		Content:          "片段摘要",
		Retention:        RetentionLong,
		UserMessage:      "用户说的话",
		AssistantMessage: "助手说的话",
		SourceKey:        "k-1",
		OriginPlatform:   "imessage",
		OwnerPrincipal:   "bob@example.com",
	})
	if ep.Retention != RetentionLong || ep.UserMessage != "用户说的话" || ep.Owner != "b***m" {
		t.Fatalf("片段视图不对: %+v", ep)
	}

	ev := EvictedViewOf(Record{ID: 9, Kind: KindEvicted, Content: "原话", Role: "user", OwnerPrincipal: "bob@example.com"})
	if ev.Role != "user" || ev.Owner != "b***m" || ev.Content != "原话" {
		t.Fatalf("挤出轮次视图不对: %+v", ev)
	}

	rev := RevisionViewOf(Record{ID: 10, Kind: KindRevision, MemoryID: 7, OldContent: "旧", NewContent: "新", SourceEpisodeIDs: Int64List{1, 2}})
	if rev.MemoryID != 7 || rev.OldContent != "旧" || len(rev.SourceEpisodeIDs) != 2 {
		t.Fatalf("修订视图不对: %+v", rev)
	}
}

func TestListOptionsTolerance(t *testing.T) {
	var payload struct {
		Tags     StringList  `json:"tags"`
		Subjects SubjectList `json:"subjects"`
		Episodes Int64List   `json:"eps"`
	}
	raw := `{"tags":"[\"a\",\"b\"]","subjects":[{"name":"本人","role":"owner"},{"name":"家人"}],"eps":[11,"12",null]}`
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("兼容解码失败: %v", err)
	}
	if len(payload.Tags) != 2 || payload.Tags[1] != "b" {
		t.Fatalf("tags = %v", payload.Tags)
	}
	if len(payload.Subjects) != 2 || payload.Subjects[0]["role"] != "owner" {
		t.Fatalf("subjects = %#v", payload.Subjects)
	}
	if len(payload.Episodes) != 2 || payload.Episodes[0] != 11 || payload.Episodes[1] != 12 {
		t.Fatalf("source_episode_ids = %v", payload.Episodes)
	}

	// 字符串数组写法补成对象；纯文本也接受。
	var subjects SubjectList
	if err := json.Unmarshal([]byte(`["小明"]`), &subjects); err != nil {
		t.Fatalf("字符串数组解码: %v", err)
	}
	if len(subjects) != 1 || subjects[0]["name"] != "小明" {
		t.Fatalf("字符串数组未补成对象: %#v", subjects)
	}
	if err := json.Unmarshal([]byte(`"[\"小红\"]"`), &subjects); err != nil {
		t.Fatalf("JSON 文本解码: %v", err)
	}
	if len(subjects) != 1 || subjects[0]["name"] != "小红" {
		t.Fatalf("JSON 文本未还原: %#v", subjects)
	}
	if err := json.Unmarshal([]byte(`null`), &subjects); err != nil || subjects != nil {
		t.Fatalf("null 解码: %v %#v", err, subjects)
	}

	var tags StringList
	if err := json.Unmarshal([]byte(`"a, b"`), &tags); err != nil || len(tags) != 2 || tags[1] != "b" {
		t.Fatalf("逗号分隔文本解码: %v %v", err, tags)
	}
}
