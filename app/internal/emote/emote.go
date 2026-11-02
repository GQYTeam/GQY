// Package emote 在 /api/chat 回复完成后按语境向表情包插件要一张表情（task-13）。
//
// 设计依据：表情包插件 emote-pack-default v1.2.0 的 suggest 动作契约——
// result.content 是 {"decision":"emote"|"none","none_score":0.02,"predictions":[...]}。
// 插件设置 auto_emote（默认 true）/ min_score（默认 0，clamp [0,1]）在这里读取；
// 插件未就绪、关闭、调用失败、超时（3s）、JSON 解析失败都静默跳过，不影响回复流。
package emote

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"strings"
	"time"

	"gqy/internal/plugin"
	"gqy/internal/session"
)

const (
	// PluginID 是默认表情包插件的 id。
	PluginID = "emote-pack-default"
	// suggestAction 是按语境挑表情的动作名。
	suggestAction = "suggest"
	// suggestLimit 是候选条数（插件契约 1–5，默认 3），只要 top1。
	suggestLimit = 3
	// maxContextRunes 是语境上限（插件契约 1–2000 字符）。
	maxContextRunes = 2000
	// pickTimeout 是单次挑表情的超时：超时静默跳过，不拖慢回复流。
	pickTimeout = 3 * time.Second
)

// Picker 按语境挑一张表情；Plugins 为 nil 时恒返回 false。
type Picker struct {
	Plugins *plugin.Manager
	Logger  *slog.Logger
}

// suggestResult 是 suggest 动作 result.content 的 JSON 形状（插件 1.2.0）。
type suggestResult struct {
	Decision    string       `json:"decision"`
	NoneScore   float64      `json:"none_score"`
	Predictions []prediction `json:"predictions"`
}

type prediction struct {
	Name  string  `json:"name"`
	Label string  `json:"label"`
	Score float64 `json:"score"`
	File  string  `json:"file"`
}

// Pick 返回本次回复应随附的表情；false 表示不发。
// 插件未加载/未就绪、auto_emote 关闭、调用失败或超时、JSON 解析失败、
// decision=none、预测为空、top1 置信度低于 min_score 时都返回 false，只记日志。
func (p *Picker) Pick(ctx context.Context, contextText string) (session.Emote, bool) {
	var zero session.Emote
	if p == nil || p.Plugins == nil {
		return zero, false
	}
	text := strings.TrimSpace(contextText)
	if text == "" {
		// 语境为空（或全空白）算「过短」：不浪费一次插件调用。
		p.logger().Debug("emote: 语境为空，跳过")
		return zero, false
	}
	if runes := []rune(text); len(runes) > maxContextRunes {
		text = string(runes[len(runes)-maxContextRunes:])
	}
	if !p.ready() {
		p.logger().Debug("emote: 插件未就绪，跳过")
		return zero, false
	}
	settings, err := p.Plugins.EffectiveSettings(PluginID)
	if err != nil {
		p.logger().Debug("emote: 读取插件设置失败，跳过", "error", err)
		return zero, false
	}
	if !autoEmote(settings) {
		p.logger().Debug("emote: 自动发表情已关闭，跳过")
		return zero, false
	}
	minScore := minScore(settings)

	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, pickTimeout)
	defer cancel()
	content, err := p.Plugins.Call(callCtx, PluginID, suggestAction, map[string]any{
		"context": text,
		"limit":   suggestLimit,
	})
	if err != nil {
		// 未就绪 / 超时 / 进程失败：只记日志，不影响回复流。
		p.logger().Debug("emote: suggest 调用失败，跳过", "error", err)
		return zero, false
	}

	var out suggestResult
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		p.logger().Warn("emote: suggest 返回内容不是 JSON，跳过", "error", err)
		return zero, false
	}
	if out.Decision != "emote" || len(out.Predictions) == 0 {
		p.logger().Debug("emote: 模型判断不适合发表情", "decision", out.Decision)
		return zero, false
	}
	// top1 按分数取最高（插件按分数降序返回，这里不依赖顺序）。
	top := out.Predictions[0]
	for _, pred := range out.Predictions[1:] {
		if pred.Score > top.Score {
			top = pred
		}
	}
	if top.Name == "" {
		p.logger().Warn("emote: 候选缺少 name，跳过")
		return zero, false
	}
	if math.IsNaN(top.Score) || math.IsInf(top.Score, 0) || math.IsNaN(out.NoneScore) || math.IsInf(out.NoneScore, 0) {
		p.logger().Warn("emote: 分数非法，跳过")
		return zero, false
	}
	if top.Score < minScore {
		p.logger().Debug("emote: 置信度低于阈值，跳过", "score", top.Score, "min_score", minScore)
		return zero, false
	}
	return session.Emote{
		Name:      top.Name,
		Label:     top.Label,
		File:      top.File,
		Score:     top.Score,
		NoneScore: out.NoneScore,
	}, true
}

// logger 返回可用日志器（未注入时用默认）。
func (p *Picker) logger() *slog.Logger {
	if p != nil && p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

// ready 报告表情包插件是否 state=ready（Status() 是插件运行时的公开状态源）。
func (p *Picker) ready() bool {
	for _, st := range p.Plugins.Status() {
		if st.ID == PluginID {
			return st.State == string(plugin.StateReady)
		}
	}
	return false
}

// autoEmote 读 auto_emote：缺省 true，只有显式 false 才关闭。
func autoEmote(settings map[string]any) bool {
	v, ok := settings["auto_emote"]
	if !ok {
		return true
	}
	enabled, ok := v.(bool)
	if !ok {
		return true
	}
	return enabled
}

// minScore 读 min_score：缺省 0，并 clamp 到 [0,1]。
func minScore(settings map[string]any) float64 {
	score, ok := toFloat(settings["min_score"])
	if !ok {
		return 0
	}
	return math.Min(1, math.Max(0, score))
}

// toFloat 把设置/JSON 里的数字统一成 float64；NaN/Inf 视为非法。
func toFloat(v any) (float64, bool) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case float32:
		f = float64(n)
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	case json.Number:
		parsed, err := n.Float64()
		if err != nil {
			return 0, false
		}
		f = parsed
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}
