// 包 cases：评测用例的加载、校验、快照与版本哈希。
//
// 用例可以是一个 JSON 文件，也可以是目录（按文件名排序合并所有 *.json）。
// 每个用例是「输入 + 期望行为 + 可选故障注入」，期望用约束而不是完全相等。
package cases

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var idRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Message 是用例里预置的消息。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Expect 描述期望行为。ShouldCall / MustCall 用指针区分「未设置」和「显式 false」。
// 两个字段都支持：设计文档用 should_call，工单口径也叫 must_call。
type Expect struct {
	ShouldCall    *bool          `json:"should_call,omitempty"`
	MustCall      *bool          `json:"must_call,omitempty"`
	MustNotCall   bool           `json:"must_not_call,omitempty"`
	Tool          string         `json:"tool,omitempty"`
	ArgContains   map[string]any `json:"arg_contains,omitempty"`
	FinalContains []string       `json:"final_contains,omitempty"`
}

// CallExpectation 返回显式的调用期望；must_call 优先，未设置返回 nil。
func (e Expect) CallExpectation() *bool {
	if e.MustCall != nil {
		return e.MustCall
	}
	return e.ShouldCall
}

// ShouldCallValue 返回是否要求调用；工具名或参数约束也隐含要求调用。
func (e Expect) ShouldCallValue() bool {
	if v := e.CallExpectation(); v != nil {
		return *v
	}
	return e.Tool != "" || len(e.ArgContains) > 0
}

// Inject 是评测宿主的故障注入配置。
type Inject struct {
	// Mode: empty（空结果）/ error（错误）/ timeout（超时）/ slow（慢响应）。
	Mode    string `json:"mode"`
	Tool    string `json:"tool,omitempty"`
	Times   int    `json:"times,omitempty"`
	DelayMS int    `json:"delay_ms,omitempty"`
	Message string `json:"message,omitempty"`
}

// Case 是一条评测用例。
type Case struct {
	ID       string    `json:"id"`
	Input    string    `json:"input,omitempty"`
	Messages []Message `json:"messages,omitempty"`
	Expect   Expect    `json:"expect,omitempty"`
	Inject   *Inject   `json:"inject,omitempty"`

	// Source 是来源文件名，只用于错误定位和快照，不进入哈希。
	Source string `json:"-"`
}

// Set 是一次加载得到的用例集。
type Set struct {
	Version string   `json:"version"`
	Cases   []Case   `json:"cases"`
	Hash    string   `json:"hash"`
	Files   []string `json:"files,omitempty"`
	// Filter 记录本次实际使用的 id 子集（为空表示全量）。
	Filter []string `json:"filter,omitempty"`
}

type fileDoc struct {
	Version string `json:"version"`
	Cases   []Case `json:"cases"`
}

// Load 读取文件或目录。路径不存在、JSON 非法、字段未知、用例重复都返回定位清晰的错误。
func Load(path string) (*Set, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("用例路径 %s: %w", path, err)
	}
	if info.IsDir() {
		return loadDir(path)
	}
	return loadFile(path)
}

func loadFile(path string) (*Set, error) {
	doc, err := readDoc(path)
	if err != nil {
		return nil, err
	}
	set := &Set{Version: doc.Version, Cases: doc.Cases, Files: []string{filepath.Base(path)}}
	for i := range set.Cases {
		set.Cases[i].Source = filepath.Base(path)
	}
	if err := validateSet(set); err != nil {
		return nil, err
	}
	set.recomputeHash()
	return set, nil
}

func loadDir(dir string) (*Set, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取用例目录 %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("用例目录 %s 下没有 *.json 文件", dir)
	}
	set := &Set{Files: names}
	seenVersion := ""
	for _, name := range names {
		doc, err := readDoc(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		if seenVersion == "" {
			seenVersion = doc.Version
		} else if doc.Version != seenVersion {
			return nil, fmt.Errorf("%s: 版本 %q 与同目录其他文件 %q 不一致", name, doc.Version, seenVersion)
		}
		for i := range doc.Cases {
			doc.Cases[i].Source = name
		}
		set.Cases = append(set.Cases, doc.Cases...)
	}
	set.Version = seenVersion
	if err := validateSet(set); err != nil {
		return nil, err
	}
	set.recomputeHash()
	return set, nil
}

func readDoc(path string) (fileDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return fileDoc{}, fmt.Errorf("读取用例 %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc fileDoc
	if err := dec.Decode(&doc); err != nil {
		return fileDoc{}, jsonLocError(filepath.Base(path), data, err)
	}
	if err := ensureEOF(dec); err != nil {
		return fileDoc{}, fmt.Errorf("%s: JSON 末尾有多余内容", filepath.Base(path))
	}
	return doc, nil
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	err := dec.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("extra")
	}
	return err
}

func validateSet(set *Set) error {
	if strings.TrimSpace(set.Version) == "" {
		return errors.New("用例集缺少 version")
	}
	if len(set.Cases) == 0 {
		return errors.New("用例集 cases 不能为空")
	}
	seen := map[string]string{}
	for i := range set.Cases {
		c := &set.Cases[i]
		where := fmt.Sprintf("%scases[%d]", prefix(c.Source), i)
		if err := validateCase(c, where); err != nil {
			return err
		}
		if prev, ok := seen[c.ID]; ok {
			return fmt.Errorf("%s.id: 重复用例 %q（已在 %s 出现）", where, c.ID, prev)
		}
		seen[c.ID] = where
	}
	return nil
}

func prefix(source string) string {
	if source == "" {
		return ""
	}
	return source + ": "
}

func validateCase(c *Case, where string) error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("%s.id: 不能为空", where)
	}
	if !idRE.MatchString(c.ID) {
		return fmt.Errorf("%s.id: %q 只能包含字母、数字、点、下划线和连字符", where, c.ID)
	}
	if c.Input != "" && len(c.Messages) > 0 {
		return fmt.Errorf("%s: input 与 messages 只能二选一", where)
	}
	if c.Input == "" && len(c.Messages) == 0 {
		return fmt.Errorf("%s: input 或 messages 必须提供一个", where)
	}
	if len(c.Messages) > 0 {
		hasUser := false
		for i, m := range c.Messages {
			switch m.Role {
			case "user":
				hasUser = true
			case "system", "assistant":
			default:
				return fmt.Errorf("%s.messages[%d].role: 未知角色 %q", where, i, m.Role)
			}
			if strings.TrimSpace(m.Content) == "" {
				return fmt.Errorf("%s.messages[%d].content: 不能为空", where, i)
			}
		}
		if !hasUser {
			return fmt.Errorf("%s.messages: 至少需要一条 user 消息", where)
		}
	}
	if c.Expect.ShouldCall != nil && c.Expect.MustCall != nil && *c.Expect.ShouldCall != *c.Expect.MustCall {
		return fmt.Errorf("%s.expect: should_call 与 must_call 冲突", where)
	}
	if v := c.Expect.CallExpectation(); v != nil && *v && c.Expect.MustNotCall {
		return fmt.Errorf("%s.expect: should_call/must_call 与 must_not_call 不能同时为 true", where)
	}
	for k := range c.Expect.ArgContains {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("%s.expect.arg_contains: 参数名不能为空", where)
		}
	}
	for i, fact := range c.Expect.FinalContains {
		if strings.TrimSpace(fact) == "" {
			return fmt.Errorf("%s.expect.final_contains[%d]: 不能为空", where, i)
		}
	}
	if c.Inject != nil {
		switch c.Inject.Mode {
		case "empty", "error", "timeout", "slow":
		default:
			return fmt.Errorf("%s.inject.mode: 未知故障 %q（可选 empty/error/timeout/slow）", where, c.Inject.Mode)
		}
		if c.Inject.Times < 0 {
			return fmt.Errorf("%s.inject.times: 不能为负数", where)
		}
		if c.Inject.Times == 0 {
			c.Inject.Times = 1
		}
		if c.Inject.DelayMS < 0 {
			return fmt.Errorf("%s.inject.delay_ms: 不能为负数", where)
		}
		if c.Inject.Mode == "slow" && c.Inject.DelayMS == 0 {
			c.Inject.DelayMS = 100
		}
	}
	return nil
}

// recomputeHash 计算用例集版本哈希；版本、文件列表、用例内容任一变化都会改变哈希。
func (s *Set) recomputeHash() {
	doc := struct {
		Version string   `json:"version"`
		Files   []string `json:"files"`
		Filter  []string `json:"filter,omitempty"`
		Cases   []Case   `json:"cases"`
	}{Version: s.Version, Files: s.Files, Filter: s.Filter, Cases: s.Cases}
	data, _ := json.Marshal(doc)
	sum := sha256.Sum256(data)
	s.Hash = hex.EncodeToString(sum[:])
}

// Snapshot 返回可落盘的用例快照（含哈希、来源文件和实际用例）。
func (s *Set) Snapshot() ([]byte, error) {
	doc := struct {
		Version string   `json:"version"`
		Hash    string   `json:"hash"`
		Files   []string `json:"files,omitempty"`
		Filter  []string `json:"filter,omitempty"`
		Cases   []Case   `json:"cases"`
	}{Version: s.Version, Hash: s.Hash, Files: s.Files, Filter: s.Filter, Cases: s.Cases}
	return json.MarshalIndent(doc, "", "  ")
}

// Subset 按给定 id 取子集，保留原用例顺序；未知 id 报错。
func (s *Set) Subset(ids []string) (*Set, error) {
	if len(ids) == 0 {
		return s, nil
	}
	want := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, errors.New("用例过滤值不能为空")
		}
		want[id] = true
	}
	out := &Set{Version: s.Version, Files: append([]string(nil), s.Files...), Filter: append([]string(nil), ids...)}
	for _, c := range s.Cases {
		if want[c.ID] {
			out.Cases = append(out.Cases, c)
			delete(want, c.ID)
		}
	}
	if len(want) > 0 {
		missing := make([]string, 0, len(want))
		for id := range want {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("用例过滤包含不存在的 id: %s", strings.Join(missing, ", "))
	}
	out.recomputeHash()
	return out, nil
}

// jsonLocError 给 JSON 语法/类型错误补上文件名和行列号。
func jsonLocError(source string, data []byte, err error) error {
	offset := int64(-1)
	var se *json.SyntaxError
	var ute *json.UnmarshalTypeError
	switch {
	case errors.As(err, &se):
		offset = se.Offset
	case errors.As(err, &ute):
		offset = ute.Offset
	}
	where := source
	if offset >= 0 {
		line, col := lineCol(data, offset)
		where = fmt.Sprintf("%s:%d:%d", source, line, col)
	}
	return fmt.Errorf("%s: JSON 解析失败: %v", where, err)
}

func lineCol(data []byte, offset int64) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > int64(len(data)) {
		offset = int64(len(data))
	}
	line, col := 1, 1
	for i := int64(0); i < offset; i++ {
		if data[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}
