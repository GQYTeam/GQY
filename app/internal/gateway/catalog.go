// Package gateway 负责模型目录、模型解析与后续的路由/转移。
//
// 设计依据：docs/designs/02-网关与路由.md（R1、R2、R6）、
// docs/designs/03-协议翻译.md（C11）。
package gateway

import (
	"gqy/internal/config"
)

// ModelEntry 是 /v1/models 的一项（C11）。
type ModelEntry struct {
	ID          string   `json:"id"`
	Object      string   `json:"object"`
	OwnedBy     string   `json:"owned_by"`
	Created     int64    `json:"created"`
	DisplayName string   `json:"display_name,omitempty"`
	Default     bool     `json:"default,omitempty"`
	Members     []string `json:"members,omitempty"`
}

// Catalog 按「provider 顺序在前、路由组在后」生成模型列表（C11）。
func Catalog(snap *config.Snapshot) []ModelEntry {
	out := make([]ModelEntry, 0)
	for _, p := range snap.Providers {
		for _, model := range p.Models {
			display := model
			if p.DisplayName != "" && p.DisplayName != p.ID {
				display = p.DisplayName + " · " + model
			}
			out = append(out, ModelEntry{
				ID:          p.ID + "/" + model,
				Object:      "model",
				OwnedBy:     p.ID,
				Created:     0,
				DisplayName: display,
				Default:     model == p.DefaultModel,
			})
		}
	}
	for _, g := range snap.Routing.Groups {
		out = append(out, ModelEntry{
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
