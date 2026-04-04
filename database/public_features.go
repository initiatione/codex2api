package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// PublicAPIKeyAuth 公开密钥鉴权上下文
type PublicAPIKeyAuth struct {
	ID         int64
	Name       string
	Key        string
	Source     string
	Balance    float64
	CreatedIP  string
	LastUsedIP string
}

// PublicAPIKeyOverview 公开密钥概览信息
type PublicAPIKeyOverview struct {
	ID                int64
	Name              string
	Key               string
	Source            string
	BalanceUSD        float64
	CreatedIP         string
	LastUsedIP        string
	CreatedAt         time.Time
	LastUsedAt        sql.NullTime
	BoundAccountCount int64
	SettledAmountUSD  float64
}

// PublicSettlementBindInput 账号绑定公开密钥时的结算初始化参数
type PublicSettlementBindInput struct {
	AccountID            int64
	PublicAPIKeyID       int64
	BaselineUsagePercent float64
	InitialAmountUSD     float64
	FullAmountUSD        float64
}

// PublicSettlementResult 单次结算结果
type PublicSettlementResult struct {
	AccountID      int64   `json:"account_id"`
	PublicAPIKeyID int64   `json:"public_api_key_id"`
	UsagePercent   float64 `json:"usage_percent"`
	DeltaUSD       float64 `json:"delta_usd"`
	SettledAmount  float64 `json:"settled_amount_usd"`
	BalanceUSD     float64 `json:"balance_usd"`
	Finalized      bool    `json:"finalized"`
}

// RedeemCodeSummary 兑换码面额汇总
type RedeemCodeSummary struct {
	AmountUSD float64 `json:"amount_usd"`
	Count     int64   `json:"count"`
}

// RedeemCodeResult 兑换结果
type RedeemCodeResult struct {
	PublicAPIKeyID   int64   `json:"public_api_key_id"`
	Code             string  `json:"code"`
	RequestedAmount  float64 `json:"requested_amount_usd"`
	RedeemedAmount   float64 `json:"redeemed_amount_usd"`
	RemainingBalance float64 `json:"remaining_balance_usd"`
}

// RedeemFailure 兑换失败原因
type RedeemFailure struct {
	Kind            string  `json:"kind"`
	Message         string  `json:"message"`
	RequestedAmount float64 `json:"requested_amount_usd,omitempty"`
	MatchedAmount   float64 `json:"matched_amount_usd,omitempty"`
	BalanceUSD      float64 `json:"balance_usd,omitempty"`
}

func (e *RedeemFailure) Error() string {
	if e == nil {
		return "redeem failed"
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return "redeem failed"
}

func roundUSD(value float64) float64 {
	return math.Round(value*10000) / 10000
}

func clampPercent(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func settlementAmountForUsage(usagePct, baseline, initial, full float64) float64 {
	denominator := 100 - baseline
	if denominator <= 0 {
		denominator = 1
	}
	progress := (usagePct - baseline) / denominator
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	amount := roundUSD(initial + (full-initial)*progress)
	if amount < initial {
		amount = initial
	}
	if amount > full {
		amount = full
	}
	return amount
}

func (db *DB) nowExpr() string {
	if db.isSQLite() {
		return "CURRENT_TIMESTAMP"
	}
	return "NOW()"
}

// GetPublicAPIKeyByValue 按明文 key 查公开密钥
func (db *DB) GetPublicAPIKeyByValue(ctx context.Context, key string) (*PublicAPIKeyAuth, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, sql.ErrNoRows
	}

	row := &PublicAPIKeyAuth{}
	err := db.conn.QueryRowContext(ctx, `
		SELECT id, name, key, COALESCE(source, 'admin'), COALESCE(balance_usd, 0), COALESCE(created_ip, ''), COALESCE(last_used_ip, '')
		FROM public_api_keys
		WHERE key = $1
	`, key).Scan(&row.ID, &row.Name, &row.Key, &row.Source, &row.Balance, &row.CreatedIP, &row.LastUsedIP)
	if err != nil {
		return nil, err
	}
	return row, nil
}

// GetPublicAPIKeyOverview 获取公开密钥概览（含绑定账号与已结算金额）
func (db *DB) GetPublicAPIKeyOverview(ctx context.Context, id int64) (*PublicAPIKeyOverview, error) {
	if id == 0 {
		return nil, sql.ErrNoRows
	}

	query := `
		SELECT
			k.id,
			k.name,
			k.key,
			COALESCE(k.source, 'admin') AS source,
			COALESCE(k.balance_usd, 0) AS balance_usd,
			COALESCE(k.created_ip, '') AS created_ip,
			COALESCE(k.last_used_ip, '') AS last_used_ip,
			k.created_at,
			k.last_used_at,
			COALESCE(stats.bound_account_count, 0) AS bound_account_count,
			COALESCE(stats.settled_amount_usd, 0) AS settled_amount_usd
		FROM public_api_keys k
		LEFT JOIN (
			SELECT
				s.public_api_key_id,
				COUNT(*) AS bound_account_count,
				COALESCE(SUM(s.settled_amount_usd), 0) AS settled_amount_usd
			FROM public_account_settlements s
			JOIN accounts a ON a.id = s.account_id
			WHERE COALESCE(a.error_message, '') <> 'deleted'
			GROUP BY s.public_api_key_id
		) stats ON stats.public_api_key_id = k.id
		WHERE k.id = $1
	`

	row := &PublicAPIKeyOverview{}
	var (
		createdAtRaw  interface{}
		lastUsedAtRaw interface{}
	)
	err := db.conn.QueryRowContext(ctx, query, id).Scan(
		&row.ID,
		&row.Name,
		&row.Key,
		&row.Source,
		&row.BalanceUSD,
		&row.CreatedIP,
		&row.LastUsedIP,
		&createdAtRaw,
		&lastUsedAtRaw,
		&row.BoundAccountCount,
		&row.SettledAmountUSD,
	)
	if err != nil {
		return nil, err
	}

	row.CreatedAt, err = parseDBTimeValue(createdAtRaw)
	if err != nil {
		return nil, err
	}
	row.LastUsedAt, err = parseDBNullTimeValue(lastUsedAtRaw)
	if err != nil {
		return nil, err
	}
	row.BalanceUSD = roundUSD(row.BalanceUSD)
	row.SettledAmountUSD = roundUSD(row.SettledAmountUSD)
	return row, nil
}

// TouchPublicAPIKeyUsage 更新公开密钥最近调用来源
func (db *DB) TouchPublicAPIKeyUsage(ctx context.Context, id int64, lastUsedIP string) error {
	if id == 0 {
		return nil
	}
	query := fmt.Sprintf(`
		UPDATE public_api_keys
		SET last_used_ip = $1, last_used_at = %s, updated_at = %s
		WHERE id = $2
	`, db.nowExpr(), db.nowExpr())
	_, err := db.conn.ExecContext(ctx, query, strings.TrimSpace(lastUsedIP), id)
	return err
}

// CountRecentGeneratedPublicKeysByIP 统计某 IP 最近生成的公开 key 数
func (db *DB) CountRecentGeneratedPublicKeysByIP(ctx context.Context, ip string, since time.Time) (int64, error) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return 0, nil
	}
	var count int64
	query := `
		SELECT COUNT(*)
		FROM public_api_keys
		WHERE COALESCE(source, '') = 'public_generate'
		  AND COALESCE(created_ip, '') = $1
		  AND created_at >= $2
	`
	argSince := interface{}(since)
	if db.isSQLite() {
		query = `
			SELECT COUNT(*)
			FROM public_api_keys
			WHERE COALESCE(source, '') = 'public_generate'
			  AND COALESCE(created_ip, '') = $1
			  AND datetime(created_at) >= datetime($2)
		`
		argSince = since.UTC().Format("2006-01-02 15:04:05")
	}
	err := db.conn.QueryRowContext(ctx, query, ip, argSince).Scan(&count)
	return count, err
}

// BindAccountToPublicKey 将账号绑定到公开密钥，并发放初始额度
func (db *DB) BindAccountToPublicKey(ctx context.Context, in PublicSettlementBindInput) error {
	if in.AccountID == 0 || in.PublicAPIKeyID == 0 {
		return nil
	}

	initial := roundUSD(in.InitialAmountUSD)
	full := roundUSD(in.FullAmountUSD)
	if full < initial {
		full = initial
	}
	baseline := clampPercent(in.BaselineUsagePercent)

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	updateAccount := fmt.Sprintf(`UPDATE accounts SET public_api_key_id = $1, updated_at = %s WHERE id = $2`, db.nowExpr())
	if _, err = tx.ExecContext(ctx, updateAccount, in.PublicAPIKeyID, in.AccountID); err != nil {
		return err
	}

	insertSettlement := fmt.Sprintf(`
		INSERT INTO public_account_settlements (
			account_id, public_api_key_id, baseline_usage_percent, last_usage_percent,
			settled_amount_usd, initial_amount_usd, full_amount_usd, finalized, created_at, updated_at
		)
		VALUES ($1, $2, $3, $3, $4, $4, $5, false, %s, %s)
		ON CONFLICT (account_id) DO NOTHING
	`, db.nowExpr(), db.nowExpr())
	res, err := tx.ExecContext(ctx, insertSettlement, in.AccountID, in.PublicAPIKeyID, baseline, initial, full)
	if err != nil {
		return err
	}

	affected, _ := res.RowsAffected()
	if affected > 0 && initial > 0 {
		updateBalance := fmt.Sprintf(`
			UPDATE public_api_keys
			SET balance_usd = COALESCE(balance_usd, 0) + $1, updated_at = %s
			WHERE id = $2
		`, db.nowExpr())
		if _, err = tx.ExecContext(ctx, updateBalance, initial, in.PublicAPIKeyID); err != nil {
			return err
		}

		var balanceAfter float64
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(balance_usd, 0) FROM public_api_keys WHERE id = $1`, in.PublicAPIKeyID).Scan(&balanceAfter); err != nil {
			return err
		}
		insertLog := fmt.Sprintf(`
			INSERT INTO public_balance_logs (public_api_key_id, account_id, entry_type, delta_usd, balance_after_usd, note, created_at)
			VALUES ($1, $2, 'upload_initial', $3, $4, $5, %s)
		`, db.nowExpr())
		if _, err = tx.ExecContext(ctx, insertLog, in.PublicAPIKeyID, in.AccountID, initial, balanceAfter, "account_upload_initial_credit"); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// SettlePublicAccountUsage 按账号实时用量百分比进行增量结算（兼容旧调用）。
func (db *DB) SettlePublicAccountUsage(ctx context.Context, accountID int64, usagePercent float64) (*PublicSettlementResult, error) {
	return db.SettlePublicAccountUsageWithRequestDelta(ctx, accountID, 0, false, usagePercent)
}

// SettlePublicAccountUsageWithRequestDelta 按单次请求的前后用量差值进行结算。
// requestBeforeValid=true 时，仅对 [request_before, request_after] 区间进行计费；
// 这可避免把请求开始前已发生的外部消费计入本次结算。
func (db *DB) SettlePublicAccountUsageWithRequestDelta(
	ctx context.Context,
	accountID int64,
	requestBeforePercent float64,
	requestBeforeValid bool,
	requestAfterPercent float64,
) (*PublicSettlementResult, error) {
	if accountID == 0 {
		return nil, nil
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	selectQuery := `
		SELECT public_api_key_id, baseline_usage_percent, last_usage_percent,
		       settled_amount_usd, initial_amount_usd, full_amount_usd, finalized
		FROM public_account_settlements
		WHERE account_id = $1
	`
	if !db.isSQLite() {
		selectQuery += ` FOR UPDATE`
	}

	var (
		publicKeyID int64
		baseline    float64
		lastUsage   float64
		settled     float64
		initial     float64
		full        float64
		finalized   bool
	)
	err = tx.QueryRowContext(ctx, selectQuery, accountID).Scan(&publicKeyID, &baseline, &lastUsage, &settled, &initial, &full, &finalized)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	baseline = clampPercent(baseline)
	lastUsage = clampPercent(lastUsage)

	requestAfter := clampPercent(requestAfterPercent)
	requestBefore := lastUsage
	if requestBeforeValid {
		requestBefore = clampPercent(requestBeforePercent)
	}
	if requestBefore < baseline {
		requestBefore = baseline
	}
	if requestAfter < requestBefore {
		requestAfter = requestBefore
	}

	initial = roundUSD(initial)
	full = roundUSD(full)
	if full < initial {
		full = initial
	}
	settled = roundUSD(settled)
	if settled < initial {
		settled = initial
	}

	// 只结算本请求窗口内增长；如果请求前用量已经高于 last_usage_percent，
	// 视为外部消费，推进状态但不回补计费。
	effectiveStart := math.Max(lastUsage, requestBefore)
	if effectiveStart < baseline {
		effectiveStart = baseline
	}
	effectiveEnd := math.Max(effectiveStart, requestAfter)
	if effectiveEnd < baseline {
		effectiveEnd = baseline
	}

	startAmount := settlementAmountForUsage(effectiveStart, baseline, initial, full)
	target := settlementAmountForUsage(effectiveEnd, baseline, initial, full)
	if target < startAmount {
		target = startAmount
	}

	settleBase := settled
	if startAmount > settleBase {
		settleBase = startAmount
	}
	delta := roundUSD(target - settleBase)
	if delta < 0 {
		delta = 0
	}

	if target < settled {
		target = settled
	}
	if target > full {
		target = full
	}
	newFinalized := finalized || effectiveEnd >= 100 || target >= full

	updateSettlement := fmt.Sprintf(`
		UPDATE public_account_settlements
		SET last_usage_percent = $1,
		    settled_amount_usd = $2,
		    finalized = $3,
		    updated_at = %s
		WHERE account_id = $4
	`, db.nowExpr())
	if _, err = tx.ExecContext(ctx, updateSettlement, effectiveEnd, target, newFinalized, accountID); err != nil {
		return nil, err
	}

	balance := 0.0
	if delta > 0 {
		updateBalance := fmt.Sprintf(`
			UPDATE public_api_keys
			SET balance_usd = COALESCE(balance_usd, 0) + $1, updated_at = %s
			WHERE id = $2
		`, db.nowExpr())
		if _, err = tx.ExecContext(ctx, updateBalance, delta, publicKeyID); err != nil {
			return nil, err
		}

		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(balance_usd, 0) FROM public_api_keys WHERE id = $1`, publicKeyID).Scan(&balance); err != nil {
			return nil, err
		}

		insertLog := fmt.Sprintf(`
			INSERT INTO public_balance_logs (public_api_key_id, account_id, entry_type, delta_usd, balance_after_usd, note, created_at)
			VALUES ($1, $2, 'usage_settlement', $3, $4, $5, %s)
		`, db.nowExpr())
		note := fmt.Sprintf("request_before=%.4f request_after=%.4f effective_end=%.4f", requestBefore, requestAfter, effectiveEnd)
		if _, err = tx.ExecContext(ctx, insertLog, publicKeyID, accountID, delta, balance, note); err != nil {
			return nil, err
		}
	} else {
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(balance_usd, 0) FROM public_api_keys WHERE id = $1`, publicKeyID).Scan(&balance); err != nil {
			return nil, err
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	return &PublicSettlementResult{
		AccountID:      accountID,
		PublicAPIKeyID: publicKeyID,
		UsagePercent:   effectiveEnd,
		DeltaUSD:       delta,
		SettledAmount:  target,
		BalanceUSD:     roundUSD(balance),
		Finalized:      newFinalized,
	}, nil
}

// NormalizeRedeemCodes 清洗粘贴导入的兑换码
func NormalizeRedeemCodes(rawCodes []string) []string {
	seen := make(map[string]struct{}, len(rawCodes))
	out := make([]string, 0, len(rawCodes))
	for _, raw := range rawCodes {
		code := strings.TrimSpace(raw)
		if code == "" {
			continue
		}
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	return out
}

// ImportRedeemCodes 批量导入兑换码（按状态软删除模型）
func (db *DB) ImportRedeemCodes(ctx context.Context, amountUSD float64, codes []string) (inserted int, duplicates int, err error) {
	amountUSD = roundUSD(amountUSD)
	if amountUSD <= 0 {
		return 0, 0, nil
	}
	codes = NormalizeRedeemCodes(codes)
	if len(codes) == 0 {
		return 0, 0, nil
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	insertQuery := fmt.Sprintf(`
		INSERT INTO redeem_codes (code, amount_usd, status, created_at, updated_at)
		VALUES ($1, $2, 'active', %s, %s)
		ON CONFLICT (code) DO NOTHING
	`, db.nowExpr(), db.nowExpr())

	for _, code := range codes {
		res, execErr := tx.ExecContext(ctx, insertQuery, code, amountUSD)
		if execErr != nil {
			return inserted, duplicates, execErr
		}
		affected, _ := res.RowsAffected()
		if affected > 0 {
			inserted++
		} else {
			duplicates++
		}
	}

	if err = tx.Commit(); err != nil {
		return inserted, duplicates, err
	}
	return inserted, duplicates, nil
}

// ListRedeemCodeSummaries 返回可用兑换码的面额统计
func (db *DB) ListRedeemCodeSummaries(ctx context.Context) ([]RedeemCodeSummary, error) {
	rows, err := db.conn.QueryContext(ctx, `
		SELECT amount_usd, COUNT(*)
		FROM redeem_codes
		WHERE status = 'active'
		GROUP BY amount_usd
		ORDER BY amount_usd DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]RedeemCodeSummary, 0)
	for rows.Next() {
		item := RedeemCodeSummary{}
		if err = rows.Scan(&item.AmountUSD, &item.Count); err != nil {
			return nil, err
		}
		item.AmountUSD = roundUSD(item.AmountUSD)
		result = append(result, item)
	}
	return result, rows.Err()
}

// RedeemByPublicKey 按公开 key 余额兑换码
func (db *DB) RedeemByPublicKey(ctx context.Context, publicKeyID int64, requestedAmount float64, requestIP string) (*RedeemCodeResult, error) {
	if publicKeyID == 0 {
		return nil, &RedeemFailure{Kind: "invalid_key", Message: "公开密钥无效"}
	}
	requestedAmount = roundUSD(requestedAmount)
	if requestedAmount <= 0 {
		return nil, &RedeemFailure{Kind: "invalid_amount", Message: "兑换金额必须大于 0"}
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	selectKey := `SELECT COALESCE(balance_usd, 0) FROM public_api_keys WHERE id = $1`
	if !db.isSQLite() {
		selectKey += ` FOR UPDATE`
	}
	var balance float64
	if err = tx.QueryRowContext(ctx, selectKey, publicKeyID).Scan(&balance); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &RedeemFailure{Kind: "invalid_key", Message: "公开密钥不存在"}
		}
		return nil, err
	}
	balance = roundUSD(balance)

	var (
		codeID     int64
		codeValue  string
		codeAmount float64
	)
	selectCode := `
		SELECT id, code, amount_usd
		FROM redeem_codes
		WHERE status = 'active' AND amount_usd <= $1
		ORDER BY amount_usd DESC, id ASC
		LIMIT 1
	`
	err = tx.QueryRowContext(ctx, selectCode, requestedAmount).Scan(&codeID, &codeValue, &codeAmount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &RedeemFailure{
			Kind:            "no_code",
			Message:         "没有可兑换的金额（未找到小于等于请求金额的兑换码）",
			RequestedAmount: requestedAmount,
		}
	}
	if err != nil {
		return nil, err
	}
	codeAmount = roundUSD(codeAmount)

	if balance < codeAmount {
		return nil, &RedeemFailure{
			Kind:            "insufficient_balance",
			Message:         "余额不足",
			RequestedAmount: requestedAmount,
			MatchedAmount:   codeAmount,
			BalanceUSD:      balance,
		}
	}

	updateCode := fmt.Sprintf(`
		UPDATE redeem_codes
		SET status = 'deleted',
		    deleted_at = %s,
		    deleted_reason = 'redeemed',
		    deleted_by_public_key_id = $1,
		    updated_at = %s
		WHERE id = $2 AND status = 'active'
	`, db.nowExpr(), db.nowExpr())
	res, err := tx.ExecContext(ctx, updateCode, publicKeyID, codeID)
	if err != nil {
		return nil, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, &RedeemFailure{Kind: "code_conflict", Message: "兑换码已被使用，请重试"}
	}

	newBalance := roundUSD(balance - codeAmount)
	updateKey := fmt.Sprintf(`
		UPDATE public_api_keys
		SET balance_usd = $1, last_used_ip = $2, last_used_at = %s, updated_at = %s
		WHERE id = $3
	`, db.nowExpr(), db.nowExpr())
	if _, err = tx.ExecContext(ctx, updateKey, newBalance, strings.TrimSpace(requestIP), publicKeyID); err != nil {
		return nil, err
	}

	insertLog := fmt.Sprintf(`
		INSERT INTO public_balance_logs (public_api_key_id, account_id, entry_type, delta_usd, balance_after_usd, note, created_at)
		VALUES ($1, 0, 'redeem', $2, $3, $4, %s)
	`, db.nowExpr())
	if _, err = tx.ExecContext(ctx, insertLog, publicKeyID, -codeAmount, newBalance, fmt.Sprintf("code=%s", codeValue)); err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	return &RedeemCodeResult{
		PublicAPIKeyID:   publicKeyID,
		Code:             codeValue,
		RequestedAmount:  requestedAmount,
		RedeemedAmount:   codeAmount,
		RemainingBalance: newBalance,
	}, nil
}
