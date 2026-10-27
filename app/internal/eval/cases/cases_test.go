package cases

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func oneCase(id, input string) string {
	return `{"version":"1","cases":[{"id":"` + id + `","input":"` + input + `","expect":{"should_call":true,"final_contains":["ok"]}}]}`
}

func TestLoadFileHashStableAndSensitive(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "cases.json", oneCase("a", "hello"))
	set, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(set.Cases) != 1 || set.Cases[0].Source != "cases.json" {
		t.Fatalf("用例加载不对: %+v", set.Cases)
	}
	if len(set.Hash) != 64 {
		t.Fatalf("hash 长度=%d", len(set.Hash))
	}
	first := set.Hash

	set2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if set2.Hash != first {
		t.Fatalf("同内容 hash 不稳定: %s vs %s", first, set2.Hash)
	}

	write(t, dir, "cases.json", oneCase("a", "hello!"))
	set3, _ := Load(path)
	if set3.Hash == first {
		t.Fatal("内容变化后 hash 未变化")
	}

	write(t, dir, "cases.json", strings.Replace(oneCase("a", "hello"), `"version":"1"`, `"version":"2"`, 1))
	set4, _ := Load(path)
	if set4.Hash == first {
		t.Fatal("版本变化后 hash 未变化")
	}
}

func TestLoadDirAndDuplicate(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.json", oneCase("a", "a"))
	write(t, dir, "b.json", oneCase("b", "b"))
	write(t, dir, "ignore.txt", "not json")
	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load dir: %v", err)
	}
	if len(set.Cases) != 2 || len(set.Files) != 2 || set.Files[0] != "a.json" || set.Files[1] != "b.json" {
		t.Fatalf("目录合并不对: %+v files=%v", set.Cases, set.Files)
	}

	write(t, dir, "a.json", oneCase("dup", "a"))
	write(t, dir, "b.json", oneCase("dup", "b"))
	_, err = Load(dir)
	if err == nil || !strings.Contains(err.Error(), "重复用例") {
		t.Fatalf("期望重复用例错误，实际: %v", err)
	}

	write(t, dir, "a.json", strings.Replace(oneCase("a", "a"), `"version":"1"`, `"version":"9"`, 1))
	write(t, dir, "b.json", oneCase("b", "b"))
	_, err = Load(dir)
	if err == nil || !strings.Contains(err.Error(), "版本") {
		t.Fatalf("期望版本不一致错误，实际: %v", err)
	}
}

func TestValidation(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"both.json":     `{"version":"1","cases":[{"id":"x","input":"a","messages":[{"role":"user","content":"b"}]}]}`,
		"neither.json":  `{"version":"1","cases":[{"id":"x"}]}`,
		"role.json":     `{"version":"1","cases":[{"id":"x","messages":[{"role":"tool","content":"b"}]}]}`,
		"inject.json":   `{"version":"1","cases":[{"id":"x","input":"a","inject":{"mode":"boom"}}]}`,
		"expect.json":   `{"version":"1","cases":[{"id":"x","input":"a","expect":{"should_call":true,"must_not_call":true}}]}`,
		"unknown.json":  `{"version":"1","cases":[{"id":"x","input":"a","foo":1}]}`,
		"badid.json":    `{"version":"1","cases":[{"id":"../x","input":"a"}]}`,
		"mustcall.json": `{"version":"1","cases":[{"id":"x","input":"a","expect":{"should_call":true,"must_call":false}}]}`,
	}
	wants := map[string]string{
		"both.json":     "只能二选一",
		"neither.json":  "必须提供一个",
		"role.json":     "未知角色",
		"inject.json":   "未知故障",
		"expect.json":   "不能同时为 true",
		"unknown.json":  `unknown field "foo"`,
		"badid.json":    "只能包含字母",
		"mustcall.json": "冲突",
	}
	for name, body := range cases {
		path := write(t, dir, name, body)
		_, err := Load(path)
		if err == nil || !strings.Contains(err.Error(), wants[name]) {
			t.Errorf("%s: 期望包含 %q，实际: %v", name, wants[name], err)
		}
	}
}

func TestSubsetAndInjectDefaults(t *testing.T) {
	dir := t.TempDir()
	body := `{"version":"1","cases":[
		{"id":"a","input":"a","inject":{"mode":"slow"}},
		{"id":"b","input":"b"}
	]}`
	path := write(t, dir, "cases.json", body)
	set, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if set.Cases[0].Inject.Times != 1 || set.Cases[0].Inject.DelayMS != 100 {
		t.Fatalf("inject 默认值不对: %+v", set.Cases[0].Inject)
	}
	sub, err := set.Subset([]string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Cases) != 1 || sub.Cases[0].ID != "b" || sub.Hash == set.Hash {
		t.Fatalf("子集不对: %+v hash %s/%s", sub.Cases, sub.Hash, set.Hash)
	}
	if _, err := set.Subset([]string{"missing"}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("期望未知 id 错误，实际: %v", err)
	}
}

func TestLoadMissing(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("期望路径不存在错误")
	}
}
