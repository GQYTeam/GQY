package httpapi

import "gqy/internal/config"

// Capabilities 是 /v1/models 中每个 provider 模型的能力描述（MD2）。
// 布尔能力缺省为 false、数值能力缺省为 0、reasoning_effort 为空数组，都表示「未知」。
type Capabilities struct {
	ToolCall        bool     `json:"tool_call"`
	Vision          bool     `json:"vision"`
	Thinking        bool     `json:"thinking"`
	ReasoningEffort []string `json:"reasoning_effort"`
	ContextWindow   int      `json:"context_window"`
	MaxOutput       int      `json:"max_output"`
}

// modelEntry 是 /v1/models 的一项（C11 + MD4 capabilities）。
// 路由组条目不携带 capabilities（组能力看成员）。
type modelEntry struct {
	ID           string        `json:"id"`
	Object       string        `json:"object"`
	OwnedBy      string        `json:"owned_by"`
	Created      int64         `json:"created"`
	DisplayName  string        `json:"display_name,omitempty"`
	Default      bool          `json:"default,omitempty"`
	Members      []string      `json:"members,omitempty"`
	Capabilities *Capabilities `json:"capabilities,omitempty"`
}

// modelCatalog 按「provider 顺序在前、路由组在后」生成 /v1/models 的 data（C11）。
// 目前没有接入公开目录，能力字段全为未知值；后续目录补全资料后再填充（13 MD3/MD6）。
func modelCatalog(snap *config.Snapshot) []modelEntry {
	out := make([]modelEntry, 0)
	for _, p := range snap.Providers {
		for _, model := range p.Models {
			display := model
			if p.DisplayName != "" && p.DisplayName != p.ID {
				display = p.DisplayName + " · " + model
			}
			out = append(out, modelEntry{
				ID:          p.ID + "/" + model,
				Object:      "model",
				OwnedBy:     p.ID,
				Created:     0,
				DisplayName: display,
				Default:     model == p.DefaultModel,
				Capabilities: &Capabilities{
					ReasoningEffort: []string{},
				},
			})
		}
	}
	for _, g := range snap.Routing.Groups {
		out = append(out, modelEntry{
			ID:          "group/" + g.ID,
			Object:      "model",
			OwnedBy:     "group",
			Created:     0,
			DisplayName: g.Name,
			Members:     append([]string(nil), g.Members...),
		})
	}
	return out
}
