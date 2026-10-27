// 包 judge：规则判定器。运行时错误与模型决策错误分开。
//
// 判定只看约束：是否调用、调用哪个、参数是否命中、最终答案是否包含关键事实，
// 不做「答案完全相等」的比较。
package judge

import (
	"fmt"
	"reflect"
	"strings"

	"gqy/internal/eval/cases"
	"gqy/internal/eval/result"
)

// Judge 对一次运行给出规则判定。strict 为 true 时 schema 合法性进入通过条件。
func Judge(c cases.Case, tr *result.Trace, strict bool) result.Verdict {
	v := result.Verdict{Rules: map[string]bool{}}
	fail := func(rule, format string, args ...any) {
		v.Rules[rule] = false
		v.Reasons = append(v.Reasons, fmt.Sprintf(format, args...))
	}
	pass := func(rule string) { v.Rules[rule] = true }

	// 1. 运行时状态。
	v.Runtime = tr.Status != result.StatusOK
	if !v.Runtime {
		pass("status_ok")
	} else {
		msg := fmt.Sprintf("运行状态 %s", tr.Status)
		if tr.Error != "" {
			msg += ": " + tr.Error
		}
		fail("status_ok", "%s", msg)
	}

	calls := tr.ToolCalls
	// 2. 调用决策。
	if expect := c.Expect.CallExpectation(); expect != nil {
		if *expect {
			if len(calls) > 0 {
				pass("should_call")
			} else {
				fail("should_call", "期望调用工具，但模型没有调用")
			}
		} else {
			if len(calls) == 0 {
				pass("should_call")
			} else {
				fail("should_call", "期望不调用工具，但模型调用了 %d 次", len(calls))
			}
		}
	}
	if c.Expect.MustNotCall {
		if len(calls) == 0 {
			pass("must_not_call")
		} else {
			fail("must_not_call", "禁止调用工具，但模型调用了 %d 次", len(calls))
		}
	}

	// 3. 工具选择。
	if c.Expect.Tool != "" {
		if findCall(calls, c.Expect.Tool) {
			pass("expected_tool")
		} else {
			fail("expected_tool", "期望调用工具 %q，实际调用 %s", c.Expect.Tool, callNames(calls))
		}
	}

	// 4. 参数约束。
	if len(c.Expect.ArgContains) > 0 {
		if argsSatisfied(calls, c.Expect) {
			pass("arg_contains")
		} else {
			fail("arg_contains", "没有一次调用同时满足参数约束 %v", c.Expect.ArgContains)
		}
	}

	// 5. 最终答案关键事实。
	if len(c.Expect.FinalContains) > 0 {
		missing := missingFacts(tr.FinalAnswer, c.Expect.FinalContains)
		if len(missing) == 0 {
			pass("final_contains")
		} else {
			fail("final_contains", "最终答案缺少关键事实: %s", strings.Join(missing, ", "))
		}
	}

	// 6. schema 合法性；只有严格模式才影响通过。
	v.Rules["schema_valid"] = schemaAllValid(calls)
	if strict && !v.Rules["schema_valid"] {
		fail("schema_valid", "严格校验开启，存在参数不合法的工具调用")
	}

	// 所有已判定且属于通过条件的规则都为 true。
	v.Pass = true
	for rule, ok := range v.Rules {
		if !ok && ruleRequired(rule, c, strict) {
			v.Pass = false
		}
	}
	if v.Runtime {
		v.Pass = false
	}
	if v.Reasons == nil {
		v.Reasons = []string{}
	}
	return v
}

func ruleRequired(rule string, c cases.Case, strict bool) bool {
	switch rule {
	case "status_ok":
		return true
	case "should_call":
		return c.Expect.CallExpectation() != nil
	case "must_not_call":
		return c.Expect.MustNotCall
	case "expected_tool":
		return c.Expect.Tool != ""
	case "arg_contains":
		return len(c.Expect.ArgContains) > 0
	case "final_contains":
		return len(c.Expect.FinalContains) > 0
	case "schema_valid":
		return strict
	}
	return false
}

func schemaAllValid(calls []result.ToolCallRecord) bool {
	for _, c := range calls {
		if !c.SchemaValid {
			return false
		}
	}
	return true
}

func findCall(calls []result.ToolCallRecord, name string) bool {
	for _, c := range calls {
		if c.Name == name {
			return true
		}
	}
	return false
}

func callNames(calls []result.ToolCallRecord) string {
	if len(calls) == 0 {
		return "（无）"
	}
	names := make([]string, 0, len(calls))
	for _, c := range calls {
		names = append(names, c.Name)
	}
	return strings.Join(names, ", ")
}

func argsSatisfied(calls []result.ToolCallRecord, expect cases.Expect) bool {
	candidates := calls
	if expect.Tool != "" {
		candidates = nil
		for _, c := range calls {
			if c.Name == expect.Tool {
				candidates = append(candidates, c)
			}
		}
	}
	for _, call := range candidates {
		if len(argMismatches(expect.ArgContains, call.ArgsParsed)) == 0 {
			return true
		}
	}
	return false
}

func argMismatches(expected map[string]any, actual map[string]any) []string {
	var out []string
	for path, want := range expected {
		got, ok := lookupPath(actual, path)
		if !ok {
			out = append(out, fmt.Sprintf("缺少参数 %q", path))
			continue
		}
		if !valueMatches(want, got) {
			out = append(out, fmt.Sprintf("参数 %q 不满足约束", path))
		}
	}
	return out
}

func lookupPath(root map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any = root
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func valueMatches(want, got any) bool {
	switch w := want.(type) {
	case string:
		g, ok := got.(string)
		if !ok {
			return false
		}
		return g == w || strings.Contains(g, w)
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) < len(w) {
			return false
		}
		for _, item := range w {
			found := false
			for _, gv := range g {
				if valueMatches(item, gv) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range w {
			gv, ok := g[k]
			if !ok || !valueMatches(v, gv) {
				return false
			}
		}
		return true
	default:
		return deepEqualJSON(want, got)
	}
}

func deepEqualJSON(a, b any) bool {
	if af, ok := toFloat(a); ok {
		if bf, ok := toFloat(b); ok {
			return af == bf
		}
		return false
	}
	return reflect.DeepEqual(a, b)
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

func missingFacts(final string, facts []string) []string {
	lower := strings.ToLower(final)
	var missing []string
	for _, fact := range facts {
		if !strings.Contains(lower, strings.ToLower(fact)) {
			missing = append(missing, fact)
		}
	}
	return missing
}
