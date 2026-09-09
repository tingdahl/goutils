package cron_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tingdahl/goutils/cron"
)

func TestRegistryHandler(t *testing.T) {
	const validSecret = "test-secret-123"

	t.Run("unauthorized when secret empty", func(t *testing.T) {
		reg := cron.NewRegistry()
		h := reg.Handler(func() string { return "" }, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/cron", nil)
		req.Header.Set("Authorization", "Bearer "+validSecret)
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", rec.Code)
		}
	})

	t.Run("unauthorized with invalid token", func(t *testing.T) {
		reg := cron.NewRegistry()
		h := reg.Handler(func() string { return validSecret }, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/cron", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", rec.Code)
		}
	})

	t.Run("success with Bearer token and tasks", func(t *testing.T) {
		reg := cron.NewRegistry()
		executedA := false
		executedB := false

		reg.Register("taskA", func(ctx context.Context) error {
			executedA = true
			return nil
		})
		reg.Register("taskB", func(ctx context.Context) error {
			executedB = true
			return nil
		})

		h := reg.Handler(func() string { return validSecret }, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/cron", nil)
		req.Header.Set("Authorization", "Bearer "+validSecret)
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if !executedA || !executedB {
			t.Fatalf("expected both tasks executed, got A=%v, B=%v", executedA, executedB)
		}
	})

	t.Run("success with X-Cron-Key header", func(t *testing.T) {
		reg := cron.NewRegistry()
		executed := false
		reg.Register("taskX", func(ctx context.Context) error {
			executed = true
			return nil
		})

		h := reg.Handler(func() string { return validSecret }, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/cron", nil)
		req.Header.Set("X-Cron-Key", validSecret)
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if !executed {
			t.Fatal("expected task executed")
		}
	})

	t.Run("task failure returns 500 and lists errors", func(t *testing.T) {
		reg := cron.NewRegistry()
		reg.Register("taskOK", func(ctx context.Context) error {
			return nil
		})
		reg.Register("taskFail", func(ctx context.Context) error {
			return errors.New("something went wrong")
		})

		h := reg.Handler(func() string { return validSecret }, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/cron", nil)
		req.Header.Set("Authorization", "Bearer "+validSecret)
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected status 500, got %d", rec.Code)
		}
	})
}
