package goal

// Verdict 是评估器三态（走查借鉴：未达成 / 达成 / 不可能）。
type Verdict string

const (
	VerdictUndecided  Verdict = "undecided"
	VerdictAchieved   Verdict = "achieved"
	VerdictImpossible Verdict = "impossible"
)

// Evaluator 独立评估器：不跑工具，只看对话证据（走查借鉴）。
type Evaluator struct {
	// Check 对证据做可验证判定；默认恒未达成。
	Check func(evidence string) Verdict
}

// Evaluate 返回三态判定。
func (e Evaluator) Evaluate(evidence string) Verdict {
	if e.Check == nil {
		return VerdictUndecided
	}
	return e.Check(evidence)
}

// 错误分类（走查借鉴）：不可恢复 → blocked；暂时性 → 重试 N 次后 paused。
const (
	ErrAuth      = "auth_failed"
	ErrQuota     = "quota_exhausted"
	ErrContext   = "context_overflow"
	ErrUnavail   = "model_unavailable"
	ErrTransient = "transient"
)

// Classify 把错误归为可恢复/不可恢复。
func Classify(errCode string) (transient bool) {
	switch errCode {
	case ErrAuth, ErrQuota, ErrContext, ErrUnavail:
		return false
	default:
		return true
	}
}

// Assess 跑一轮评估并处理错误：不可恢复→blocked；暂时性→重试 N 次后 paused。
// doRound 执行评估轮并返回证据；返回错误按 Classify 分类。
func Assess(retries int, eval Evaluator, doRound func(attempt int) (evidence string, errCode string, err error)) Assessment {
	if retries <= 0 {
		retries = 3
	}
	for attempt := 0; attempt <= retries; attempt++ {
		evidence, code, err := doRound(attempt)
		if err == nil {
			return Assessment{Verdict: eval.Evaluate(evidence)}
		}
		if !Classify(code) {
			return Assessment{Verdict: VerdictImpossible, Blocked: true, Reason: "不可恢复错误: " + code}
		}
		if attempt == retries {
			return Assessment{Verdict: VerdictUndecided, Paused: true, Retries: retries, Reason: "暂时性错误重试耗尽: " + code}
		}
	}
	return Assessment{Verdict: VerdictUndecided}
}

// Assessment 是一次评估结果。
type Assessment struct {
	Verdict Verdict `json:"verdict"`
	Blocked bool    `json:"blocked,omitempty"`
	Paused  bool    `json:"paused,omitempty"`
	Retries int     `json:"retries,omitempty"`
	Reason  string  `json:"reason,omitempty"`
}
