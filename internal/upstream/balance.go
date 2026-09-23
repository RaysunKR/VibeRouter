package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/viberouter/viberouter/internal/config"
)

// BalanceResult is the normalized outcome of a balance/quota probe.
type BalanceResult struct {
	OK        bool    `json:"ok"`
	Message   string  `json:"message,omitempty"`
	Remaining float64 `json:"remaining"`        // cash remaining or quota percent remaining (0-100)
	Unit      string  `json:"unit"`             // CNY | USD | percent
	Plan      string  `json:"plan,omitempty"`
}

// clientLangKey marks whether the client prefers Chinese for UI-facing messages.
type clientLangCtx int

const clientLangKey clientLangCtx = 0

// WithClientLang annotates ctx with the client's UI language, parsed from an
// Accept-Language header value. Msg reads it.
func WithClientLang(ctx context.Context, acceptLanguage string) context.Context {
	return context.WithValue(ctx, clientLangKey, strings.Contains(strings.ToLower(acceptLanguage), "zh"))
}

// Msg returns zh when the client prefers Chinese (or no preference is known),
// otherwise en. Used for the handful of backend strings shown in the admin UI.
func Msg(ctx context.Context, zh, en string) string {
	if v, ok := ctx.Value(clientLangKey).(bool); ok && !v {
		return en
	}
	return zh
}

// QueryBalance probes a provider's balance/quota per its configured type.
func QueryBalance(ctx context.Context, p *config.Provider) BalanceResult {
	if p.Balance.Type == "" || p.Balance.Type == "none" {
		return BalanceResult{
			OK:      false,
			Message: Msg(ctx, "该供应商不支持余额查询", "This provider does not support balance queries"),
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	base := strings.TrimRight(p.BaseURL, "/")
	if p.Balance.URL != "" {
		base = strings.TrimRight(p.Balance.URL, "/")
	}
	switch p.Balance.Type {
	case "deepseek":
		return probeJSON(ctx, base+"/user/balance", bearer(p.APIKey), func(v any) BalanceResult {
			infos := dig(v, "balance_infos")
			arr, _ := infos.([]any)
			if len(arr) == 0 {
				return BalanceResult{OK: false, Message: "no balance_infos"}
			}
			total := num(dig(arr[0], "total_balance"))
			cur, _ := dig(arr[0], "currency").(string)
			return BalanceResult{OK: true, Remaining: total, Unit: cur}
		})
	case "stepfun":
		return probeJSON(ctx, base+"/v1/accounts", bearer(p.APIKey), func(v any) BalanceResult {
			return BalanceResult{OK: true, Remaining: num(dig(v, "balance")), Unit: "CNY"}
		})
	case "siliconflow":
		return probeJSON(ctx, base+"/v1/user/info", bearer(p.APIKey), func(v any) BalanceResult {
			unit := "CNY"
			if strings.Contains(base, "siliconflow.com") {
				unit = "USD"
			}
			return BalanceResult{OK: true, Remaining: num(dig(v, "data", "totalBalance")), Unit: unit}
		})
	case "openrouter":
		return probeJSON(ctx, base+"/v1/credits", bearer(p.APIKey), func(v any) BalanceResult {
			rem := num(dig(v, "data", "total_credits")) - num(dig(v, "data", "total_usage"))
			return BalanceResult{OK: true, Remaining: rem, Unit: "USD"}
		})
	case "novita":
		return probeJSON(ctx, base+"/v3/user/balance", bearer(p.APIKey), func(v any) BalanceResult {
			// availableBalance is in 0.0001 USD units
			return BalanceResult{OK: true, Remaining: num(dig(v, "availableBalance")) / 10000, Unit: "USD"}
		})
	case "zhipu":
		// raw key without Bearer prefix, per cc-switch
		return probeJSON(ctx, quotaBase(base)+"/api/monitor/usage/quota/limit",
			map[string]string{"Authorization": p.APIKey, "Accept-Language": "en-US,en"},
			func(v any) BalanceResult {
				if ok, _ := dig(v, "success").(bool); !ok {
					msg, _ := dig(v, "msg").(string)
					return BalanceResult{OK: false, Message: msg}
				}
				plan, _ := dig(v, "data", "level").(string)
				limits, _ := dig(v, "data", "limits").([]any)
				for _, l := range limits {
					if t, _ := dig(l, "type").(string); t == "TOKENS_LIMIT" || t == "CREDIT_LIMIT" {
						usedPct := num(dig(l, "percentage"))
						return BalanceResult{OK: true, Remaining: 100 - usedPct, Unit: "percent", Plan: plan}
					}
				}
				return BalanceResult{OK: false, Message: "no TOKENS_LIMIT in response"}
			})
	case "kimi_coding":
		return probeJSON(ctx, base+"/v1/usages", bearer(p.APIKey), func(v any) BalanceResult {
			lim := num(dig(v, "usage", "limit"))
			rem := num(dig(v, "usage", "remaining"))
			if lim > 0 {
				return BalanceResult{OK: true, Remaining: rem / lim * 100, Unit: "percent", Plan: "weekly"}
			}
			return BalanceResult{OK: false, Message: "no usage limits in response"}
		})
	case "minimax":
		return probeJSON(ctx, base+"/v1/api/openplatform/coding_plan/remains", bearer(p.APIKey), func(v any) BalanceResult {
			if code := num(dig(v, "base_resp", "status_code")); code != 0 {
				msg, _ := dig(v, "base_resp", "status_msg").(string)
				return BalanceResult{OK: false, Message: msg}
			}
			items, _ := dig(v, "model_remains").([]any)
			for _, it := range items {
				if name, _ := dig(it, "model_name").(string); name == "general" {
					remPct := num(dig(it, "current_interval_remaining_percent"))
					return BalanceResult{OK: true, Remaining: remPct, Unit: "percent", Plan: "coding_plan"}
				}
			}
			return BalanceResult{OK: false, Message: "no general model_remains"}
		})
	}
	return BalanceResult{OK: false, Message: "unknown balance type: " + p.Balance.Type}
}

// quotaBase strips API path suffixes so quota endpoints hit the host root,
// e.g. https://open.bigmodel.cn/api/anthropic -> https://open.bigmodel.cn.
func quotaBase(base string) string {
	for _, suffix := range []string{"/api/anthropic", "/api/paas/v4", "/v1"} {
		base = strings.TrimSuffix(base, suffix)
	}
	return base
}

func bearer(key string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + key}
}

func probeJSON(ctx context.Context, url string, headers map[string]string, parse func(any) BalanceResult) BalanceResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return BalanceResult{OK: false, Message: err.Error()}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := Client.Do(req)
	if err != nil {
		return BalanceResult{OK: false, Message: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return BalanceResult{OK: false, Message: err.Error()}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return BalanceResult{
			OK: false,
			Message: Msg(ctx,
				fmt.Sprintf("凭证无效或过期 (HTTP %d)", resp.StatusCode),
				fmt.Sprintf("Invalid or expired credentials (HTTP %d)", resp.StatusCode)),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return BalanceResult{OK: false, Message: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))}
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return BalanceResult{OK: false, Message: "invalid JSON: " + err.Error()}
	}
	return parse(v)
}

// dig walks nested maps by key path.
func dig(v any, path ...string) any {
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[p]
	}
	return v
}

// num coerces JSON numbers and numeric strings to float64.
func num(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	case json.Number:
		f, _ := t.Float64()
		return f
	}
	return 0
}
