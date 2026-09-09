package httputil

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
)

// DefaultMaxRequestBodySize limits incoming request bodies to 4 MB to prevent memory exhaustion / DoS.
const DefaultMaxRequestBodySize int64 = 4 << 20

// JSONErrorResponse represents the standard JSON error payload returned by WriteJSONError.
type JSONErrorResponse struct {
	Error string `json:"error"`
}

// WriteJSONError writes a JSON error response {"error": "..."} with the given status code and sets Content-Type.
func WriteJSONError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set(HeaderContentType, ContentTypeJSONUTF8)
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(JSONErrorResponse{Error: msg})
}

// ReadLimitedBody reads the request body up to maxBytes. If maxBytes <= 0, DefaultMaxRequestBodySize is used.
// If the body exceeds the size limit, an HTTP 413 Payload Too Large error is written to w and an error is returned.
// If reading fails for another reason, an HTTP 400 Bad Request error is written to w.
func ReadLimitedBody(w http.ResponseWriter, r *http.Request, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxRequestBodySize
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			WriteJSONError(w, MsgPayloadTooLarge, http.StatusRequestEntityTooLarge)
			return nil, err
		}
		WriteJSONError(w, "Failed to read request body", http.StatusBadRequest)
		return nil, err
	}
	return body, nil
}

// GetRouteParam extracts a named routing parameter from the request.
// It checks standard library Go 1.22+ r.PathValue(name) first, and falls back to gorilla/mux mux.Vars(r)[name].
func GetRouteParam(r *http.Request, name string) string {
	if val := r.PathValue(name); val != "" {
		return val
	}
	vars := mux.Vars(r)
	if vars != nil {
		return vars[name]
	}
	return ""
}

// GetInt64Param extracts a route parameter as an int64.
// If missing or not a valid int64, it returns an error.
func GetInt64Param(r *http.Request, name string) (int64, error) {
	valStr := strings.TrimSpace(GetRouteParam(r, name))
	if valStr == "" {
		return 0, fmt.Errorf("missing route parameter %q", name)
	}
	val, err := strconv.ParseInt(valStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid integer route parameter %q: %w", name, err)
	}
	return val, nil
}

// GetPositiveInt64Param extracts a route parameter and verifies it is a strictly positive integer (> 0).
func GetPositiveInt64Param(r *http.Request, name string) (int64, error) {
	val, err := GetInt64Param(r, name)
	if err != nil {
		return 0, err
	}
	if val <= 0 {
		return 0, fmt.Errorf("route parameter %q must be greater than 0", name)
	}
	return val, nil
}
