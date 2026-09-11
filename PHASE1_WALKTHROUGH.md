# Phase 1 Implementation Walkthrough: Shared Go Infrastructure Consolidation

Consolidated shared Go infrastructure from `kvittoburken.se`, `lst`, and `passwordsender.com` into [goutils](file:///home/kristofer/Development/goutils) as Phase 1 of the 3-step migration plan.

## Changes Made

### 1. Centralized Configuration ([config/config.go](file:///home/kristofer/Development/goutils/config/config.go))
- **`ConfigManager`** singleton backed by `os.Getenv` or custom lookup function for testing.
- Automatic fallback from `ENVIRONMENT` to legacy `OTAP` (defaulting to `"dev"`).
- Helper methods for all cloud keys, secrets, URLs, and environment validation.
- Comprehensive test coverage in [config_test.go](file:///home/kristofer/Development/goutils/config/config_test.go).

### 2. VCS Build Versioning ([version/version.go](file:///home/kristofer/Development/goutils/version/version.go))
- Read build info via `debug.ReadBuildInfo()`: Git commit hash, commit time, and dirty status.
- Internal testable parser [version_test.go](file:///home/kristofer/Development/goutils/version/version_test.go) verifying clean, dirty, and default scenarios.

### 3. Telemetry & Contextual Logging ([telemetry/context.go](file:///home/kristofer/Development/goutils/telemetry/context.go), [telemetry/ratelimit.go](file:///home/kristofer/Development/goutils/telemetry/ratelimit.go))
- **`ContextHandler`**: Structured `slog.Handler` injecting `trace_id`, `session_id`, `user_id`, `email`, `client_ip`, and `user_agent`.
- **`TraceMiddleware`**: Injects and propagates `Trace-ID`, `X-Session-ID`, and session cookies.
- **`IPRateLimiter`**: Token bucket rate limiter per client IP with background visitor pruning and `Stop()` lifecycle cleanup.
- **`GetClientIP`**: Reverse-proxy header extraction (`X-Real-IP`, `X-Forwarded-For`) with CIDR/private network proxy verification.
- Comprehensive test suites in [context_test.go](file:///home/kristofer/Development/goutils/telemetry/context_test.go) and [ratelimit_test.go](file:///home/kristofer/Development/goutils/telemetry/ratelimit_test.go).

### 4. HTTP Utilities & Static Serving ([httputil/httputil.go](file:///home/kristofer/Development/goutils/httputil/httputil.go))
- **`NewStaticServer`**: Production SPA and asset file server with:
  - Extensionless clean URL matching (`/about` -> `about.html`)
  - SPA fallback to `index.html`
  - Pre-compressed `.br` Brotli serving with `Content-Encoding: br`
  - On-the-fly Brotli decompression when the client does not accept `br`
  - Defensive path traversal protection, null-byte rejection, dotfile blocking, and extension blocklists
  - Granular Cache-Control (immutable for hashed assets, no-cache for HTML, 1h for static)
- **`SecurityHeadersMiddleware`**: CSP, Permissions-Policy, HSTS, X-Frame-Options, X-Content-Type-Options.
- **`CORSMiddleware`**: Preflight OPTIONS handling, origin validation, credentials control.
- **`RestrictiveRobotsTxtHandler`**: Standard restrictive robots.txt handler.
- Tests in [httputil_test.go](file:///home/kristofer/Development/goutils/httputil/httputil_test.go).

### 5. Object Storage Abstraction & Providers ([storage/](file:///home/kristofer/Development/goutils/storage))
- **`storage.StorageClient`**: Universal interface for cloud storage (revisions, raw/brotli read/write, presigned URLs, listing).
- **Pure Go Brotli** in [storage/compression.go](file:///home/kristofer/Development/goutils/storage/compression.go) via `github.com/molecule-man/go-brrr`.
- **AWS S3 Provider** in [storage/s3/s3.go](file:///home/kristofer/Development/goutils/storage/s3/s3.go):
  - Conditional compile tag `//go:build !exclude_s3`
  - Stub in [storage/s3/stub.go](file:///home/kristofer/Development/goutils/storage/s3/stub.go) for `//go:build exclude_s3`
- **Google Cloud Storage Provider** in [storage/gcs/gcs.go](file:///home/kristofer/Development/goutils/storage/gcs/gcs.go):
  - Conditional compile tag `//go:build !exclude_gcs`
  - Stub in [storage/gcs/stub.go](file:///home/kristofer/Development/goutils/storage/gcs/stub.go) for `//go:build exclude_gcs`
- **Optimistic Concurrency Docstore** in [storage/docstore/docstore.go](file:///home/kristofer/Development/goutils/storage/docstore/docstore.go):
  - Protobuf persistence with conditional revision writes and retry loops
  - Schema minor version guard preventing writes from older code versions
- Full unit tests in [storage_test.go](file:///home/kristofer/Development/goutils/storage/storage_test.go), [compression_test.go](file:///home/kristofer/Development/goutils/storage/compression_test.go), [docstore_test.go](file:///home/kristofer/Development/goutils/storage/docstore/docstore_test.go), [s3_test.go](file:///home/kristofer/Development/goutils/storage/s3/s3_test.go), [gcs_test.go](file:///home/kristofer/Development/goutils/storage/gcs/gcs_test.go), and exclude stub tests.

### 6. GCP Cloud Metadata ([cloud/gcp/project.go](file:///home/kristofer/Development/goutils/cloud/gcp/project.go))
- `DetermineProjectID()`: Detects project ID via environment variables, GCE metadata server, or ADC. Fails with an explicit error instead of defaulting.
- `IsRunningOnGCE()`: GCE / Cloud Run detector.
- Tests in [project_test.go](file:///home/kristofer/Development/goutils/cloud/gcp/project_test.go).

### 7. Generalized OIDC Authentication ([auth/](file:///home/kristofer/Development/goutils/auth))
- **`OIDCRegistry`**: Multi-provider registry parsed from JSON.
- **`UserIdentity`**: Normalized representation of authenticated subject, claims, and verified email.
- **`IdentityResolver` Interface**: Generalizes application-specific user databases (companies in kvittoburken, churches/organizations in lst).
- **`AuthMiddleware`**: Token validation, claim extraction, and context enrichment via `IdentityResolver`.
- Tests in [auth_test.go](file:///home/kristofer/Development/goutils/auth/auth_test.go).

### 8. Deprecation Annotations for Legacy APIs
All legacy root files and logging functions now include `// Deprecated:` comments directing consumers to the new packages while maintaining 100% API compatibility for `passwordsender.com`:
- [otap.go](file:///home/kristofer/Development/goutils/otap.go) -> `// Deprecated: Use config.GetEnvironment() instead.`
- [gcpproject.go](file:///home/kristofer/Development/goutils/gcpproject.go) -> `// Deprecated: Use cloud/gcp.DetermineProjectID() instead.`
- [httpserverutils.go](file:///home/kristofer/Development/goutils/httpserverutils.go) -> `// Deprecated: Use httputil package instead.`
- [robots.go](file:///home/kristofer/Development/goutils/robots.go) -> `// Deprecated: Use httputil.RestrictiveRobotsTxtHandler() instead.`
- [staticcache.go](file:///home/kristofer/Development/goutils/staticcache.go) -> `// Deprecated: Use httputil.SetCacheControl or NewStaticServer instead.`
- [logging/gcplogging.go](file:///home/kristofer/Development/goutils/logging/gcplogging.go) -> `// Deprecated: Use telemetry.ContextHandler with log/slog instead.`

---

## Verification Results

### Automated Test Suite
Ran `go test -v -count=1 ./...`:
- **Root `goutils`**: `PASS` (legacy tests pass)
- **`config`**: `PASS` (defaults, fallbacks, validation, singleton)
- **`version`**: `PASS` (clean/dirty VCS metadata, fallback)
- **`telemetry`**: `PASS` (trace propagation, ContextHandler, rate limiting, trusted proxies)
- **`httputil`**: `PASS` (SPA fallback, Brotli handling, security headers, CORS preflight, traversal protection)
- **`storage`**: `PASS` (Brotli compression roundtrip, MIME types, healthcheck)
- **`storage/s3`**: `PASS` (initialization, constructor registration)
- **`storage/gcs`**: `PASS` (initialization, constructor registration)
- **`storage/docstore`**: `PASS` (concurrency retry, version guard, document creation)
- **`cloud/gcp`**: `PASS` (precedence order, GCE check)
- **`auth`**: `PASS` (provider lookup, JWT claim verification, IdentityResolver context enrichment, rejection)

### Build Tag Matrix
1. Default build (both S3 and GCS included): `go test ./...` -> **PASS**
2. S3 excluded: `go test -tags exclude_s3 ./...` -> **PASS**
3. GCS excluded: `go test -tags exclude_gcs ./...` -> **PASS**
4. Both excluded: `go test -tags "exclude_s3 exclude_gcs" ./...` -> **PASS**
