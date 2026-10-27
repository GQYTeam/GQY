// 外部工具：provider_usage —— 查询当前供应商的余额/用量。
//
// 属于外部工具（KindExternal）：它依赖具体供应商 API，不属于核心能力。
// 当余额低于阈值时，结果里会带提示，让模型尽快完成工作并汇报。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ProviderUsage 查询 OpenAI 兼容供应商余额（DeepSeek 的 /user/balance）。
type ProviderUsage struct {
	APIBase string
	APIKey  string
	Client  *http.Client
	// LowBalance 是余额阈值（单位与供应商返回一致，如 CNY 元）。低于时提示收尾。
	LowBalance float64
}

func (ProviderUsage) Name() string { return "provider_usage" }
func (ProviderUsage) Kind() Kind   { return KindExternal }
func (ProviderUsage) Description() string {
	return "查询当前供应商的账户余额。余额不足时结果里会带「尽快收尾」提示。用于在长时间任务前确认还有多少额度。"
}
func (ProviderUsage) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

type balanceResp struct {
	IsAvailable  bool `json:"is_available"`
	BalanceInfos []struct {
		Currency        string `json:"currency"`
		TotalBalance    string `json:"total_balance"`
		GrantedBalance  string `json:"granted_balance"`
		ToppedUpBalance string `json:"topped_up_balance"`
	} `json:"balance_infos"`
}

func (p ProviderUsage) Execute(ctx context.Context, args map[string]any) (string, error) {
	if p.APIKey == "" {
		return "", fmt.Errorf("未配置 API key")
	}
	base := strings.TrimRight(p.APIBase, "/")
	// DeepSeek 的余额接口在 base 域名的 /user/balance（非 /v1 路径）
	balanceURL := strings.TrimSuffix(base, "/v1") + "/user/balance"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, balanceURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)

	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("供应商返回 %d", resp.StatusCode)
	}
	var b balanceResp
	if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
		return "", err
	}
	if len(b.BalanceInfos) == 0 {
		return "", fmt.Errorf("余额响应为空")
	}
	info := b.BalanceInfos[0]
	out := fmt.Sprintf("可用: %v, 货币: %s, 总余额: %s, 赠送: %s, 充值: %s",
		b.IsAvailable, info.Currency, info.TotalBalance, info.GrantedBalance, info.ToppedUpBalance)

	// 阈值提示
	var total float64
	fmt.Sscanf(info.TotalBalance, "%f", &total)
	if p.LowBalance > 0 && total < p.LowBalance {
		out += fmt.Sprintf("\n⚠️ 余额 %.2f %s 低于阈值 %.2f，请尽快完成当前工作并向用户汇报剩余额度。", total, info.Currency, p.LowBalance)
	}
	return out, nil
}
