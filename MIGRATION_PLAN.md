# Consolidate Shared Go Infrastructure into `goutils`

## Three-Phase Approach

| Phase | Repo | Goal |
|---|---|---|
| **Phase 1** | goutils | Add all new packages. Keep every existing export. Record what becomes legacy. |
| **Phase 2** | passwordsender.com, kvittoburken.se, lst | Migrate each consumer to the new goutils packages. |
| **Phase 3** | goutils | Remove legacy code recorded in Phase 1. |

This plan covers **Phase 1** in full detail, and outlines the consumer migration work for Phase 2.

---

## Phase 1 — New Package Layout

```
github.com/tingdahl/goutils/
│
│  ── Existing (LEGACY, kept for now) ──────────────────
│  gcpproject.go          → // Deprecated: use cloud/gcp
│  httpserverutils.go     → // Deprecated: use httputil
│  otap.go                → // Deprecated: use config
│  robots.go              → // Deprecated: use httputil
│  staticcache.go         → // Deprecated: use httputil
│  logging/               → // Deprecated: use log/slog + telemetry
│
│  ── New packages ─────────────────────────────────────
├── config/               # Environment-backed ConfigManager
├── version/              # Git commit / build info from debug.ReadBuildInfo
├── telemetry/            # slog ContextHandler, tracing middleware, IP rate limiter, client IP
├── httputil/             # Port, static/SPA serving, security headers, CORS, cache, robots.txt
├── storage/              # StorageClient interface, compression, content-type helpers
├── storage/s3/           # S3-compatible implementation (AWS SDK v2) — build tag: !exclude_s3
├── storage/gcs/          # GCS implementation — build tag: !exclude_gcs
├── storage/docstore/     # Optimistic-write Document pattern (protobuf-backed)
├── auth/                 # OIDC registry, AuthMiddleware, IdentityResolver, generic UserIdentity
└── cloud/gcp/            # GCP project detection (metadata → ADC → env)
```

---

## Legacy Record (to remove in Phase 3)

Every item below will get a `// Deprecated:` comment in Phase 1 pointing to its replacement.

| Legacy file | Replacement | Notes |
|---|---|---|
| `gcpproject.go` | `cloud/gcp` | Thin wrapper delegating to `cloud/gcp.DetermineProjectID()` |
| `httpserverutils.go` | `httputil` | `GetHttpPort` → `httputil.GetPort`, `SetupStaticServer` → `httputil.NewStaticServer`, `SetJSonHeader` → `httputil.SetJSONHeader`, `SetContentSecurityPolicy`/`SetPermissionPolicy` → `httputil.SecurityHeadersMiddleware` |
| `otap.go` | `config` | `GetOtap()` → `config.Config().GetEnvironment()` (reads `ENVIRONMENT`, falls back to `OTAP`) |
| `robots.go` | `httputil` | `InitRestrictiveRobotsTxt` → `httputil.RestrictiveRobotsTxtHandler` |
| `staticcache.go` | `httputil` | `SetupStaticCache` / `SetCacheHeader` → `httputil.SetCacheControl` |
| `logging/` (entire package) | `log/slog` + `telemetry` | `logging.Log` → `slog.Info`/`slog.Error`, `logging.Trace` → `telemetry.GetTraceID`, `logging.InitLogging` → `slog.SetDefault` + `telemetry.NewContextHandler` |

---

## Proposed Changes — Phase 1

### `config` — Configuration Management

#### [NEW] [config/config.go](file:///home/kristofer/Development/goutils/config/config.go)

Port kvittoburken.se's `ConfigManager` pattern, made generic.

```go
// ConfigManager provides centralized, testable access to environment configuration.
type ConfigManager struct { lookup func(key string) string }

func Config() *ConfigManager             // Global singleton (os.Getenv)
func NewConfigManager(fn) *ConfigManager // For testing with custom lookup

func (c *ConfigManager) GetSecret(key string) string
func (c *ConfigManager) GetPort() string            // PORT, default "8080"
func (c *ConfigManager) GetEnvironment() string     // ENVIRONMENT, fallback OTAP, default "dev"
func (c *ConfigManager) ValidateEnvironment() error // must be dev/stage/prod
func (c *ConfigManager) GetLogLevel() string        // LOG_LEVEL
```

Only shared env var constants live here (`PORT`, `ENVIRONMENT`, `LOG_LEVEL`). App-specific constants stay in each consumer.

---

### `version` — Build Information

#### [NEW] [version/version.go](file:///home/kristofer/Development/goutils/version/version.go)

From kvittoburken.se's `common/version.go`:

```go
func GetGitCommit() string  // VCS revision from debug.ReadBuildInfo, "dev" if unavailable
func GetCommitTime() string // VCS time (RFC3339)
func IsDirty() bool         // VCS modified flag
```

---

### `telemetry` — Observability & Request Context

#### [NEW] [telemetry/context.go](file:///home/kristofer/Development/goutils/telemetry/context.go)

Context-propagated request metadata. From kvittoburken.se's `telemetry` package.

```go
// slog handler that auto-injects trace/session/user from context
type ContextHandler struct { Next slog.Handler }

// Middleware: extracts/generates Trace-ID, Session-ID, client IP, user-agent → context
func TracingMiddleware(next http.Handler) http.Handler

// Context getters
func GetTraceID(ctx) string
func GetSessionID(ctx) string
func GetClientIP(ctx) string
func GetUserAgent(ctx) string
func GetUserID(ctx) string
func GetEmail(ctx) string

// Context setters (used by auth middleware)
func WithUser(ctx, userID, email string) context.Context
func WithUserMetadata(ctx, userID, email, name, issuer, oidcSub string) context.Context
```

#### [NEW] [telemetry/ratelimit.go](file:///home/kristofer/Development/goutils/telemetry/ratelimit.go)

```go
type IPRateLimiter struct { ... }
func NewIPRateLimiter(r rate.Limit, b int) *IPRateLimiter
func (i *IPRateLimiter) Middleware(next http.Handler) http.Handler

func GetClientIP(r *http.Request) string    // Trusted-proxy-aware
func IsTrustedProxy(remoteIP string) bool   // TRUSTED_PROXIES env or private/loopback
```

---

### `httputil` — HTTP Server Utilities

#### [NEW] [httputil/httputil.go](file:///home/kristofer/Development/goutils/httputil/httputil.go)

Consolidates goutils' static serving + kvittoburken.se's SPA handler + security headers.

```go
func GetPort() string  // PORT env, default "8080"

// Static / SPA server (replaces both goutils.SetupStaticServer and kvittoburken.se's handleSPA)
type StaticServerConfig struct {
    PublicDir      string   // Root directory
    IndexFile      string   // Default: "index.html"
    SPAFallback    bool     // Serve IndexFile for unknown routes (SPA mode)
    BlockedExts    []string // e.g. [".go", ".env", ".proto"]
    BlockDotFiles  bool     // Block /.git, /.env, etc
    EnableBrotli   bool     // Serve pre-compressed .br files
}
func NewStaticServer(cfg StaticServerConfig) http.Handler

// Security headers middleware
type SecurityConfig struct {
    CSP              string   // Full CSP string, or use CSPBuilder
    HSTS             bool     // Strict-Transport-Security
    HSTSMaxAge       int      // Default 63072000
    PermissionsPolicy string
    ReferrerPolicy   string   // Default "strict-origin-when-cross-origin"
    FrameOptions     string   // Default "SAMEORIGIN"
    COOP             string   // Cross-Origin-Opener-Policy
    CORP             string   // Cross-Origin-Resource-Policy
}
func SecurityHeadersMiddleware(cfg SecurityConfig) func(http.Handler) http.Handler

// CORS middleware
type CORSConfig struct {
    AllowedOrigins  []string // Explicit origins, or ["*"]
    AllowedMethods  []string
    AllowedHeaders  []string
    AllowCredentials bool
    AppDomain       string   // Auto-allow https://<domain>
}
func CORSMiddleware(cfg CORSConfig) func(http.Handler) http.Handler

// Utility
func RestrictiveRobotsTxtHandler() http.HandlerFunc
func SetJSONHeader(w http.ResponseWriter)
func SetCacheControl(w http.ResponseWriter, maxAge int)
```

---

### `storage` — Object Storage Abstraction

#### [NEW] [storage/storage.go](file:///home/kristofer/Development/goutils/storage/storage.go)

Interface + helpers from kvittoburken.se's `storage` package. No cloud-specific imports.

```go
type StorageClient interface {
    GetCurrentRevision(ctx, bucket, object string) (string, error)
    WriteObject(ctx, bucket, object string, data []byte) (string, error)
    WriteRawObject(ctx, bucket, object string, data []byte) (string, error)
    WriteObjectIfRevisionMatch(ctx, bucket, object string, data []byte, revision string) (string, error)
    ReadObject(ctx, bucket, object string) ([]byte, string, error)
    ReadRawObject(ctx, bucket, object string) ([]byte, string, error)
    GetObjectLink(ctx, bucket, object string, duration int, ip string) (string, error)
    GetUploadLink(ctx, bucket, object string, duration int, contentType string) (string, error)
    DeleteObject(ctx, bucket, object string) error
    ListPrefixes(ctx, bucket, prefix, delimiter string) ([]string, error)
    ListObjects(ctx, bucket, prefix string) ([]StorageObject, error)
}

type StorageObject struct { Key string; LastModified time.Time }

var RevisionWriteError = errors.New(...)
var WriteFailedError   = errors.New(...)

func IsBrotliKey(object string) bool
func ContentTypeFromKey(object string) string
func NewStorageClient() (StorageClient, error)
func SetStorageConstructor(fn func() (StorageClient, error))
```

#### [NEW] [storage/compression.go](file:///home/kristofer/Development/goutils/storage/compression.go)

```go
func CompressBrotli(data []byte) ([]byte, error)
func DecompressBrotli(data []byte) ([]byte, error)
```

#### [NEW] [storage/s3/s3.go](file:///home/kristofer/Development/goutils/storage/s3/s3.go)

Build tag: `//go:build !exclude_s3`

Full S3-compatible implementation (AWS SDK v2). Works with AWS S3, Scaleway, MinIO. Ported from kvittoburken.se's `storage/s3`.

```go
func Init(region, endpoint string) error  // Sets storage constructor
func NewS3StorageClient(ctx, region, endpoint string) (storage.StorageClient, error)
```

#### [NEW] [storage/gcs/gcs.go](file:///home/kristofer/Development/goutils/storage/gcs/gcs.go)

Build tag: `//go:build !exclude_gcs`

GCS implementation ported from kvittoburken.se's `publicclouds/gcp/gcpstorage.go`.

```go
func Init() error  // Determines GCP project, sets storage constructor
func NewGCSStorageClient() (storage.StorageClient, error)
```

> [!IMPORTANT]
> **Build-tag naming for provider exclusion.** The consumer excludes a provider by building with `-tags exclude_gcs` or `-tags exclude_s3`. By default (no tags) both are compiled in. This is the inverse of kvittoburken.se's current approach (which uses positive tags `gcp`/`scaleway` with a fallback "compile both"). The exclusion approach is simpler for the common case where you want everything.

#### [NEW] [storage/docstore/docstore.go](file:///home/kristofer/Development/goutils/storage/docstore/docstore.go)

Optimistic-write `Document` pattern from kvittoburken.se's `docstore` package. Uses `storage.StorageClient` and `version.GetGitCommit()`.

```go
type DocumentClient interface {
    ReadFromProto([]byte) error
    GetSchemaMinorVersion() int32
    StampVersion(msg proto.Message, schemaMinor int32, commit string)
}

type Document struct {
    Client               DocumentClient
    Storage              storage.StorageClient
    BucketName, ObjectName string
    Revision             string
    SupportedSchemaMinor int32
    ProtoMsg             proto.Message
    // ...
}

func (d *Document) Update(ctx context.Context, fn func(proto.Message) error) error
func (d *Document) UpdateFromStorage() error
```

---

### `cloud/gcp` — GCP Project Detection

#### [NEW] [cloud/gcp/project.go](file:///home/kristofer/Development/goutils/cloud/gcp/project.go)

Merged from both repos' GCP project detection. Used by `storage/gcs`. Does **not** support a fallback default project (fails with error).

```go
func DetermineProjectID() (string, error) // metadata → ADC → GOOGLE_CLOUD_PROJECT → GCLOUD_PROJECT
func IsRunningOnGCE() bool                // metadata.OnGCE()
```

The existing `gcpproject.go` at root level becomes a deprecated wrapper calling this.

---

### `auth` — OIDC & Identity Management

#### [NEW] [auth/oidc.go](file:///home/kristofer/Development/goutils/auth/oidc.go)

Generic OIDC provider registry and token verification. Ported from kvittoburken.se's `users/auth.go` with the app-specific parts extracted.

```go
// Provider configuration (parsed from JSON)
type AuthProviderConfig struct {
    ID           string   `json:"id"`
    Name         string   `json:"name"`
    Icon         string   `json:"icon,omitempty"`
    Issuer       string   `json:"issuer"`
    ClientID     string   `json:"client_id"`
    SecretName   string   `json:"secret_name,omitempty"`
    ClientSecret string   `json:"client_secret,omitempty"`
    Scopes       []string `json:"scopes,omitempty"`
}

// Initialized provider with verifier
type RegisteredProvider struct {
    Config       AuthProviderConfig
    Provider     *oidc.Provider
    Verifier     *oidc.IDTokenVerifier
    ClientSecret string
}

// Registry of all configured OIDC providers
type OIDCRegistry struct {
    Providers []RegisteredProvider
}

func InitOIDCRegistry(ctx context.Context, configJSON string, secretLookup func(string) string) (*OIDCRegistry, error)
func (r *OIDCRegistry) GetProviderByID(id string) *RegisteredProvider
func (r *OIDCRegistry) GetProviderByClientID(clientID string) *RegisteredProvider
```

#### [NEW] [auth/identity.go](file:///home/kristofer/Development/goutils/auth/identity.go)

Generic authenticated identity (the OIDC-verified "who") — separated from app-specific authorization ("what they may do").

```go
// UserIdentity is the generic, app-agnostic representation of an authenticated user.
// It carries OIDC claims but no application-specific permissions.
type UserIdentity struct {
    ID         string            // App-assigned user ID (may differ from OIDC sub)
    Email      string            // Verified email
    Name       string            // Display name from OIDC claims
    Issuer     string            // OIDC issuer URL
    OIDCSub    string            // OIDC subject identifier
    ProviderID string            // Which registered OIDC provider verified this
    Identities map[string]string // All linked provider→sub mappings
}
```

#### [NEW] [auth/middleware.go](file:///home/kristofer/Development/goutils/auth/middleware.go)

The auth middleware verifies the OIDC token and then delegates to the app's `IdentityResolver` to map claims → app-specific user. This is the key abstraction that keeps auth generic.

```go
// IdentityResolver is implemented by each application to define how OIDC claims
// map to local users. This is where app-specific logic lives:
// - kvittoburken: upsert user, load company permissions
// - lst: upsert user, load organization memberships
type IdentityResolver interface {
    // ResolveIdentity is called after successful OIDC token verification.
    // It receives the verified token claims and provider info.
    // The implementation should:
    //   1. Find or create the local user (upsert)
    //   2. Return a context enriched with the app-specific user value
    // On error, the middleware returns 401/500 to the client.
    ResolveIdentity(ctx context.Context, identity UserIdentity) (context.Context, error)
}

// Convenience: function adapter for IdentityResolver
type IdentityResolverFunc func(ctx context.Context, identity UserIdentity) (context.Context, error)

// AuthMiddleware returns HTTP middleware that:
//   1. Extracts Bearer token from Authorization header
//   2. Verifies against all registered OIDC providers
//   3. Parses standard claims (email, name, sub)
//   4. Calls IdentityResolver to map to app-specific user
//   5. Enriches context with telemetry user metadata
func AuthMiddleware(registry *OIDCRegistry, resolver IdentityResolver) func(http.Handler) http.Handler
```

**How each app uses this:**

```
┌────────────────────────────────────────────────────┐
│                    goutils/auth                    │
│                                                    │
│  OIDC Token ──→ OIDCRegistry.Verify()              │
│       │                                            │
│       ▼                                            │
│  UserIdentity { Email, Name, OIDCSub, ProviderID } │
│       │                                            │
│       ▼                                            │
│  IdentityResolver.ResolveIdentity(ctx, identity)   │
│       │                                            │
└───────┼────────────────────────────────────────────┘
        │
   ┌────┴────────────────────────┐
   │                             │
   ▼                             ▼
kvittoburken.se              lst
┌────────────────┐   ┌─────────────────────┐
│ User {          │   │ User {              │
│   CompanyRights │   │   OrganizationRoles │
│     companyID   │   │     orgID (church)  │
│     role        │   │     role            │
│ }               │   │ }                   │
│                 │   │                     │
│ MayReadCompany  │   │ MayReadOrg          │
│ MayWriteCompany │   │ MayWriteOrg         │
│ IsSiteAdmin     │   │ IsOrgAdmin          │
└────────────────┘   └─────────────────────┘
```

Each app implements `IdentityResolver` roughly like:

```go
// In kvittoburken.se
func (r *KvittoResolver) ResolveIdentity(ctx context.Context, identity auth.UserIdentity) (context.Context, error) {
    user := r.usersClient.GetUserByIdentity(identity.ProviderID, identity.OIDCSub)
    if user == nil {
        user = r.usersClient.GetUserByEmail(identity.Email)
        if user == nil {
            r.usersClient.AddUserWithIdentity(identity.Email, identity.OIDCSub, identity.ProviderID, identity.OIDCSub)
            user = r.usersClient.GetUserByID(identity.OIDCSub)
        } else {
            r.usersClient.LinkUserIdentity(user.ID, identity.ProviderID, identity.OIDCSub)
        }
    }
    return WithKvittoUser(ctx, user), nil  // app-specific context key
}

// In lst
func (r *LSTResolver) ResolveIdentity(ctx context.Context, identity auth.UserIdentity) (context.Context, error) {
    user := r.usersClient.GetUserByIdentity(identity.ProviderID, identity.OIDCSub)
    if user == nil { ... } // similar upsert, but with OrganizationRoles instead of CompanyRoles
    return WithLSTUser(ctx, user), nil
}
```

> [!NOTE]
> The upsert logic (find by identity → find by email → create → link) is nearly identical in both apps. We could provide a helper in goutils that takes a `UserStore` interface to further reduce duplication. However, since the user stores themselves are app-specific (different protobuf schemas, different role types), the `IdentityResolver` approach strikes the right balance: OIDC plumbing is shared, user storage stays app-owned.

---

### Backward Compatibility Wrappers

#### [MODIFY] Existing files in goutils root

Each existing exported function/type gets a `// Deprecated:` doc comment and a `go:linkname` or thin delegation to the new package. Examples:

```go
// In otap.go:
// Deprecated: Use config.Config().GetEnvironment() instead.
func GetOtap() string { return config.Config().GetEnvironment() }

// In httpserverutils.go:
// Deprecated: Use httputil.GetPort() instead.
func GetHttpPort() string { return httputil.GetPort() }
```

The `logging` package keeps compiling but each function gets `// Deprecated:` pointing to `log/slog` + `telemetry`.

---

## Implementation Order (Phase 1)

| Step | Package | Dependencies | Effort |
|------|---------|-------------|--------|
| 1 | `config/` | — | S |
| 2 | `version/` | — | S |
| 3 | `telemetry/` | `config/` | M |
| 4 | `httputil/` | `config/`, `telemetry/` | M |
| 5 | `storage/` (interface + compression) | — | S |
| 6 | `storage/s3/` | `storage/` | M |
| 7 | `storage/gcs/` | `storage/`, `cloud/gcp/` | M |
| 8 | `storage/docstore/` | `storage/`, `version/` | S |
| 9 | `cloud/gcp/` | — | S |
| 10 | `auth/` | `telemetry/`, `config/` | M |
| 11 | Deprecation wrappers | All above | S |

**S** = Small (< 2h), **M** = Medium (2–4h)

---

## Phase 2 — Consumer Migration (outline)

### passwordsender.com
- Replace `goutils.InitGCPEnvironment` → `cloud/gcp.DetermineProjectID` (or just drop if only used for logging project)
- Replace `goutils.GetOtap()` → `config.Config().GetEnvironment()`
- Replace `goutils.GetHttpPort()` → `httputil.GetPort()`
- Replace `goutils.SetupStaticCache()` + `SetCacheHeader` → `httputil.SetCacheControl`
- Replace custom `withIndexHTML` → `httputil.NewStaticServer` with SPAFallback
- Replace custom security headers → `httputil.SecurityHeadersMiddleware`
- Replace `goutils/logging` → `log/slog` + `telemetry.NewContextHandler`
- Replace custom `GetRequestIP` → `telemetry.GetClientIP`
- Replace custom `handleCors` → `httputil.CORSMiddleware`

### kvittoburken.se
- Replace local `common/config.go` → `goutils/config` (extend with app-specific getters)
- Replace local `common/version.go` → `goutils/version`
- Replace local `telemetry/` → `goutils/telemetry`
- Replace local `storage/` → `goutils/storage`
- Replace local `storage/s3/` → `goutils/storage/s3`
- Replace local `publicclouds/gcp/gcpstorage.go` → `goutils/storage/gcs`
- Replace local `publicclouds/gcp/gcpproject.go` → `goutils/cloud/gcp`
- Replace local `docstore/` → `goutils/storage/docstore`
- Replace local `users/auth.go` OIDCRegistry → `goutils/auth` + implement `IdentityResolver`
- Keep: `users/users.go` (User struct, CompanyRights, UsersClient, protobuf schema)
- Keep: all domain packages (companies, accounting, banking, files, siteadmin, i18n, audit)
- Provider registry (`publicclouds/registry.go`) → each app uses build tags directly

### lst
- Add `goutils` dependency
- `config.Config().GetPort()` replaces inline env reading
- `httputil.NewStaticServer(...)` replaces inline SPA handler
- `httputil.SecurityHeadersMiddleware(...)` adds security headers
- `goutils/auth` + `IdentityResolver` for OIDC login
- `goutils/storage` + `goutils/storage/s3` + `goutils/storage/docstore` for data persistence
- Define lst-specific User struct with `OrganizationRoles` and implement `IdentityResolver`
- Can drop custom `loadDotEnv()` if `config` supports file-based lookup (or keep as app-specific)

---

## Phase 3 — Legacy Removal (outline)

After all consumers are migrated and tested:

| Remove | Replaced by |
|---|---|
| `gcpproject.go` | `cloud/gcp/` |
| `httpserverutils.go` | `httputil/` |
| `otap.go` | `config/` |
| `robots.go` | `httputil/` |
| `staticcache.go` | `httputil/` |
| `logging/` (entire package) | `log/slog` + `telemetry/` |
| `goutils_test.go`, `gcplogging_test.go` | New per-package tests |
| `gorilla/mux` dependency from goutils | Consumers bring their own router |
| `lpar/gzipped/v2` dependency | `httputil` uses own brotli logic |
| `golang.org/x/oauth2` + `google.golang.org/api/compute` | Only needed in `cloud/gcp` |

---

## Test Coverage Plan

Every new package gets a `_test.go` file with the following coverage:

### `config/config_test.go`
- Default values: `GetPort()` returns `"8080"`, `GetEnvironment()` returns `"dev"` when no env set
- Custom lookup: `NewConfigManager(fn)` with mock lookup returns correct values
- `OTAP` fallback: when `ENVIRONMENT` is empty but `OTAP=prod`, `GetEnvironment()` returns `"prod"`
- `ENVIRONMENT` takes priority: when both set, `ENVIRONMENT` wins
- `ValidateEnvironment()`: succeeds for dev/stage/prod, errors for invalid values
- Empty string handling: `GetSecret("")` returns `""`

### `version/version_test.go`
- `GetGitCommit()` returns non-empty string (at minimum `"dev"` or actual commit)
- Repeated calls return the same value (sync.Once correctness)
- `GetCommitTime()` returns string (may be empty in test, but doesn't panic)
- `IsDirty()` returns bool without panic

### `telemetry/context_test.go`
- Round-trip: `WithUser(ctx, id, email)` → `GetUserID(ctx)` returns `id`, `GetEmail(ctx)` returns `email`
- `WithUserMetadata` injects all fields correctly
- Empty context: all getters return `""` on `context.Background()`
- `ContextHandler`: log records include trace/session/user when present in context
- `ContextHandler`: log records omit fields when not in context
- `TracingMiddleware`: generates trace ID when none in request headers
- `TracingMiddleware`: propagates existing `X-Cloud-Trace-Context` header
- `TracingMiddleware`: sets `Trace-ID` response header

### `telemetry/ratelimit_test.go`
- `NewIPRateLimiter(10, 1)`: first request allowed, rapid burst blocked
- Different IPs get independent limits
- `GetClientIP`: returns `X-Forwarded-For` first entry when peer is trusted proxy
- `GetClientIP`: returns remote address when peer is NOT trusted proxy (ignores XFF)
- `GetClientIP`: handles IPv6 addresses correctly
- `IsTrustedProxy`: loopback (`127.0.0.1`, `::1`) is trusted by default
- `IsTrustedProxy`: private ranges (`10.x`, `192.168.x`) are trusted by default
- `IsTrustedProxy`: public IPs are not trusted by default
- `Middleware`: returns 429 with `Retry-After` header when rate exceeded

### `httputil/httputil_test.go`
- `GetPort()`: defaults to `"8080"`, reads `PORT` env
- `NewStaticServer`: serves existing file with correct content
- `NewStaticServer`: SPA fallback serves `index.html` for unknown routes when enabled
- `NewStaticServer`: returns 404 for blocked extensions (`.go`, `.env`)
- `NewStaticServer`: blocks dotfiles when configured (`.git/`, `.env`)
- `NewStaticServer`: serves pre-compressed `.br` file with `Content-Encoding: br` when `Accept-Encoding` matches
- `SecurityHeadersMiddleware`: sets all configured headers (`X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, `Permissions-Policy`, CSP, HSTS)
- `SecurityHeadersMiddleware`: HSTS only set when request is HTTPS (TLS or `X-Forwarded-Proto`)
- `CORSMiddleware`: allowed origin gets `Access-Control-Allow-Origin` header
- `CORSMiddleware`: disallowed origin gets no CORS headers
- `CORSMiddleware`: preflight `OPTIONS` returns 204 with correct headers
- `CORSMiddleware`: `AllowCredentials=true` sets `Access-Control-Allow-Credentials`
- `RestrictiveRobotsTxtHandler`: returns `User-agent: *\nDisallow: /` with `text/plain` content type
- `SetJSONHeader`: sets `Content-Type: application/json; charset=utf-8`
- `SetCacheControl`: sets `Cache-Control: max-age=N`

### `storage/storage_test.go`
- `IsBrotliKey`: `"data.pb.br"` → true, `"data.pb"` → false
- `ContentTypeFromKey`: `.pb` → `application/x-protobuf`, `.json` → `application/json`, `.pdf` → `application/pdf`, `.png` → `image/png`, `.jpg`/`.jpeg` → `image/jpeg`, `.html` → `text/html`, unknown → `application/octet-stream`
- `ContentTypeFromKey` strips `.br`: `"data.pb.br"` → `application/x-protobuf`
- `SetStorageConstructor` / `NewStorageClient`: round-trip works
- `NewStorageClient` without constructor returns error

### `storage/compression_test.go`
- Compress → decompress round-trip: output matches input
- Empty input: both functions return nil without error
- Large payload (1MB): compresses to smaller size, decompresses back correctly
- Corrupted compressed data: `DecompressBrotli` returns error

### `storage/s3/s3_test.go`
- Uses mock S3 client (interface-based) or `s3mem` in-memory backend
- `WriteObject` → `ReadObject` round-trip
- `WriteRawObject` → `ReadRawObject` round-trip (no compression)
- `WriteObject` with `.br` key: data is stored compressed, read back decompressed
- `WriteObjectIfRevisionMatch` with correct revision: succeeds
- `WriteObjectIfRevisionMatch` with wrong revision: returns `RevisionWriteError`
- `WriteObjectIfRevisionMatch` with empty revision on non-existent object: succeeds
- `DeleteObject` → `ReadObject` returns not-found error
- `ListObjects` returns expected keys
- `ListPrefixes` returns expected prefixes with delimiter
- `GetCurrentRevision` returns non-empty string after write
- `MaxReadObjectSizeBytes` guard: reading oversized object returns `ErrObjectTooLarge`

### `storage/gcs/gcs_test.go`
- Same test matrix as S3 but against GCS mock/emulator
- Build tag `//go:build !exclude_gcs` verified: file excluded with `-tags exclude_gcs`
- `Init()` sets the storage constructor correctly

### `storage/docstore/docstore_test.go`
- Port kvittoburken.se's existing `docstore_version_guard_test.go`:
  - `Update` succeeds when data schema minor ≤ supported minor
  - `Update` returns `ErrNewerSchemaVersionWriteForbidden` when data minor > supported
- Additional tests:
  - `Update` with concurrent modifications: retries and eventually succeeds
  - `Update` on non-existent document: creates it (empty revision path)
  - `UpdateFromStorage` loads data correctly
  - `StampVersion` is called with correct commit hash and schema minor
  - `Update` fails after 100 retries with `WriteFailedError`
  - Mock `StorageClient` used throughout (same pattern as kvittoburken's test)

### `cloud/gcp/project_test.go`
- With `GOOGLE_CLOUD_PROJECT` env: returns that project ID
- With `GCLOUD_PROJECT` env: returns that project ID
- Priority: metadata > ADC > `GOOGLE_CLOUD_PROJECT` > `GCLOUD_PROJECT`
- No credentials and no env: returns error
- `IsRunningOnGCE()`: returns bool without panic (actual value depends on environment)

### `auth/oidc_test.go`
- `InitOIDCRegistry` with valid JSON config: succeeds, providers registered
- `InitOIDCRegistry` with empty JSON: returns error
- `InitOIDCRegistry` with missing required fields (id/issuer/client_id): returns error
- `GetProviderByID`: returns correct provider, nil for unknown
- `GetProviderByClientID`: returns correct provider, nil for unknown
- `SecretName` resolution: when `secret_name` is set, calls secret lookup function

### `auth/middleware_test.go`
- No `Authorization` header: returns 401
- Invalid Bearer token: returns 401
- Valid token from registered provider: calls `IdentityResolver`, request proceeds
- `IdentityResolver` returns error: returns 500
- No providers registered: returns 401 with descriptive message
- `UserIdentity` passed to resolver has correct Email, Name, OIDCSub, ProviderID
- Telemetry context enriched after successful auth (user ID, email in context)
- Uses mock OIDC provider/verifier for token verification

### Backward Compatibility Tests
- Existing `goutils_test.go` and `gcplogging_test.go` must continue passing unchanged
- `GetOtap()` still works (delegates to `config`)
- `GetHttpPort()` still works (delegates to `httputil`)
- `InitGCPEnvironment(defaultProj)` still works (delegates to `cloud/gcp`, uses default as fallback for legacy compat)
- `SetCacheHeader`, `SetJSonHeader`, `InitRestrictiveRobotsTxt`, `SetupStaticServer` all still work

---

## Verification Plan

### Phase 1 — Run all tests
```bash
cd /home/kristofer/Development/goutils && go test -v -count=1 ./...
```

### Phase 1 — Build-tag verification
```bash
# Exclude GCS, only S3
go build -tags exclude_gcs ./...
go test -tags exclude_gcs ./...
# Exclude S3, only GCS  
go build -tags exclude_s3 ./...
go test -tags exclude_s3 ./...
# Both providers (default)
go build ./...
go test ./...
```

### Phase 1 — Backward compat check
```bash
cd /home/kristofer/Development/passwordsender.com && go get github.com/tingdahl/goutils@latest && go test ./...
```
Must compile and pass without any changes to passwordsender.com.

### Phase 2 — Per-consumer
```bash
cd /home/kristofer/Development/passwordsender.com && go test ./...
cd /home/kristofer/Development/kvittoburken.se/server && go test ./...
cd /home/kristofer/Development/lst/server && go test ./...
```

### Docker builds
All three Dockerfiles must continue producing working images after migration.
