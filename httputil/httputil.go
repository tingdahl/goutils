package httputil

import (
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tingdahl/goutils/config"
	"github.com/tingdahl/goutils/storage"
)

func init() {
	_ = mime.AddExtensionType(".js", "application/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".mjs", "application/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".wasm", "application/wasm")
	_ = mime.AddExtensionType(".json", "application/json; charset=utf-8")
	_ = mime.AddExtensionType(".css", "text/css; charset=utf-8")
	_ = mime.AddExtensionType(".svg", "image/svg+xml")
}

// GetPort returns the HTTP port from configuration.
func GetPort() string {
	return config.Config().GetPort()
}

// SetJSONHeader sets the Content-Type header to application/json; charset=utf-8.
func SetJSONHeader(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
}

// SetCacheControl sets the Cache-Control header on the response writer.
func SetCacheControl(w http.ResponseWriter, directive string) {
	w.Header().Set("Cache-Control", directive)
}

// RestrictiveRobotsTxtHandler returns an HTTP handler that serves a restrictive robots.txt.
func RestrictiveRobotsTxtHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write([]byte("User-agent: *\nDisallow: /\n"))
	})
}

// CORSOptions configures the CORS middleware.
type CORSOptions struct {
	AllowedOrigins   []string
	AllowAllOrigins  bool
	AllowedMethods   []string
	AllowedHeaders   []string
	AllowCredentials bool
}

// DefaultCORSOptions returns sensible defaults for CORS.
func DefaultCORSOptions() CORSOptions {
	return CORSOptions{
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization", "X-Session-ID", "Trace-ID", "X-Cron-Key", "X-API-Key"},
		AllowCredentials: true,
	}
}

// CORSMiddleware creates a middleware that handles CORS headers and preflight OPTIONS requests.
func CORSMiddleware(opts CORSOptions) func(http.Handler) http.Handler {
	if len(opts.AllowedMethods) == 0 {
		opts.AllowedMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	}
	if len(opts.AllowedHeaders) == 0 {
		opts.AllowedHeaders = []string{"Content-Type", "Authorization", "X-Session-ID", "Trace-ID", "X-Cron-Key", "X-API-Key"}
	}
	methodsHeader := strings.Join(opts.AllowedMethods, ", ")
	headersHeader := strings.Join(opts.AllowedHeaders, ", ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			var originAllowed bool
			var allowCreds bool

			if origin != "" {
				if opts.AllowAllOrigins {
					originAllowed = true
					allowCreds = false
				} else {
					originAllowed, allowCreds = checkAllowedOrigin(origin, r.Host, opts)
				}

				if originAllowed {
					if allowCreds {
						w.Header().Set("Access-Control-Allow-Origin", origin)
						w.Header().Set("Access-Control-Allow-Credentials", "true")
					} else {
						w.Header().Set("Access-Control-Allow-Origin", "*")
					}
					w.Header().Set("Access-Control-Allow-Methods", methodsHeader)
					w.Header().Set("Access-Control-Allow-Headers", headersHeader)
					w.Header().Set("Vary", "Origin")
				}
			}

			if r.Method == http.MethodOptions {
				if origin != "" && !originAllowed {
					http.Error(w, "Forbidden", http.StatusForbidden)
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func checkAllowedOrigin(origin string, reqHost string, opts CORSOptions) (bool, bool) {
	for _, o := range opts.AllowedOrigins {
		trimmed := strings.TrimSpace(o)
		if trimmed == "*" {
			return true, false
		}
		if trimmed == origin {
			return true, opts.AllowCredentials
		}
	}

	// Check environment variable
	if allowedEnv := config.Config().GetCORSAllowedOrigins(); allowedEnv != "" {
		for _, o := range strings.Split(allowedEnv, ",") {
			trimmed := strings.TrimSpace(o)
			if trimmed == "*" {
				return true, false
			}
			if trimmed == origin {
				return true, opts.AllowCredentials
			}
		}
	}

	// Check APP_DOMAIN
	if appDomain := config.Config().GetAppDomain(); appDomain != "" {
		if origin == "https://"+appDomain || origin == "http://"+appDomain {
			return true, opts.AllowCredentials
		}
	}

	// Check same host origin
	if parsed, err := url.Parse(origin); err == nil {
		if strings.EqualFold(parsed.Host, reqHost) {
			return true, opts.AllowCredentials
		}
	}

	return false, false
}

// SecurityHeadersConfig configures defensive HTTP security headers.
type SecurityHeadersConfig struct {
	CSP               string
	PermissionsPolicy string
	HSTS              bool
	HSTSMaxAge        int
}

// DefaultSecurityHeadersConfig returns standard security header defaults.
func DefaultSecurityHeadersConfig() SecurityHeadersConfig {
	return SecurityHeadersConfig{
		PermissionsPolicy: "geolocation=(), camera=(), microphone=()",
		HSTS:              true,
		HSTSMaxAge:        63072000,
	}
}

// SecurityHeadersMiddleware creates a middleware setting defensive security headers.
func SecurityHeadersMiddleware(cfg SecurityHeadersConfig) func(http.Handler) http.Handler {
	if cfg.PermissionsPolicy == "" {
		cfg.PermissionsPolicy = "geolocation=(), camera=(), microphone=()"
	}
	if cfg.HSTSMaxAge == 0 {
		cfg.HSTSMaxAge = 63072000
	}
	hstsValue := fmt.Sprintf("max-age=%d; includeSubDomains; preload", cfg.HSTSMaxAge)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "SAMEORIGIN")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			w.Header().Set("Permissions-Policy", cfg.PermissionsPolicy)
			w.Header().Set("Cross-Origin-Opener-Policy", "same-origin-allow-popups")
			w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")

			if cfg.HSTS && (r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https") {
				w.Header().Set("Strict-Transport-Security", hstsValue)
			}

			if cfg.CSP != "" {
				w.Header().Set("Content-Security-Policy", cfg.CSP)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// StaticServerConfig configures static asset serving.
type StaticServerConfig struct {
	PublicDir         string
	IndexFile         string
	BlockedExtensions []string
	EnableBrotli      bool
	FallbackToSPA     bool
}

var (
	defaultBlockedExtensions = []string{
		".ts", ".go", ".env", ".pb", ".git", ".bak", ".mod", ".sum", ".yaml", ".yml", ".toml", ".sh", ".sql",
	}
	hashedAssetRegex = regexp.MustCompile(`[-.][0-9a-f]{24}\.(js|mjs|css|svg|json|js\.map|mjs\.map)$`)
)

type staticServer struct {
	publicDir         string
	indexFile         string
	blockedExtensions []string
	enableBrotli      bool
	fallbackToSPA     bool
}

// NewStaticServer creates an http.Handler that securely serves static assets and SPAs.
func NewStaticServer(cfg StaticServerConfig) http.Handler {
	if cfg.IndexFile == "" {
		cfg.IndexFile = "index.html"
	}
	if len(cfg.BlockedExtensions) == 0 {
		cfg.BlockedExtensions = defaultBlockedExtensions
	}

	cleanPublicDir := filepath.Clean(cfg.PublicDir)

	return &staticServer{
		publicDir:         cleanPublicDir,
		indexFile:         cfg.IndexFile,
		blockedExtensions: cfg.BlockedExtensions,
		enableBrotli:      cfg.EnableBrotli,
		fallbackToSPA:     cfg.FallbackToSPA,
	}
}

func (s *staticServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	path := r.URL.Path

	// Reject null bytes
	if strings.Contains(path, "\x00") {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	cleanPath := filepath.Clean(path)
	fullPath := filepath.Join(s.publicDir, cleanPath)

	// Ensure path stays within public directory
	if !strings.HasPrefix(fullPath, s.publicDir) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Block hidden dotfiles and dot-directories (e.g. /.git, /.env, /.DS_Store)
	for _, segment := range strings.Split(cleanPath, "/") {
		if strings.HasPrefix(segment, ".") && segment != "." && segment != ".." {
			http.NotFound(w, r)
			return
		}
	}

	// Block sensitive or backend file extensions
	lower := strings.ToLower(cleanPath)
	for _, ext := range s.blockedExtensions {
		if strings.HasSuffix(lower, ext) {
			http.NotFound(w, r)
			return
		}
	}

	// Try serving a matching .html file for clean/extensionless URLs
	if path != "/" && !strings.Contains(path, ".") && !strings.HasPrefix(path, "/api/") {
		htmlPath := filepath.Join(s.publicDir, cleanPath+".html")
		if strings.HasPrefix(htmlPath, s.publicDir) {
			if fi, err := os.Stat(htmlPath); err == nil && !fi.IsDir() {
				s.serveFile(w, r, htmlPath)
				return
			}
			if s.enableBrotli {
				if fi, err := os.Stat(htmlPath + ".br"); err == nil && !fi.IsDir() {
					s.serveFile(w, r, htmlPath)
					return
				}
			}
		}

		if s.fallbackToSPA {
			s.serveFile(w, r, filepath.Join(s.publicDir, s.indexFile))
			return
		}
	}

	if path == "/" {
		s.serveFile(w, r, filepath.Join(s.publicDir, s.indexFile))
		return
	}

	// Check if directory
	if fi, err := os.Stat(fullPath); err == nil && fi.IsDir() {
		indexPath := filepath.Join(fullPath, s.indexFile)
		if ifi, ierr := os.Stat(indexPath); ierr == nil && !ifi.IsDir() {
			s.serveFile(w, r, indexPath)
			return
		}
	}

	s.serveFile(w, r, fullPath)
}

func (s *staticServer) serveFile(w http.ResponseWriter, r *http.Request, filePath string) {
	supportsBr := s.enableBrotli && strings.Contains(r.Header.Get("Accept-Encoding"), "br")
	brPath := filePath + ".br"

	hasBr := false
	if s.enableBrotli {
		if fi, err := os.Stat(brPath); err == nil && !fi.IsDir() {
			hasBr = true
		}
	}

	hasUncompressed := false
	if fi, err := os.Stat(filePath); err == nil && !fi.IsDir() {
		hasUncompressed = true
	}

	if !hasBr && !hasUncompressed {
		if s.fallbackToSPA && filePath != filepath.Join(s.publicDir, s.indexFile) {
			s.serveFile(w, r, filepath.Join(s.publicDir, s.indexFile))
			return
		}
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Vary", "Accept-Encoding")

	// Set Cache-Control
	base := filepath.Base(filePath)
	if hashedAssetRegex.MatchString(base) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else if strings.HasSuffix(base, ".html") {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}

	// Content-Type
	ext := filepath.Ext(filePath)
	ctype := mime.TypeByExtension(ext)
	if ctype == "" {
		switch ext {
		case ".js", ".mjs":
			ctype = "application/javascript; charset=utf-8"
		case ".json":
			ctype = "application/json; charset=utf-8"
		case ".css":
			ctype = "text/css; charset=utf-8"
		case ".html":
			ctype = "text/html; charset=utf-8"
		case ".svg":
			ctype = "image/svg+xml"
		case ".wasm":
			ctype = "application/wasm"
		default:
			ctype = "application/octet-stream"
		}
	}
	w.Header().Set("Content-Type", ctype)

	if supportsBr && hasBr {
		w.Header().Set("Content-Encoding", "br")
		http.ServeFile(w, r, brPath)
		return
	}

	if hasUncompressed {
		http.ServeFile(w, r, filePath)
		return
	}

	// If only .br exists but client doesn't support br, decompress on the fly
	if hasBr {
		compressedBytes, err := os.ReadFile(brPath)
		if err == nil {
			decompressed, err := storage.DecompressBrotli(compressedBytes)
			if err == nil {
				w.Header().Set("Content-Length", fmt.Sprintf("%d", len(decompressed)))
				w.Write(decompressed)
				return
			}
		}
	}

	http.NotFound(w, r)
}
