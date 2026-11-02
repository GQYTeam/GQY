// Package oneshot 实现一次性插件：按来源拉取、校验、执行、收集产物与清理（15 NX1–NX7）。
package oneshot

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type sourceKind int

const (
	srcLocal sourceKind = iota
	srcGit
	srcURL
	srcRegistry
)

// sourceSpec 是一次运行解析出的来源。
type sourceSpec struct {
	kind            sourceKind
	raw             string
	url             string // git / url 使用
	ref             string // git 使用
	registryName    string
	registryVersion string
}

var (
	registryNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	registryVerRe  = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,31}$`)
)

// ParseSource 解析四级来源：本地路径 / git / url / 内置注册表（NX2）。
func ParseSource(raw string) (sourceSpec, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return sourceSpec{}, errors.New("oneshot: 来源为空")
	}
	switch {
	case strings.HasPrefix(s, "reg:"):
		name := strings.TrimPrefix(s, "reg:")
		n, v := splitNameVersion(name)
		if !registryNameRe.MatchString(n) {
			return sourceSpec{}, fmt.Errorf("oneshot: 注册表条目名非法: %q", n)
		}
		return sourceSpec{kind: srcRegistry, raw: s, registryName: n, registryVersion: v}, nil

	case strings.HasPrefix(s, "git+"):
		rest := strings.TrimPrefix(s, "git+")
		u, ref := splitGitRef(rest)
		if u == "" {
			return sourceSpec{}, fmt.Errorf("oneshot: git 来源缺少地址: %q", s)
		}
		return sourceSpec{kind: srcGit, raw: s, url: u, ref: ref}, nil

	case strings.HasPrefix(s, "http://"), strings.HasPrefix(s, "https://"):
		if i := strings.Index(s, "#"); i >= 0 {
			return sourceSpec{kind: srcGit, raw: s, url: s[:i], ref: s[i+1:]}, nil
		}
		return sourceSpec{kind: srcURL, raw: s, url: s}, nil

	default:
		if isRegistryShorthand(s) {
			n, v := splitNameVersion(s)
			return sourceSpec{kind: srcRegistry, raw: s, registryName: n, registryVersion: v}, nil
		}
		return sourceSpec{kind: srcLocal, raw: s, url: s}, nil
	}
}

func splitGitRef(rest string) (url, ref string) {
	if i := strings.Index(rest, "#"); i >= 0 {
		return rest[:i], rest[i+1:]
	}
	mark := strings.Index(rest, "://")
	if mark >= 0 {
		if i := strings.LastIndex(rest, "@"); i > mark {
			return rest[:i], rest[i+1:]
		}
		return rest, ""
	}
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		return rest[:i], rest[i+1:]
	}
	return rest, ""
}

func splitNameVersion(s string) (name, version string) {
	if i := strings.LastIndex(s, "@"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func isRegistryShorthand(s string) bool {
	if strings.Contains(s, "://") {
		return false
	}
	if !strings.Contains(s, "@") {
		return false
	}
	n, v := splitNameVersion(s)
	return registryNameRe.MatchString(n) && registryVerRe.MatchString(v)
}
