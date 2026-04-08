package admin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/proxy"
)

// ProbeUsageSnapshot 主动发送最小探针请求刷新账号用量
func (h *Handler) ProbeUsageSnapshot(ctx context.Context, account *auth.Account) error {
	if account == nil {
		return nil
	}

	account.Mu().RLock()
	hasToken := account.AccessToken != ""
	account.Mu().RUnlock()
	if !hasToken {
		return nil
	}

	payload := buildTestPayload(h.store.GetTestModel())
	proxyURL := h.store.NextProxy()
	resp, err := proxy.ExecuteRequest(ctx, account, payload, "", proxyURL, "", nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if usagePct, ok := proxy.ParseCodexUsageHeaders(resp, account); ok {
		h.store.PersistUsageSnapshot(account, usagePct)
	}

	_, _ = io.Copy(io.Discard, resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		h.store.ReportRequestSuccess(account, 0)
		if _, cooldownReason, active := account.GetCooldownSnapshot(); active && cooldownReason == "full_usage" {
			// 允许提前恢复：探针成功后按最新用量快照重判；
			// 仍满用量则继续等待，不满用量则立即退出等待模式。
			if h.store.MarkFullUsageCooldownFromSnapshot(account) {
				return nil
			}
		}
		h.store.ClearCooldown(account)
		return nil
	case http.StatusUnauthorized:
		account.SetLastFailureDetail(http.StatusUnauthorized, "unauthorized", "Unauthorized")
		h.store.ReportRequestFailure(account, "client", 0)
		h.store.MarkCooldown(account, 24*time.Hour, "unauthorized")
		return nil
	case http.StatusTooManyRequests:
		account.SetLastFailureDetail(http.StatusTooManyRequests, "rate_limited", "Rate limited")
		h.store.ReportRequestFailure(account, "client", 0)
		if _, cooldownReason, _ := account.GetCooldownSnapshot(); cooldownReason == "full_usage" {
			if h.store.MarkFullUsageCooldownFromSnapshot(account) {
				return nil
			}
			// 没有可用 reset 时间时，至少再等待一个测活周期
			h.store.MarkCooldown(account, auth.FullUsageProbeInterval, "full_usage")
			return nil
		}
		if h.store.MarkFullUsageCooldownFromSnapshot(account) {
			return nil
		}
		h.store.ExtendRateLimitedCooldown(account, auth.RateLimitedProbeInterval)
		return nil
	default:
		if resp.StatusCode >= 500 {
			h.store.ReportRequestFailure(account, "server", 0)
		} else if resp.StatusCode >= 400 {
			h.store.ReportRequestFailure(account, "client", 0)
		}
		return fmt.Errorf("探针返回状态 %d", resp.StatusCode)
	}
}
