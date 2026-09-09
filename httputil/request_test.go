package httputil

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func TestWriteJSONError(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteJSONError(rr, "Unauthorized access", http.StatusUnauthorized)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", rr.Code)
	}
	if !strings.HasPrefix(rr.Header().Get(HeaderContentType), ContentTypeJSON) {
		t.Errorf("Expected JSON content type, got %s", rr.Header().Get(HeaderContentType))
	}

	var resp JSONErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode JSON error: %v", err)
	}
	if resp.Error != "Unauthorized access" {
		t.Errorf("Expected error message 'Unauthorized access', got %q", resp.Error)
	}
}

func TestReadLimitedBody(t *testing.T) {
	// Normal body within limit
	data := []byte("hello world")
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(data))
	w := httptest.NewRecorder()

	read, err := ReadLimitedBody(w, req, 100)
	if err != nil {
		t.Fatalf("ReadLimitedBody failed: %v", err)
	}
	if !bytes.Equal(read, data) {
		t.Errorf("Expected %q, got %q", data, read)
	}

	// Body exceeding limit
	largeData := bytes.Repeat([]byte("a"), 200)
	req2 := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(largeData))
	w2 := httptest.NewRecorder()

	_, err = ReadLimitedBody(w2, req2, 100)
	if err == nil {
		t.Fatal("Expected error for body exceeding limit, got nil")
	}
	if w2.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("Expected status 413, got %d", w2.Code)
	}
}

func TestGetRouteParam(t *testing.T) {
	// 1. gorilla/mux fallback
	req := httptest.NewRequest(http.MethodGet, "/tenants/12345/rooms/abc", nil)
	req = mux.SetURLVars(req, map[string]string{
		"id":     "12345",
		"roomId": "abc",
		"neg":    "-5",
		"bad":    "not-a-number",
	})

	if val := GetRouteParam(req, "roomId"); val != "abc" {
		t.Errorf("Expected 'abc', got %q", val)
	}

	// 2. GetInt64Param
	id, err := GetInt64Param(req, "id")
	if err != nil || id != 12345 {
		t.Errorf("GetInt64Param failed: val=%d, err=%v", id, err)
	}

	// 3. GetPositiveInt64Param
	posID, err := GetPositiveInt64Param(req, "id")
	if err != nil || posID != 12345 {
		t.Errorf("GetPositiveInt64Param failed: val=%d, err=%v", posID, err)
	}

	// 4. Negative int fails positive check
	_, err = GetPositiveInt64Param(req, "neg")
	if err == nil {
		t.Error("Expected error for negative int in GetPositiveInt64Param")
	}

	// 5. Non-number fails
	_, err = GetInt64Param(req, "bad")
	if err == nil {
		t.Error("Expected error for bad int in GetInt64Param")
	}

	// 6. Missing param fails
	_, err = GetInt64Param(req, "missing")
	if err == nil {
		t.Error("Expected error for missing param")
	}
}
