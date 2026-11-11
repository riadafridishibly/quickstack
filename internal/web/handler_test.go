package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleCanceled(t *testing.T) {
	t.Parallel()
	h := Handle(func(_ http.ResponseWriter, r *http.Request) error {
		return fmt.Errorf("query: %w", context.Canceled)
	})

	// A client that went away gets nothing written.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil))
	if w.Body.Len() != 0 {
		t.Errorf("canceled request got %d: %s", w.Code, w.Body)
	}

	// A cancellation of the handler's own making is still an error.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500", w.Code)
	}
}
