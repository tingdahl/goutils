package legal

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tingdahl/goutils/httputil"
	"google.golang.org/protobuf/proto"
)

// TermsInfo is an alias for TermsInfoProto.
type TermsInfo = TermsInfoProto

// AcceptTermsRequest is an alias for AcceptTermsRequestProto.
type AcceptTermsRequest = AcceptTermsRequestProto

// AcceptTermsResponse is an alias for AcceptTermsResponseProto.
type AcceptTermsResponse = AcceptTermsResponseProto

// AcceptanceStore defines the storage interface for recording terms acceptance.
type AcceptanceStore interface {
	SetTCAccepted(ctx context.Context, userID, version string) error
}

// HasAcceptedTerms checks if the user's accepted version matches the required version.
func HasAcceptedTerms(userVersion, requiredVersion string) bool {
	req := strings.TrimSpace(requiredVersion)
	if req == "" {
		return true
	}
	return strings.TrimSpace(userVersion) == req
}

// TermsHandler returns an HTTP handler that serves TermsInfoProto with protobuf Content-Type and caching.
func TermsHandler(getTerms func() *TermsInfoProto) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		terms := getTerms()
		data, err := proto.Marshal(terms)
		if err != nil {
			http.Error(w, "failed to marshal terms", http.StatusInternalServerError)
			return
		}

		httputil.SetProtobufHeader(w)
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(data)
	}
}

// AcceptTermsOptions configures the terms acceptance handler.
type AcceptTermsOptions struct {
	Store      AcceptanceStore
	GetUserID  func(r *http.Request) (string, bool)
	OnAccepted func(ctx context.Context, userID, version string, w http.ResponseWriter, r *http.Request)
}

// AcceptTermsHandler handles terms acceptance endpoints using Protobuf.
func AcceptTermsHandler(opts AcceptTermsOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.Store == nil || opts.GetUserID == nil {
			http.Error(w, "terms acceptance not configured", http.StatusInternalServerError)
			return
		}

		userID, ok := opts.GetUserID(r)
		if !ok || userID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}

		var req AcceptTermsRequestProto
		if err := proto.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid protobuf request body", http.StatusBadRequest)
			return
		}

		version := strings.TrimSpace(req.Version)
		if version == "" {
			http.Error(w, "version is required", http.StatusBadRequest)
			return
		}

		if err := opts.Store.SetTCAccepted(r.Context(), userID, version); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if opts.OnAccepted != nil {
			opts.OnAccepted(r.Context(), userID, version, w, r)
			return
		}

		resp := &AcceptTermsResponseProto{
			Success:          true,
			Version:          version,
			AcceptedAtUnixMs: time.Now().UnixMilli(),
		}
		data, err := proto.Marshal(resp)
		if err != nil {
			http.Error(w, "failed to marshal response", http.StatusInternalServerError)
			return
		}

		httputil.SetProtobufHeader(w)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}
