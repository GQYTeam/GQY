package goal

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Manager 管理 goal 与支线关系（可观测，G7）。
type Manager struct {
	mu    sync.Mutex
	goals map[string]*Goal
	// branches 记录分叉会话与父 goal 的合并关系（GL3/GL4）。
	branches map[string][]Branch
	// queued 记录已入队的消息 id，保证重复入队幂等（013）。
	queued map[string]bool
}

// Branch 是一次分叉支线（GL3）。
type Branch struct {
	BranchID string `json:"branch_id"`
	ParentID string `json:"parent_id"`
	Summary  string `json:"summary,omitempty"`
	Adopted  bool   `json:"adopted"`
	// Injected 表示已采纳摘要已注入主线上下文（只注入一次，017）。
	Injected bool `json:"injected,omitempty"`
}

// NewManager 构造管理器。
func NewManager() *Manager {
	return &Manager{goals: make(map[string]*Goal), branches: make(map[string][]Branch), queued: make(map[string]bool)}
}

// Create 创建 active goal（GL1）。
func (m *Manager) Create(title string, maxRounds int) *Goal {
	if maxRounds <= 0 {
		maxRounds = 20
	}
	g := &Goal{ID: fmt.Sprintf("goal-%d", time.Now().UnixNano()), Title: title, State: StateActive, MaxRounds: maxRounds, CreatedAt: time.Now()}
	m.mu.Lock()
	m.goals[g.ID] = g
	m.mu.Unlock()
	return g
}

// Get 返回 goal。
func (m *Manager) Get(id string) (*Goal, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.goals[id]
	return g, ok
}

// Pause 冻结主线（GL5）。
func (m *Manager) Pause(id, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.goals[id]
	if !ok {
		return fmt.Errorf("goal: %s 不存在", id)
	}
	return g.Transition(StatePaused, reason)
}

// Resume 恢复主线（GL5）。
func (m *Manager) Resume(id, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.goals[id]
	if !ok {
		return fmt.Errorf("goal: %s 不存在", id)
	}
	return g.Transition(StateActive, reason)
}

// Complete 标记达成。
func (m *Manager) Complete(id, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.goals[id]
	if !ok {
		return fmt.Errorf("goal: %s 不存在", id)
	}
	return g.Transition(StateCompleted, reason)
}

// Block 标记不可达成。
func (m *Manager) Block(id, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.goals[id]
	if !ok {
		return fmt.Errorf("goal: %s 不存在", id)
	}
	return g.Transition(StateBlocked, reason)
}

// Tick 主线推进一轮（max_rounds 用尽自动 blocked）。
func (m *Manager) Tick(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.goals[id]
	if !ok {
		return fmt.Errorf("goal: %s 不存在", id)
	}
	return g.Tick()
}

// Queue 新指令进同会话排队（GL2）。
func (m *Manager) Queue(id, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.goals[id]
	if !ok {
		return fmt.Errorf("goal: %s 不存在", id)
	}
	g.Queue(message)
	return nil
}

// Fork 记录一次分叉支线（GL3）。
func (m *Manager) Fork(parentID, branchID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.branches[parentID] = append(m.branches[parentID], Branch{BranchID: branchID, ParentID: parentID})
}

// RegisterBranch 登记一条已执行的支线，返回 branch id（017）。
// 排队支线执行完由 sink 调用，后续才能被显式采纳。
func (m *Manager) RegisterBranch(goalID, message, output string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.branches[goalID]
	id := fmt.Sprintf("br-%d", len(list)+1)
	summary := message
	if output != "" {
		summary = message + " → " + output
	}
	m.branches[goalID] = append(list, Branch{BranchID: id, ParentID: goalID, Summary: summary})
	return id
}

// TakeAdoptions 取走「已采纳但未注入」的支线摘要，并标记已注入（017）。
// 主线在轮边界把这些摘要注入上下文；每条只注入一次。
func (m *Manager) TakeAdoptions(goalID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.branches[goalID]
	var out []string
	for i := range list {
		if list[i].Adopted && !list[i].Injected {
			out = append(out, list[i].Summary)
			list[i].Injected = true
		}
	}
	return out
}

// Adopt 显式采纳支线摘要并合并进主线（GL4）。
func (m *Manager) Adopt(parentID, branchID, summary string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.branches[parentID]
	for i := range list {
		if list[i].BranchID == branchID {
			// 采纳时可补充/覆盖摘要；空摘要保留登记时的摘要（017）。
			if summary != "" {
				list[i].Summary = summary
			}
			list[i].Adopted = true
			return nil
		}
	}
	return fmt.Errorf("goal: 支线 %s 不存在", branchID)
}

// Status 返回全部 goal 与支线（G7 可观测）。
func (m *Manager) Status() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.goals))
	for id, g := range m.goals {
		out = append(out, Status{Goal: *g, Branches: m.branches[id]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Status 是状态接口的一项。
type Status struct {
	Goal
	Branches []Branch `json:"branches"`
}

// Active 返回第一个 active 的 goal（按 id 排序，稳定可测）（013）。
func (m *Manager) Active() (*Goal, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.goals))
	for id := range m.goals {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if g := m.goals[id]; g.State == StateActive {
			return g, true
		}
	}
	return nil, false
}

// QueueOnce 幂等入队：同一 messageID 只入队一次；goal 非 active 时不入队（013）。
func (m *Manager) QueueOnce(id, messageID, message string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.goals[id]
	if !ok {
		return false, fmt.Errorf("goal: %q 不存在", id)
	}
	if g.State != StateActive {
		return false, nil
	}
	key := id + "\x00" + messageID
	if m.queued[key] {
		return false, nil
	}
	m.queued[key] = true
	g.Queue(message)
	return true, nil
}

// TakeQueued 取走某 goal 的全部排队消息（FIFO）（013）。
func (m *Manager) TakeQueued(id string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.goals[id]
	if !ok || len(g.Queued) == 0 {
		return nil
	}
	out := append([]string(nil), g.Queued...)
	g.Queued = nil
	return out
}
