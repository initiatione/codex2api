package admin

import "testing"

func TestExtractCredentialUpdatesFromRawInfo(t *testing.T) {
	raw := []byte(`{
		"account": {
			"email": "demo@example.com",
			"chatgpt_account_id": "acc_123",
			"plan_type": "Plus"
		}
	}`)

	refreshed, updates := extractCredentialUpdatesFromRawInfo(raw)

	if got := refreshed["email"]; got != "demo@example.com" {
		t.Fatalf("email = %q, want demo@example.com", got)
	}
	if got := refreshed["account_id"]; got != "acc_123" {
		t.Fatalf("account_id = %q, want acc_123", got)
	}
	if got := refreshed["plan_type"]; got != "plus" {
		t.Fatalf("plan_type = %q, want plus", got)
	}

	if got, _ := updates["email"].(string); got != "demo@example.com" {
		t.Fatalf("updates[email] = %q, want demo@example.com", got)
	}
	if got, _ := updates["account_id"].(string); got != "acc_123" {
		t.Fatalf("updates[account_id] = %q, want acc_123", got)
	}
	if got, _ := updates["plan_type"].(string); got != "plus" {
		t.Fatalf("updates[plan_type] = %q, want plus", got)
	}
}
