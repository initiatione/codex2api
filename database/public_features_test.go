package database

import (
	"context"
	"math"
	"path/filepath"
	"testing"
)

func newSQLiteDBForPublicFeatureTest(t *testing.T) *DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 0.0001
}

func TestPublicSettlementFlow(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDBForPublicFeatureTest(t)

	keyID, err := db.InsertPublicAPIKeyWithMeta(ctx, "pub", "pk-settle-test-1234567890", "public_generate", "1.2.3.4", 0)
	if err != nil {
		t.Fatalf("insert public key: %v", err)
	}

	accountID, err := db.InsertATAccount(ctx, "acc", "at-token-1", "")
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}

	if err := db.BindAccountToPublicKey(ctx, PublicSettlementBindInput{
		AccountID:            accountID,
		PublicAPIKeyID:       keyID,
		BaselineUsagePercent: 0,
		InitialAmountUSD:     0.1,
		FullAmountUSD:        2.0,
	}); err != nil {
		t.Fatalf("bind account to public key: %v", err)
	}

	r1, err := db.SettlePublicAccountUsage(ctx, accountID, 50)
	if err != nil {
		t.Fatalf("settle usage 50%%: %v", err)
	}
	if r1 == nil {
		t.Fatalf("settle usage 50%%: nil result")
	}
	if !almostEqual(r1.DeltaUSD, 0.95) {
		t.Fatalf("delta(50%%) = %.4f, want 0.95", r1.DeltaUSD)
	}
	if !almostEqual(r1.BalanceUSD, 1.05) {
		t.Fatalf("balance(50%%) = %.4f, want 1.05", r1.BalanceUSD)
	}

	r2, err := db.SettlePublicAccountUsage(ctx, accountID, 100)
	if err != nil {
		t.Fatalf("settle usage 100%%: %v", err)
	}
	if r2 == nil {
		t.Fatalf("settle usage 100%%: nil result")
	}
	if !almostEqual(r2.DeltaUSD, 0.95) {
		t.Fatalf("delta(100%%) = %.4f, want 0.95", r2.DeltaUSD)
	}
	if !almostEqual(r2.BalanceUSD, 2.0) {
		t.Fatalf("balance(100%%) = %.4f, want 2.0", r2.BalanceUSD)
	}

	rows, err := db.ListAll(ctx)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("accounts len = %d, want 1", len(rows))
	}
	if !rows[0].PublicAPIKeyID.Valid || rows[0].PublicAPIKeyID.Int64 != keyID {
		t.Fatalf("public_api_key_id not bound, got=%v", rows[0].PublicAPIKeyID)
	}
	if !almostEqual(rows[0].SettledAmount, 2.0) {
		t.Fatalf("settled_amount = %.4f, want 2.0", rows[0].SettledAmount)
	}
}

func TestPublicSettlementUsesRequestDeltaOnly(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDBForPublicFeatureTest(t)

	keyID, err := db.InsertPublicAPIKeyWithMeta(ctx, "pub", "pk-settle-test-jump-1234567890", "public_generate", "1.2.3.4", 0)
	if err != nil {
		t.Fatalf("insert public key: %v", err)
	}

	accountID, err := db.InsertATAccount(ctx, "acc-jump", "at-token-jump", "")
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}

	if err = db.BindAccountToPublicKey(ctx, PublicSettlementBindInput{
		AccountID:            accountID,
		PublicAPIKeyID:       keyID,
		BaselineUsagePercent: 0,
		InitialAmountUSD:     0.1,
		FullAmountUSD:        2.0,
	}); err != nil {
		t.Fatalf("bind account to public key: %v", err)
	}

	r1, err := db.SettlePublicAccountUsageWithRequestDelta(ctx, accountID, 0, true, 5)
	if err != nil {
		t.Fatalf("settle usage 5%%: %v", err)
	}
	if r1 == nil {
		t.Fatalf("settle usage 5%%: nil result")
	}
	if !almostEqual(r1.DeltaUSD, 0.095) {
		t.Fatalf("delta(5%%) = %.4f, want 0.095", r1.DeltaUSD)
	}
	if !almostEqual(r1.BalanceUSD, 0.195) {
		t.Fatalf("balance(5%%) = %.4f, want 0.195", r1.BalanceUSD)
	}

	// 模拟请求开始前账号已被外部消耗到 85%，本次请求只从 85% -> 87%。
	r2, err := db.SettlePublicAccountUsageWithRequestDelta(ctx, accountID, 85, true, 87)
	if err != nil {
		t.Fatalf("settle usage 85%%->87%%: %v", err)
	}
	if r2 == nil {
		t.Fatalf("settle usage 85%%->87%%: nil result")
	}
	if !almostEqual(r2.DeltaUSD, 0.038) {
		t.Fatalf("delta(85%%->87%%) = %.4f, want 0.038", r2.DeltaUSD)
	}
	if !almostEqual(r2.BalanceUSD, 0.233) {
		t.Fatalf("balance(85%%->87%%) = %.4f, want 0.233", r2.BalanceUSD)
	}
}

func TestRedeemByPublicKeyNearestLower(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDBForPublicFeatureTest(t)

	keyID, err := db.InsertPublicAPIKeyWithMeta(ctx, "pub", "pk-redeem-test-1234567890", "public_generate", "1.2.3.4", 3.0)
	if err != nil {
		t.Fatalf("insert public key: %v", err)
	}

	insertedA, dupA, err := db.ImportRedeemCodes(ctx, 1.0, []string{"CODE-A"})
	if err != nil {
		t.Fatalf("import redeem codes A: %v", err)
	}
	if insertedA != 1 || dupA != 0 {
		t.Fatalf("import A result = inserted %d dup %d, want 1/0", insertedA, dupA)
	}
	insertedB, dupB, err := db.ImportRedeemCodes(ctx, 0.5, []string{"CODE-B"})
	if err != nil {
		t.Fatalf("import redeem codes B: %v", err)
	}
	if insertedB != 1 || dupB != 0 {
		t.Fatalf("import B result = inserted %d dup %d, want 1/0", insertedB, dupB)
	}

	first, err := db.RedeemByPublicKey(ctx, keyID, 0.8, "5.6.7.8")
	if err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	if first.Code != "CODE-B" {
		t.Fatalf("first redeem code = %q, want CODE-B", first.Code)
	}
	if !almostEqual(first.RedeemedAmount, 0.5) {
		t.Fatalf("first redeem amount = %.4f, want 0.5", first.RedeemedAmount)
	}
	if !almostEqual(first.RemainingBalance, 2.5) {
		t.Fatalf("first remaining balance = %.4f, want 2.5", first.RemainingBalance)
	}

	second, err := db.RedeemByPublicKey(ctx, keyID, 1.0, "5.6.7.8")
	if err != nil {
		t.Fatalf("second redeem: %v", err)
	}
	if second.Code != "CODE-A" {
		t.Fatalf("second redeem code = %q, want CODE-A", second.Code)
	}
	if !almostEqual(second.RemainingBalance, 1.5) {
		t.Fatalf("second remaining balance = %.4f, want 1.5", second.RemainingBalance)
	}

	_, err = db.RedeemByPublicKey(ctx, keyID, 1.0, "5.6.7.8")
	if err == nil {
		t.Fatalf("expected no_code error")
	}
	failure, ok := err.(*RedeemFailure)
	if !ok {
		t.Fatalf("error type = %T, want *RedeemFailure", err)
	}
	if failure.Kind != "no_code" {
		t.Fatalf("failure kind = %q, want no_code", failure.Kind)
	}

	summaries, err := db.ListRedeemCodeSummaries(ctx)
	if err != nil {
		t.Fatalf("list summaries: %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("summaries len = %d, want 0", len(summaries))
	}

}
