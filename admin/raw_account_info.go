package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/proxy"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const whamAccountCheckURL = "https://chatgpt.com/backend-api/wham/accounts/check"

// GetAccountRawInfo 实时请求上游获取账号原始信息，并同步刷新数据库字段
// GET /api/admin/accounts/:id/raw-info
func (h *Handler) GetAccountRawInfo(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "无效的账号 ID")
		return
	}

	account := h.store.FindByID(id)
	if account == nil {
		writeError(c, http.StatusNotFound, "账号不在运行时池中")
		return
	}

	refreshFn := h.refreshAccount
	if refreshFn == nil {
		refreshFn = h.refreshSingleAccount
	}

	account.Mu().RLock()
	hasAccessToken := strings.TrimSpace(account.AccessToken) != ""
	hasRefreshToken := strings.TrimSpace(account.RefreshToken) != ""
	account.Mu().RUnlock()
	needsRefresh := account.NeedsRefresh()

	// 无可用 AT 或即将过期时先刷新，避免上游鉴权失败
	if (!hasAccessToken || needsRefresh) && hasRefreshToken {
		refreshCtx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
		defer cancel()
		if err := refreshFn(refreshCtx, id); err != nil {
			writeError(c, http.StatusInternalServerError, "刷新 Access Token 失败: "+err.Error())
			return
		}
	} else if !hasAccessToken {
		if !hasRefreshToken {
			writeError(c, http.StatusBadRequest, "账号没有可用的 Access Token，且缺少 Refresh Token")
			return
		}
	}

	account.Mu().RLock()
	accessToken := strings.TrimSpace(account.AccessToken)
	accountID := strings.TrimSpace(account.AccountID)
	accountProxy := strings.TrimSpace(account.ProxyURL)
	account.Mu().RUnlock()

	if accessToken == "" {
		writeError(c, http.StatusBadRequest, "账号没有可用的 Access Token，请先刷新")
		return
	}

	// 代理池优先（与测试连接一致），否则回退账号自带代理
	proxyURL := strings.TrimSpace(h.store.NextProxy())
	if proxyURL == "" {
		proxyURL = accountProxy
	}

	reqCtx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	rawBody, statusCode, err := requestWhamAccountCheck(reqCtx, accessToken, accountID, proxyURL)
	if err != nil {
		if len(rawBody) > 0 {
			errCode := strings.TrimSpace(gjson.GetBytes(rawBody, "error.code").String())
			errMsg := strings.TrimSpace(gjson.GetBytes(rawBody, "error.message").String())
			if errMsg == "" {
				errMsg = strings.TrimSpace(gjson.GetBytes(rawBody, "message").String())
			}
			account.SetLastFailureDetail(statusCode, errCode, errMsg)
			switch statusCode {
			case http.StatusUnauthorized:
				h.store.MarkCooldown(account, 24*time.Hour, "unauthorized")
			case http.StatusTooManyRequests:
				if !h.store.MarkFullUsageCooldownFromSnapshot(account) {
					h.store.MarkCooldown(account, auth.RateLimitedProbeInterval, "rate_limited")
				}
			}
		}
		writeError(c, http.StatusBadGateway, err.Error())
		return
	}

	var raw any
	if err := json.Unmarshal(rawBody, &raw); err != nil {
		writeError(c, http.StatusBadGateway, "上游返回了非 JSON 数据")
		return
	}

	refreshedFields, credentialUpdates := extractCredentialUpdatesFromRawInfo(rawBody)
	credentialUpdates["raw_info_refreshed_at"] = time.Now().UTC().Format(time.RFC3339)

	dbCtx, dbCancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer dbCancel()
	if err := h.db.UpdateCredentials(dbCtx, id, credentialUpdates); err != nil {
		writeInternalError(c, fmt.Errorf("写入账号原始信息失败: %w", err))
		return
	}

	applyRawInfoToRuntimeAccount(account, refreshedFields)
	account.ClearLastFailureDetail()
	h.db.InsertAccountEventAsync(id, "raw_info_refreshed", "manual")

	c.JSON(http.StatusOK, gin.H{
		"message":          "账号原始信息获取成功",
		"source":           "upstream",
		"fetched_at":       time.Now().UTC().Format(time.RFC3339),
		"refreshed_fields": refreshedFields,
		"raw":              raw,
	})
}

func requestWhamAccountCheck(ctx context.Context, accessToken, accountID, proxyURL string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, whamAccountCheckURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("创建上游请求失败: %w", err)
	}

	profile := proxy.StableCodexClientProfile()
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Originator", proxy.Originator)
	req.Header.Set("X-Client-Request-Id", uuid.NewString())
	if strings.TrimSpace(profile.UserAgent) != "" {
		req.Header.Set("User-Agent", profile.UserAgent)
	}
	if strings.TrimSpace(profile.Version) != "" {
		req.Header.Set("Version", profile.Version)
	}
	if strings.TrimSpace(accountID) != "" {
		req.Header.Set("ChatGPT-Account-Id", strings.TrimSpace(accountID))
	}

	client := newWhamClient(proxyURL)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("请求上游失败: %w", err)
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("读取上游响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return rawBody, resp.StatusCode, fmt.Errorf("上游返回 %d: %s", resp.StatusCode, truncate(string(rawBody), 500))
	}
	return rawBody, resp.StatusCode, nil
}

func newWhamClient(proxyURL string) *http.Client {
	transport := cloneHTTPTransport()
	transport.Proxy = nil
	transport.ForceAttemptHTTP2 = true

	baseDialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	transport.DialContext = baseDialer.DialContext

	if strings.TrimSpace(proxyURL) != "" {
		if err := auth.ConfigureTransportProxy(transport, proxyURL, baseDialer); err != nil {
			log.Printf("配置账号原始信息请求代理失败，回退直连: %v", err)
			transport.Proxy = nil
			transport.DialContext = baseDialer.DialContext
		}
	}

	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}
}

func cloneHTTPTransport() *http.Transport {
	if base, ok := http.DefaultTransport.(*http.Transport); ok && base != nil {
		return base.Clone()
	}
	return &http.Transport{}
}

func extractCredentialUpdatesFromRawInfo(rawBody []byte) (map[string]string, map[string]interface{}) {
	email := firstNonEmptyJSONValue(rawBody,
		"email",
		"user.email",
		"profile.email",
		"account.email",
		"data.email",
	)
	accountID := firstNonEmptyJSONValue(rawBody,
		"chatgpt_account_id",
		"account_id",
		"account.account_id",
		"account.chatgpt_account_id",
		"data.account_id",
	)
	planTypeRaw := firstNonEmptyJSONValue(rawBody,
		"plan_type",
		"chatgpt_plan_type",
		"planType",
		"account.plan_type",
		"account.chatgpt_plan_type",
		"account.planType",
		"subscription.plan_type",
		"subscription.chatgpt_plan_type",
		"data.plan_type",
	)

	refreshed := make(map[string]string, 3)
	updates := make(map[string]interface{}, 3)

	if strings.TrimSpace(email) != "" {
		refreshed["email"] = strings.TrimSpace(email)
		updates["email"] = strings.TrimSpace(email)
	}
	if strings.TrimSpace(accountID) != "" {
		refreshed["account_id"] = strings.TrimSpace(accountID)
		updates["account_id"] = strings.TrimSpace(accountID)
	}
	if strings.TrimSpace(planTypeRaw) != "" {
		normalizedPlan := auth.NormalizePlanType(planTypeRaw)
		refreshed["plan_type"] = normalizedPlan
		updates["plan_type"] = normalizedPlan
	}

	return refreshed, updates
}

func firstNonEmptyJSONValue(rawBody []byte, paths ...string) string {
	for _, path := range paths {
		value := strings.TrimSpace(gjson.GetBytes(rawBody, path).String())
		if value != "" {
			return value
		}
	}
	return ""
}

func applyRawInfoToRuntimeAccount(account *auth.Account, refreshed map[string]string) {
	account.Mu().Lock()
	defer account.Mu().Unlock()

	if email := strings.TrimSpace(refreshed["email"]); email != "" {
		account.Email = email
	}
	if accountID := strings.TrimSpace(refreshed["account_id"]); accountID != "" {
		account.AccountID = accountID
	}
	if planType := strings.TrimSpace(refreshed["plan_type"]); planType != "" {
		account.PlanType = auth.NormalizePlanType(planType)
	}
}
