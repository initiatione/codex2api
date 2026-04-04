package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/codex2api/database"
	"github.com/gin-gonic/gin"
)

func newSQLiteDBForAdminTest(t *testing.T) *database.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := database.New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func TestCliproxyUploadAuthRejectsWhenNoPublicKeyConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newSQLiteDBForAdminTest(t)
	h := &Handler{db: db}

	r := gin.New()
	r.POST("/v0/management/auth-files", h.cliproxyUploadAuthMiddleware(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/v0/management/auth-files", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want=%d", rec.Code, http.StatusForbidden)
	}
}

func TestCliproxyUploadAuthAllowsOnlyConfiguredPublicKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newSQLiteDBForAdminTest(t)
	h := &Handler{db: db}

	_, err := db.InsertPublicAPIKey(context.Background(), "uploader", "pk-test-public-key-1234567890")
	if err != nil {
		t.Fatalf("insert public key: %v", err)
	}

	r := gin.New()
	r.POST("/v0/management/auth-files", h.cliproxyUploadAuthMiddleware(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	badReq := httptest.NewRequest(http.MethodPost, "/v0/management/auth-files", nil)
	badReq.Header.Set("Authorization", "Bearer sk-not-public")
	badRec := httptest.NewRecorder()
	r.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusUnauthorized {
		t.Fatalf("bad key status=%d, want=%d", badRec.Code, http.StatusUnauthorized)
	}

	okReq := httptest.NewRequest(http.MethodPost, "/v0/management/auth-files", nil)
	okReq.Header.Set("X-Public-Key", "pk-test-public-key-1234567890")
	okRec := httptest.NewRecorder()
	r.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("public key status=%d, want=%d", okRec.Code, http.StatusOK)
	}
}

func TestPublicKeyCannotPassAdminMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newSQLiteDBForAdminTest(t)
	h := &Handler{db: db, adminSecretEnv: "admin-secret-123456"}

	r := gin.New()
	r.GET("/api/admin/health", h.adminAuthMiddleware(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	req.Header.Set("Authorization", "Bearer pk-test-public-key-1234567890")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want=%d", rec.Code, http.StatusUnauthorized)
	}
}
