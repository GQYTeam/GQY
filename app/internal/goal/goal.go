// Package goal 实现目标状态机、主线/支线与独立评估器（GL1–GL6）。
package goal

import (
	"errors"
	"fmt"
	"time"
)

// State 是 goal 状态（GL1）。
type State string

const (
	StateActive    State = "active"
	StatePaused    State = "paused"
	StateCompleted State = "completed"
	StateBlocked   State = "blocked"
)

// Goal 是挂在会话上的长期目标（GL1）。
type Goal struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	State      State     `json:"state"`
	MaxRounds  int       `json:"max_rounds"`
	CreatedAt  time.Time `json:"created_at"`
	Rounds     int       `json:"rounds"`
	LastReason string    `json:"last_reason,omitempty"`
	Queued     []string  `json:"queued,omitempty"`   // 同会话排队消息（GL3）
	AskHeld    bool      `json:"ask_held,omitempty"` // ask_user 挂起（GL6）
}

// 状态机（GL1）：active ⇄ paused；active → completed/blocked；paused/blocked 可回 active。
func (g *Goal) Transition(next State, reason string) error {
	switch next {
	case StateActive:
		if g.State != StatePaused && g.State != StateBlocked && g.State != "" {
			return fmt.Errorf("goal: 状态 %s 不能转 active", g.State)
		}
	case StatePaused:
		if g.State != StateActive {
			return fmt.Errorf("goal: 状态 %s 不能转 paused", g.State)
		}
	case StateCompleted, StateBlocked:
		if g.State != StateActive && g.State != StatePaused {
			return fmt.Errorf("goal: 状态 %s 不能转 %s", g.State, next)
		}
	default:
		return fmt.Errorf("goal: 未知状态 %q", next)
	}
	g.State = next
	g.LastReason = reason
	return nil
}

// Tick 主线推进一轮；max_rounds 用尽转 blocked（GL1）。
func (g *Goal) Tick() error {
	g.Rounds++
	if g.Rounds >= g.MaxRounds && g.State == StateActive {
		return g.Transition(StateBlocked, "max_rounds 用尽")
	}
	return nil
}

// Queue 把新指令排进同会话支线（GL2/GL3）。
func (g *Goal) Queue(message string) {
	g.Queued = append(g.Queued, message)
}

// Dequeue 当前轮结束后取回一条排队消息。
func (g *Goal) Dequeue() (string, bool) {
	if len(g.Queued) == 0 {
		return "", false
	}
	msg := g.Queued[0]
	g.Queued = g.Queued[1:]
	return msg, true
}

// Event 是 goal 事件（落进会话事件流；httpapi 以 content 承载）。
type Event struct {
	Type    string `json:"type"` // create|pause|resume|complete|block|round|queue|adopt|ask_hold
	GoalID  string `json:"goal_id,omitempty"`
	Title   string `json:"title,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// Rebuild 从 goal 事件重建状态（GL5：resume 从事件流投影重建）。
func Rebuild(events []Event) (*Goal, error) {
	var g *Goal
	for _, ev := range events {
		switch ev.Type {
		case "create":
			g = &Goal{ID: ev.GoalID, Title: ev.Title, State: StateActive, MaxRounds: 20, CreatedAt: time.Now()}
		case "pause":
			if g == nil {
				return nil, errors.New("goal: 缺 create 事件")
			}
			_ = g.Transition(StatePaused, ev.Reason)
		case "resume":
			if g == nil {
				return nil, errors.New("goal: 缺 create 事件")
			}
			_ = g.Transition(StateActive, ev.Reason)
		case "complete":
			if g != nil {
				_ = g.Transition(StateCompleted, ev.Reason)
			}
		case "block":
			if g != nil {
				_ = g.Transition(StateBlocked, ev.Reason)
			}
		case "round":
			if g != nil {
				_ = g.Tick()
			}
		case "queue":
			if g != nil {
				g.Queue(ev.Message)
			}
		case "ask_hold":
			if g != nil {
				g.AskHeld = true
			}
		}
	}
	if g == nil {
		return nil, errors.New("goal: 无 goal 事件")
	}
	return g, nil
}
