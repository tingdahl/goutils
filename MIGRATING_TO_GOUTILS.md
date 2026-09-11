# Migrating to `github.com/tingdahl/goutils` v2 Shared Packages

> **Context:** The `goutils` library has been expanded with shared infrastructure packages
> used across all our Go services. This document explains how to migrate your service
> to use the new packages instead of local/legacy equivalents.
>
> **Prerequisite:** Update your `go.mod` dependency:
> ```bash
> go get github.com/tingdahl/goutils@latest
> ```

---

## Quick Reference: What Replaced What

| Old (local / legacy) | New (goutils) | Package |
|---|---|---|
| `os.Getenv("PORT")` / `goutils.GetHttpPort()` | `config.Config().GetPort()` or `httputil.GetPort()` | `goutils/config`, `goutils/httputil` |
| `os.Getenv("ENVIRONMENT")` / `goutils.GetOtap()` | `config.Config().GetEnvironment()` | `goutils/config` |
| `os.Getenv("OTAP")` | `config.Config().GetEnvironment()` (reads both) | `goutils/config` |
| `os.Getenv("KEY")` | `config.Config().GetSecret("KEY")` | `goutils/config` |
| `goutils/logging` (custom JSON logger) | `log/slog` + `telemetry.ContextHandler` | `goutils/telemetry` |
| `goutils.SetupStaticServer(router, dir, file)` | `httputil.NewStaticServer(cfg)` | `goutils/httputil` |
| `goutils.SetCacheHeader(w)` | `httputil.SetCacheControl(w, maxAge)` | `goutils/httputil` |
| `goutils.SetJSonHeader(w)` | `httputil.SetJSONHeader(w)` | `goutils/httputil` |
| `goutils.InitRestrictiveRobotsTxt(router)` | `httputil.RestrictiveRobotsTxtHandler()` | `goutils/httputil` |
| `goutils.SetContentSecurityPolicy(...)` | `httputil.SecurityHeadersMiddleware(cfg)` | `goutils/httputil` |
| `goutils.InitGCPEnvironment(proj)` | `gcp.DetermineProjectID()` | `goutils/cloud/gcp` |
| Custom CORS handler | `httputil.CORSMiddleware(cfg)` | `goutils/httputil` |
| Custom `GetRequestIP(r)` | `telemetry.GetClientIP(r)` | `goutils/telemetry` |
| Local `StorageClient` interface | `storage.StorageClient` | `goutils/storage` |
| Local `storage/s3` package | `goutils/storage/s3` | `goutils/storage/s3` |
| Local GCS storage implementation | `goutils/storage/gcs` | `goutils/storage/gcs` |
| Local `docstore` package | `goutils/storage/docstore` | `goutils/storage/docstore` |
| Local `OIDCRegistry` + `AuthMiddleware` | `auth.OIDCRegistry` + `auth.AuthMiddleware` | `goutils/auth` |
| Local `CompressBrotli` / `DecompressBrotli` | `storage.CompressBrotli` / `storage.DecompressBrotli` | `goutils/storage` |
| Local `GetGitCommit()` / build info | `version.GetGitCommit()` | `goutils/version` |

---

## 1. Configuration (`goutils/config`)

### Before
```go
// Inline env reading (lst pattern)
port := os.Getenv("PORT")
if port == "" {
    port = "8080"
}

// Or goutils legacy (passwordsender pattern)
port := goutils.GetHttpPort()
otap := goutils.GetOtap()
```

### After
```go
import "github.com/tingdahl/goutils/config"

port := config.Config().GetPort()          // PORT env, default "8080"
env := config.Config().GetEnvironment()    // ENVIRONMENT env, fallback OTAP, default "dev"

// Read any env var with consistent API
apiKey := config.Config().GetSecret("API_KEY")
```

### App-Specific Config
If your app has many config values, extend `ConfigManager` in your own code:

```go
// In your app's config package
import "github.com/tingdahl/goutils/config"

func GetAblyKey() string     { return config.Config().GetSecret("ABLY_API_KEY") }
func GetDeepgramKey() string { return config.Config().GetSecret("DEEPGRAM_API_KEY") }
```

---

## 2. Logging (`log/slog` + `goutils/telemetry`)

### Before
```go
import "github.com/tingdahl/goutils/logging"

logging.InitLogging(gcpProject)

if logging.ShouldLog(logging.Info) {
    logging.Log("Starting server", logging.Info)
}

if logging.ShouldLog(logging.Error) {
    logging.LogTrace(err.Error(), logging.Error, request)
}
```

### After
```go
import (
    "log/slog"
    "os"
    "github.com/tingdahl/goutils/telemetry"
)

// In main():
slog.SetDefault(slog.New(&telemetry.ContextHandler{
    Next: slog.NewTextHandler(os.Stderr, nil),
}))

// Then just use slog everywhere:
slog.Info("Starting server")
slog.Error("Something failed", "error", err)

// In HTTP handlers, use context for automatic trace correlation:
slog.InfoContext(r.Context(), "Processing request", "path", r.URL.Path)
slog.ErrorContext(r.Context(), "Failed", "error", err)
```

> **Key benefit:** `ContextHandler` automatically adds trace ID, session ID,
> client IP, and user info to every log line when present in context.
> No manual `ShouldLog` checks needed — slog handles levels natively.

---

## 3. HTTP Server Setup (`goutils/httputil`)

### Before — Static file serving
```go
// passwordsender.com pattern
goutils.SetupStaticServer(router, "./client/public/", "index.html")

// lst pattern (inline SPA)
fs := http.FileServer(http.Dir(staticDir))
router.PathPrefix("/").Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    path := filepath.Join(staticDir, filepath.Clean(r.URL.Path))
    if info, err := os.Stat(path); os.IsNotExist(err) || info.IsDir() {
        http.ServeFile(w, r, filepath.Join(staticDir, "index.html"))
        return
    }
    fs.ServeHTTP(w, r)
}))
```

### After — Static file serving
```go
import "github.com/tingdahl/goutils/httputil"

staticHandler := httputil.NewStaticServer(httputil.StaticServerConfig{
    PublicDir:     "./client/public",
    IndexFile:     "index.html",
    SPAFallback:   true,                                        // Serve index.html for unknown routes
    BlockedExts:   []string{".go", ".env", ".proto", ".sh"},    // Block sensitive files
    BlockDotFiles: true,                                         // Block /.git, /.env, etc.
    EnableBrotli:  true,                                         // Serve .br pre-compressed files
})

// With gorilla/mux:
router.PathPrefix("/").Handler(staticHandler)

// With stdlib ServeMux:
mux.Handle("/", staticHandler)
```

### Before — Security headers
```go
// Manual in every handler or a custom wrapper
w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
w.Header().Set("Content-Security-Policy", csp)
w.Header().Set("X-Frame-Options", "SAMEORIGIN")
w.Header().Set("Referrer-Policy", "no-referrer")
w.Header().Set("X-Content-Type-Options", "nosniff")
w.Header().Set("Permissions-Policy", permissionPolicy)
```

### After — Security headers
```go
securityMw := httputil.SecurityHeadersMiddleware(httputil.SecurityConfig{
    CSP:               "default-src 'self'; script-src 'self'",
    HSTS:              true,
    HSTSMaxAge:        63072000,
    PermissionsPolicy: "geolocation=(), camera=(), microphone=()",
    ReferrerPolicy:    "strict-origin-when-cross-origin",
    FrameOptions:      "SAMEORIGIN",
    COOP:              "same-origin-allow-popups",
    CORP:              "same-origin",
})

// Wrap your entire handler:
http.ListenAndServe(":"+port, securityMw(router))
```

### Before — CORS
```go
// Custom per-endpoint
func handleCors(w http.ResponseWriter, request *http.Request) bool {
    if request.Method == http.MethodOptions {
        origin := request.Header.Get("Origin")
        if origin == "https://mysite.com" || strings.HasSuffix(origin, ".mysite.com") {
            w.Header().Set("Access-Control-Allow-Origin", origin)
            // ...
        }
        return true
    }
    return false
}
```

### After — CORS
```go
corsMw := httputil.CORSMiddleware(httputil.CORSConfig{
    AllowedOrigins:   []string{"https://mysite.com"},
    AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
    AllowedHeaders:   []string{"Content-Type", "Authorization"},
    AllowCredentials: true,
    AppDomain:        "mysite.com",  // Auto-allows https://mysite.com
})

http.ListenAndServe(":"+port, corsMw(securityMw(router)))
```

### Before — Robots.txt
```go
goutils.InitRestrictiveRobotsTxt(router)
```

### After — Robots.txt
```go
// With gorilla/mux:
router.HandleFunc("/robots.txt", httputil.RestrictiveRobotsTxtHandler())

// With stdlib ServeMux:
mux.HandleFunc("GET /robots.txt", httputil.RestrictiveRobotsTxtHandler())
```

---

## 4. Telemetry & Request Context (`goutils/telemetry`)

### Before — Client IP extraction
```go
// passwordsender.com's custom implementation
func GetRequestIP(request *http.Request) (string, error) {
    urlString := request.Header.Get("X-FORWARDED-FOR")
    if urlString == "" {
        urlString = request.RemoteAddr
    } else {
        parts := strings.Split(urlString, ",")
        urlString = strings.TrimSpace(parts[0])
    }
    return StripPort(urlString)
}
```

### After — Client IP extraction
```go
import "github.com/tingdahl/goutils/telemetry"

ip := telemetry.GetClientIP(request)  // Trusted-proxy-aware, handles IPv6
```

### Adding Tracing Middleware
```go
// Add to your middleware chain — injects trace ID, session ID, client IP into context
handler := telemetry.TracingMiddleware(router)
```

### Rate Limiting
```go
limiter := telemetry.NewIPRateLimiter(10, 20) // 10 req/sec, burst of 20
handler := limiter.Middleware(router)
```

---

## 5. Object Storage (`goutils/storage`)

### Before (kvittoburken.se local packages)
```go
import (
    "bokforing/storage"
    "bokforing/storage/s3"
    "bokforing/publicclouds/gcp"
)
```

### After
```go
import (
    "github.com/tingdahl/goutils/storage"
    "github.com/tingdahl/goutils/storage/s3"   // S3-compatible (AWS, Scaleway, MinIO)
    "github.com/tingdahl/goutils/storage/gcs"  // Google Cloud Storage
)

// Initialize (pick one provider):
s3.Init(region, endpoint)   // For S3-compatible
// or
gcs.Init()                  // For GCS

// Then use the interface:
client, err := storage.NewStorageClient()
data, revision, err := client.ReadObject(ctx, bucket, "myfile.pb.br")
newRevision, err := client.WriteObjectIfRevisionMatch(ctx, bucket, "myfile.pb.br", data, revision)
```

### Build-Tag Provider Exclusion
```bash
# Exclude GCS to save binary size (only compile S3 provider)
go build -tags exclude_gcs ./...

# Exclude S3 (only compile GCS provider)
go build -tags exclude_s3 ./...
```

---

## 6. Document Store (`goutils/storage/docstore`)

### Before (kvittoburken.se local package)
```go
import "bokforing/docstore"
```

### After
```go
import "github.com/tingdahl/goutils/storage/docstore"
```

The API is identical. Your `DocumentClient` implementations (with `ReadFromProto`, `GetSchemaMinorVersion`, `StampVersion`) work unchanged.

---

## 7. OIDC Authentication (`goutils/auth`)

### Before (kvittoburken.se — all-in-one in users/auth.go)
```go
// Everything in one package: OIDC config, token verification, user upsert, user context
registry, err := users.InitOIDCRegistry(ctx, configJSON)
handler := users.AuthMiddleware(auditLogger, registry)(next)
user := users.GetUser(r.Context())
```

### After — Two-layer approach
```go
import "github.com/tingdahl/goutils/auth"

// 1. Initialize the OIDC registry (same JSON config format)
registry, err := auth.InitOIDCRegistry(ctx, configJSON, config.Config().GetSecret)

// 2. Implement IdentityResolver in YOUR app (this is where app-specific user logic lives)
type MyResolver struct {
    usersClient *users.UsersClient
}

func (r *MyResolver) ResolveIdentity(ctx context.Context, identity auth.UserIdentity) (context.Context, error) {
    // Find or create user in your app's user store
    user := r.usersClient.GetUserByIdentity(identity.ProviderID, identity.OIDCSub)
    if user == nil {
        user = r.usersClient.GetUserByEmail(identity.Email)
        if user == nil {
            // Create new user
            r.usersClient.AddUserWithIdentity(identity.Email, identity.OIDCSub,
                identity.ProviderID, identity.OIDCSub)
            user = r.usersClient.GetUserByID(identity.OIDCSub)
        } else {
            // Link new provider identity to existing user
            r.usersClient.LinkUserIdentity(user.ID, identity.ProviderID, identity.OIDCSub)
        }
    }
    // Put YOUR app-specific user type into context
    return users.WithUser(ctx, user), nil
}

// 3. Wire it up
resolver := &MyResolver{usersClient: users.GetUsersClient()}
authMw := auth.AuthMiddleware(registry, resolver)
mux.Handle("/api/", authMw(apiHandler))
```

### What Stays in Your App
- Your `User` struct with app-specific fields (`CompanyRights`, `OrganizationRoles`, etc.)
- Your `UsersClient` / user storage (protobuf schema, docstore-backed)
- Your permission methods (`MayReadCompany`, `MayWriteOrg`, `IsAdmin`, etc.)
- Your `WithUser(ctx, user)` / `GetUser(ctx)` context helpers
- Your `IdentityResolver` implementation

### What Moves to goutils
- `OIDCRegistry`, `AuthProviderConfig`, `RegisteredProvider`
- `InitOIDCRegistry` (JSON parsing, provider discovery, verifier setup)
- `AuthMiddleware` (token extraction, verification, claim parsing)
- `UserIdentity` (generic OIDC identity — email, name, sub, provider)

---

## 8. Build Version Info (`goutils/version`)

### Before
```go
// kvittoburken.se local implementation
import "bokforing/common"

commit := common.GetGitCommit()
```

### After
```go
import "github.com/tingdahl/goutils/version"

commit := version.GetGitCommit()    // Git revision hash, or "dev"
buildTime := version.GetCommitTime() // RFC3339 timestamp
dirty := version.IsDirty()           // Was worktree clean at build?
```

---

## Migration Checklist

Use this checklist to track your migration progress:

- [ ] Update `go.mod`: `go get github.com/tingdahl/goutils@latest`
- [ ] Replace config/env reading → `goutils/config`
- [ ] Replace logging → `log/slog` + `goutils/telemetry`
- [ ] Replace static file serving → `goutils/httputil`
- [ ] Replace security headers → `httputil.SecurityHeadersMiddleware`
- [ ] Replace CORS handling → `httputil.CORSMiddleware`
- [ ] Replace robots.txt → `httputil.RestrictiveRobotsTxtHandler`
- [ ] Replace IP extraction → `telemetry.GetClientIP`
- [ ] Replace storage interface → `goutils/storage`
- [ ] Replace storage provider → `goutils/storage/s3` or `goutils/storage/gcs`
- [ ] Replace docstore → `goutils/storage/docstore`
- [ ] Replace OIDC auth → `goutils/auth` + implement `IdentityResolver`
- [ ] Replace version/build info → `goutils/version`
- [ ] Remove local packages that are now in goutils
- [ ] Run `go test ./...`
- [ ] Docker build succeeds
- [ ] Verify security headers in browser dev tools
- [ ] Verify CORS works for your allowed origins
