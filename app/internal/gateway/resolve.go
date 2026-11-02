package gateway

import (
	"errors"
	"fmt"
	"strings"

	"gqy/internal/config"
)

// ErrModelNotFound 表示模型解析全部未命中（R6）。
var ErrModelNotFound = errors.New("模型不存在")

// Candidate 是模型解析展开后的一个 provider/model 候选（R1）。
// 本单只解析到 provider/model，账号展开与运行时选择在后续施工单（R1–R5 运行时部分）。
type Candidate struct {
	Provider string
	Model    string
}

// Candidates 按 R6 四级解析并把结果展开为 provider/model 候选列表：
// group/<id> 递归展开；provider/model 单候选；裸模型名按 default_model 命中
// （多个按 providers 配置顺序全部给出）；空模型走 routing.default。
// 组嵌套与循环引用由 config 层 Build 校验，这里只做深度兜底。
func Candidates(snap *config.Snapshot, model string) ([]Candidate, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		if snap.Routing.Default == nil {
			return nil, fmt.Errorf("%w：未指定 model 且没有默认模型", ErrModelNotFound)
		}
		return []Candidate{{Provider: snap.Routing.Default.Provider, Model: snap.Routing.Default.Model}}, nil
	}

	if strings.HasPrefix(model, "group/") {
		id := strings.TrimPrefix(model, "group/")
		return expandGroup(snap, id, 0)
	}

	if provider, name, ok := strings.Cut(model, "/"); ok {
		if provider == "" || name == "" {
			return nil, fmt.Errorf("%w：%q 不是合法的 provider/model", ErrModelNotFound, model)
		}
		if lookupProvider(snap, provider) == nil {
			return nil, fmt.Errorf("%w：provider %q 不存在", ErrModelNotFound, provider)
		}
		return []Candidate{{Provider: provider, Model: name}}, nil
	}

	// 裸模型名：命中多个按 providers 配置顺序全部给出（R6 第 3 级）。
	candidates := bareCandidates(snap, model)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w：%q 未匹配任何 provider", ErrModelNotFound, model)
	}
	return candidates, nil
}

func lookupProvider(snap *config.Snapshot, id string) *config.Provider {
	for i := range snap.Providers {
		if snap.Providers[i].ID == id {
			return &snap.Providers[i]
		}
	}
	return nil
}

func lookupGroup(snap *config.Snapshot, id string) *config.Group {
	for i := range snap.Routing.Groups {
		if snap.Routing.Groups[i].ID == id {
			return &snap.Routing.Groups[i]
		}
	}
	return nil
}

// expandGroup 递归展开组成员，去重保序；循环引用由 Build 拒绝，这里只做深度兜底。
func expandGroup(snap *config.Snapshot, id string, depth int) ([]Candidate, error) {
	if depth > 64 {
		return nil, fmt.Errorf("%w：路由组嵌套过深", ErrModelNotFound)
	}
	g := lookupGroup(snap, id)
	if g == nil {
		return nil, fmt.Errorf("%w：路由组 %q 不存在", ErrModelNotFound, id)
	}

	out := make([]Candidate, 0, len(g.Members))
	seen := make(map[string]bool, len(g.Members))
	for _, member := range g.Members {
		var candidates []Candidate
		switch {
		case strings.HasPrefix(member, "group/"):
			next := strings.TrimPrefix(member, "group/")
			var err error
			candidates, err = expandGroup(snap, next, depth+1)
			if err != nil {
				return nil, err
			}
		case strings.Contains(member, "/"):
			provider, name, _ := strings.Cut(member, "/")
			if provider == "" || name == "" {
				return nil, fmt.Errorf("%w：组成员 %q 非法", ErrModelNotFound, member)
			}
			if lookupProvider(snap, provider) == nil {
				return nil, fmt.Errorf("%w：组成员引用了不存在的 provider %q", ErrModelNotFound, provider)
			}
			candidates = []Candidate{{Provider: provider, Model: name}}
		default:
			candidates = bareCandidates(snap, member)
			if len(candidates) == 0 {
				return nil, fmt.Errorf("%w：组成员 %q 未匹配任何 provider", ErrModelNotFound, member)
			}
		}
		for _, c := range candidates {
			key := c.Provider + "\x00" + c.Model
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, c)
		}
	}
	return out, nil
}

// bareCandidates 把裸模型名展开为全部匹配候选（R6：按 default_model 匹配）。
func bareCandidates(snap *config.Snapshot, model string) []Candidate {
	out := make([]Candidate, 0, 4)
	for _, p := range snap.Providers {
		if p.DefaultModel == model {
			out = append(out, Candidate{Provider: p.ID, Model: model})
		}
	}
	return out
}
