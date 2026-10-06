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

func TestWriteSignedURLResponse(t *testing.T) {
	rr := httptest.NewRecorder()
	url := "https://storage.example.com/file/1?sig=abc"
	WriteSignedURLResponse(rr, url)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}
	if !strings.HasPrefix(rr.Header().Get(HeaderContentType), ContentTypeJSON) {
		t.Errorf("Expected JSON content type, got %s", rr.Header().Get(HeaderContentType))
	}
	if rr.Header().Get(HeaderCacheControl) != "no-cache, no-store, must-revalidate" {
		t.Errorf("Expected no-cache cache control, got %s", rr.Header().Get(HeaderCacheControl))
	}

	var resp SignedURLResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode signed URL response: %v", err)
	}
	if resp.URL != url {
		t.Errorf("Expected URL %q, got %q", url, resp.URL)
	}
}

func TestSetMaxByteHeader(t *testing.T) {
	// Nil request or nil body returns nil
	if res := SetMaxByteHeader(httptest.NewRecorder(), nil, 1024); res != nil {
		t.Errorf("expected nil for nil request, got %v", res)
	}
	reqNilBody := httptest.NewRequest(http.MethodGet, "/", nil)
	reqNilBody.Body = nil
	if res := SetMaxByteHeader(httptest.NewRecorder(), reqNilBody, 1024); res != nil {
		t.Errorf("expected nil for nil body, got %v", res)
	}

	// Non-nil body is wrapped with MaxBytesReader
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("1234567890"))
	w := httptest.NewRecorder()
	req.Body = SetMaxByteHeader(w, req, 5)
	if req.Body == nil {
		t.Fatal("expected non-nil wrapped body")
	}

	// Reading up to 5 bytes succeeds, 6th byte fails with MaxBytesError
	buf := make([]byte, 10)
	n, err := req.Body.Read(buf)
	if n != 5 {
		t.Errorf("expected 5 bytes read, got %d", n)
	}
	_, err = req.Body.Read(buf)
	if err == nil {
		t.Errorf("expected error when exceeding max bytes, got nil")
	}

	// Alias SetMaxBytesReader
	req2 := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("abc"))
	req2.Body = SetMaxBytesReader(w, req2, 10)
	if req2.Body == nil {
		t.Fatal("expected non-nil body from SetMaxBytesReader")
	}
}
