package goal

import (
	"errors"
	"testing"
)

func TestGoalStateMachine(t *testing.T) {
	m := NewManager()
	g := m.Create("补齐文档", 3)
	if g.State != StateActive {
		t.Fatalf("初始状态 = %s", g.State)
	}
	if err := m.Pause(g.ID, "用户暂停"); err != nil || g.State != StatePaused {
		t.Fatalf("pause = %v / %s", err, g.State)
	}
	if err := m.Resume(g.ID, "继续"); err != nil || g.State != StateActive {
		t.Fatalf("resume = %v / %s", err, g.State)
	}
	if err := m.Complete(g.ID, "达成"); err != nil || g.State != StateCompleted {
		t.Fatalf("complete = %v / %s", err, g.State)
	}
	// paused/blocked 可回 active
	g2 := m.Create("目标2", 3)
	_ = m.Block(g2.ID, "不可达成")
	if err := m.Resume(g2.ID, "重开"); err != nil || g2.State != StateActive {
		t.Fatalf("blocked→active = %v / %s", err, g2.State)
	}
}

func TestMainlinePriorityAndQueue(t *testing.T) {
	m := NewManager()
	g := m.Create("主线任务", 5)
	// active 中发新消息 → 排队，主线上下文（Queued 之外）不含新指令
	_ = m.Queue(g.ID, "顺便查一下天气")
	if len(g.Queued) != 1 || g.Queued[0] != "顺便查一下天气" {
		t.Fatalf("queued = %v", g.Queued)
	}
	// 当前轮结束后处理排队消息
	if msg, ok := g.Dequeue(); !ok || msg != "顺便查一下天气" {
		t.Fatalf("dequeue = %q/%v", msg, ok)
	}
	if len(g.Queued) != 0 {
		t.Fatalf("队列应清空: %v", g.Queued)
	}
}

func TestMaxRoundsBlocks(t *testing.T) {
	m := NewManager()
	g := m.Create("多轮目标", 2)
	_ = m.Tick(g.ID)
	_ = m.Tick(g.ID)
	if g.State != StateBlocked || g.LastReason == "" {
		t.Fatalf("max_rounds 用尽应 blocked: %+v", g)
	}
}

func TestForkAndAdopt(t *testing.T) {
	m := NewManager()
	g := m.Create("主线", 5)
	m.Fork(g.ID, "branch-1")
	if err := m.Adopt(g.ID, "branch-1", "支线完成：已整理笔记"); err != nil {
		t.Fatal(err)
	}
	statuses := m.Status()
	if len(statuses) != 1 || len(statuses[0].Branches) != 1 {
		t.Fatalf("status = %+v", statuses)
	}
	if !statuses[0].Branches[0].Adopted || statuses[0].Branches[0].Summary == "" {
		t.Fatalf("支线应已显式采纳: %+v", statuses[0].Branches[0])
	}
	// 未采纳的支线不允许合并（Adopt 不存在报错）
	if err := m.Adopt(g.ID, "no-such-branch", "x"); err == nil {
		t.Fatal("不存在支线应报错")
	}
}

func TestRebuildFromEvents(t *testing.T) {
	events := []Event{
		{Type: "create", GoalID: "g1", Title: "重建目标"},
		{Type: "round"},
		{Type: "queue", Message: "支线消息"},
		{Type: "pause", Reason: "用户暂停"},
		{Type: "resume", Reason: "继续"},
	}
	g, err := Rebuild(events)
	if err != nil {
		t.Fatal(err)
	}
	if g.ID != "g1" || g.State != StateActive || g.Rounds != 1 || len(g.Queued) != 1 {
		t.Fatalf("重建结果 = %+v", g)
	}
}

func TestEvaluatorVerdicts(t *testing.T) {
	eval := Evaluator{Check: func(evidence string) Verdict {
		switch evidence {
		case "done":
			return VerdictAchieved
		case "impossible":
			return VerdictImpossible
		default:
			return VerdictUndecided
		}
	}}
	if got := eval.Evaluate("done"); got != VerdictAchieved {
		t.Fatalf("done → %s", got)
	}
	if got := eval.Evaluate("impossible"); got != VerdictImpossible {
		t.Fatalf("impossible → %s", got)
	}
	if got := eval.Evaluate("x"); got != VerdictUndecided {
		t.Fatalf("x → %s", got)
	}
}

func TestErrorClassification(t *testing.T) {
	if Classify(ErrAuth) || Classify(ErrQuota) || Classify(ErrContext) || Classify(ErrUnavail) {
		t.Fatal("不可恢复错误应为 transient=false")
	}
	if !Classify(ErrTransient) {
		t.Fatal("暂时性错误应为 transient=true")
	}

	// 暂时性错误重试 3 次后 paused
	eval := Evaluator{Check: func(string) Verdict { return VerdictUndecided }}
	attempts := 0
	a := Assess(3, eval, func(attempt int) (string, string, error) {
		attempts++
		return "", ErrTransient, errors.New("transient")
	})
	if !a.Paused || a.Retries != 3 || attempts != 4 {
		t.Fatalf("暂时性重试 = %+v attempts=%d", a, attempts)
	}

	// 不可恢复 → blocked
	a = Assess(3, eval, func(int) (string, string, error) { return "", ErrAuth, errors.New("auth") })
	if !a.Blocked || a.Verdict != VerdictImpossible {
		t.Fatalf("不可恢复 = %+v", a)
	}
}

// TestQueueOnceIdempotentAndTakeQueued 验证支线入队幂等与 FIFO 取走（013）。
func TestQueueOnceIdempotentAndTakeQueued(t *testing.T) {
	m := NewManager()
	g := m.Create("主线目标", 5)

	queued, err := m.QueueOnce(g.ID, "msg-1", "第一条")
	if err != nil || !queued {
		t.Fatalf("首次入队应成功: %v %v", queued, err)
	}
	if queued, _ := m.QueueOnce(g.ID, "msg-1", "第一条"); queued {
		t.Fatal("同一 messageID 重复入队应幂等")
	}
	if queued, _ := m.QueueOnce(g.ID, "msg-2", "第二条"); !queued {
		t.Fatal("新消息应成功入队")
	}
	if err := m.Pause(g.ID, "测试暂停"); err != nil {
		t.Fatal(err)
	}
	if queued, _ := m.QueueOnce(g.ID, "msg-3", "第三条"); queued {
		t.Fatal("paused 的 goal 不应再入队")
	}

	got := m.TakeQueued(g.ID)
	if len(got) != 2 || got[0] != "第一条" || got[1] != "第二条" {
		t.Fatalf("FIFO 取走 = %v", got)
	}
	if again := m.TakeQueued(g.ID); len(again) != 0 {
		t.Fatalf("取走后应为空: %v", again)
	}
}

// TestActiveGoal 验证 Active 返回第一个 active goal（013）。
func TestActiveGoal(t *testing.T) {
	m := NewManager()
	if _, ok := m.Active(); ok {
		t.Fatal("空管理器不应有 active goal")
	}
	g := m.Create("a", 3)
	active, ok := m.Active()
	if !ok || active.ID != g.ID {
		t.Fatalf("Active = %v %v", active, ok)
	}
	if err := m.Complete(g.ID, "完成"); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Active(); ok {
		t.Fatal("完成后不应再有 active goal")
	}
}

// TestRegisterBranchAndAdoptInjection 验证支线登记、采纳与「只注入一次」（017）。
func TestRegisterBranchAndAdoptInjection(t *testing.T) {
	m := NewManager()
	g := m.Create("主线", 5)
	id := m.RegisterBranch(g.ID, "帮我看日志", "日志里有一个超时")
	if id == "" {
		t.Fatal("RegisterBranch 应返回 branch id")
	}
	if got := m.TakeAdoptions(g.ID); len(got) != 0 {
		t.Fatalf("未采纳前不应有注入: %v", got)
	}
	if err := m.Adopt(g.ID, id, ""); err != nil {
		t.Fatal(err)
	}
	first := m.TakeAdoptions(g.ID)
	if len(first) != 1 || first[0] == "" {
		t.Fatalf("采纳后应取到摘要，实际 %v", first)
	}
	if second := m.TakeAdoptions(g.ID); len(second) != 0 {
		t.Fatalf("摘要只应注入一次，实际 %v", second)
	}
	if err := m.Adopt(g.ID, "br-不存在", ""); err == nil {
		t.Fatal("不存在的 branch 采纳应报错")
	}
}
