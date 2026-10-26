package legal

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tingdahl/goutils/httputil"
	"google.golang.org/protobuf/proto"
)

type mockStore struct {
	acceptedUserID  string
	acceptedVersion string
	err             error
}

func (m *mockStore) SetTCAccepted(ctx context.Context, userID, version string) error {
	if m.err != nil {
		return m.err
	}
	m.acceptedUserID = userID
	m.acceptedVersion = version
	return nil
}

func TestHasAcceptedTerms(t *testing.T) {
	if !HasAcceptedTerms("2026-09-21", "2026-09-21") {
		t.Error("expected matching versions to be accepted")
	}
	if !HasAcceptedTerms("2026-09-21 ", " 2026-09-21") {
		t.Error("expected whitespace-trimmed versions to match")
	}
	if HasAcceptedTerms("2026-01-01", "2026-09-21") {
		t.Error("expected outdated version to not be accepted")
	}
	if HasAcceptedTerms("", "2026-09-21") {
		t.Error("expected empty user version to not be accepted")
	}
	if !HasAcceptedTerms("anything", "") {
		t.Error("expected empty required version to always be accepted")
	}
}

func TestTermsHandler(t *testing.T) {
	handler := TermsHandler(func() *TermsInfoProto {
		return &TermsInfoProto{
			Type:    "broadcaster",
			Version: "2026-09-21",
			Content: "Terms content",
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/api/legal/terms", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != httputil.ContentTypeProtobuf {
		t.Errorf("expected %s, got %s", httputil.ContentTypeProtobuf, w.Header().Get("Content-Type"))
	}
	if w.Header().Get("Cache-Control") != "public, max-age=86400" {
		t.Errorf("expected public, max-age=86400, got %s", w.Header().Get("Cache-Control"))
	}

	var info TermsInfoProto
	if err := proto.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatalf("failed to decode Protobuf: %v", err)
	}
	if info.Type != "broadcaster" || info.Version != "2026-09-21" || info.Content != "Terms content" {
		t.Errorf("unexpected TermsInfoProto: %+v", &info)
	}
}

func TestAcceptTermsHandler(t *testing.T) {
	store := &mockStore{}
	getUserID := func(r *http.Request) (string, bool) {
		token := r.Header.Get("X-User-ID")
		if token != "" {
			return token, true
		}
		return "", false
	}

	handler := AcceptTermsHandler(AcceptTermsOptions{
		Store:     store,
		GetUserID: getUserID,
	})

	marshalReq := func(version string) []byte {
		b, _ := proto.Marshal(&AcceptTermsRequestProto{Version: version})
		return b
	}

	// 1. Unauthorized
	req := httptest.NewRequest(http.MethodPut, "/api/me/terms", bytes.NewReader(marshalReq("2026-09-21")))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthorized, got %d", w.Code)
	}

	// 2. Invalid Protobuf
	req = httptest.NewRequest(http.MethodPut, "/api/me/terms", bytes.NewReader([]byte{0xff, 0xff, 0xff}))
	req.Header.Set("X-User-ID", "user-1")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad protobuf, got %d", w.Code)
	}

	// 3. Empty version
	req = httptest.NewRequest(http.MethodPut, "/api/me/terms", bytes.NewReader(marshalReq("  ")))
	req.Header.Set("X-User-ID", "user-1")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty version, got %d", w.Code)
	}

	// 4. Successful acceptance
	req = httptest.NewRequest(http.MethodPut, "/api/me/terms", bytes.NewReader(marshalReq("2026-09-21")))
	req.Header.Set("X-User-ID", "user-1")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != httputil.ContentTypeProtobuf {
		t.Errorf("expected %s, got %s", httputil.ContentTypeProtobuf, w.Header().Get("Content-Type"))
	}
	if store.acceptedUserID != "user-1" || store.acceptedVersion != "2026-09-21" {
		t.Errorf("store mismatch: user=%q, version=%q", store.acceptedUserID, store.acceptedVersion)
	}

	var resp AcceptTermsResponseProto
	if err := proto.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success || resp.Version != "2026-09-21" || resp.AcceptedAtUnixMs == 0 {
		t.Errorf("unexpected response: %+v", &resp)
	}

	// 5. Store error
	store.err = errors.New("db error")
	req = httptest.NewRequest(http.MethodPut, "/api/me/terms", bytes.NewReader(marshalReq("2026-09-21")))
	req.Header.Set("X-User-ID", "user-1")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}
