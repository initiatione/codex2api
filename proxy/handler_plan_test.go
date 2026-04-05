package proxy

import (
	"net/http"
	"testing"
	"time"

	"github.com/codex2api/auth"
)

func TestCompute429CooldownPlusUsesWindowDetection(t *testing.T) {
	h := &Handler{}
	account := &auth.Account{PlanType: "plus"}
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("x-codex-primary-used-percent", "100")
	resp.Header.Set("x-codex-primary-window-minutes", "10080")
	resp.Header.Set("x-codex-secondary-used-percent", "10")
	resp.Header.Set("x-codex-secondary-window-minutes", "300")

	got := h.compute429Cooldown(account, nil, resp)
	want := 7 * 24 * time.Hour
	if got != want {
		t.Fatalf("cooldown=%v, want=%v", got, want)
	}
}
