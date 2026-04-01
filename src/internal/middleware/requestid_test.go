package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

func TestRequestID_GeneratesID(t *testing.T) {
	var capturedID string
	handler := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = domain.RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if capturedID == "" {
		t.Error("expected non-empty request ID in context")
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("expected X-Request-ID response header")
	}
	if rec.Header().Get("X-Request-ID") != capturedID {
		t.Error("header and context request IDs should match")
	}
}

func TestRequestID_ReusesExisting(t *testing.T) {
	existingID := "abc-123"
	var capturedID string
	handler := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = domain.RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", existingID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if capturedID != existingID {
		t.Errorf("expected request ID %q, got %q", existingID, capturedID)
	}
	if rec.Header().Get("X-Request-ID") != existingID {
		t.Errorf("expected header %q, got %q", existingID, rec.Header().Get("X-Request-ID"))
	}
}
