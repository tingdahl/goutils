package goutils

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/gorilla/mux"
	"github.com/lpar/gzipped/v2"
	"github.com/tingdahl/goutils/logging"
)

var (
	permissionPolicy      string
	contentSecurityPolicy string
)

// Determine port for HTTP service.
func GetHttpPort() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		if logging.ShouldLog(logging.Info) {
			logging.Log(fmt.Sprintf("Listening to port %s (default)", port), logging.Info)
		}

	} else {
		if logging.ShouldLog(logging.Info) {
			logging.Log(fmt.Sprintf("Listening to port %s (from PORT environment)", port),
				logging.Info)
		}
	}

	return port
}

//Sets CSP to 'self', plus the given default, script, image and style sources
func SetContentSecurityPolicy(defaultSource string, scriptSource string, imageSource string, styleSource string) {

	//Construct CSP from checksums
	contentSecurityPolicy = "default-src 'self' " + defaultSource
	contentSecurityPolicy += "; script-src 'self' " + scriptSource
	contentSecurityPolicy += "; img-src 'self' " + imageSource
	contentSecurityPolicy += "; style-src 'self' " + styleSource
}

func SetPermissionPolicy(pp string) {
	permissionPolicy = pp
}

//Setup server for serving static pages from /client/public
func SetupStaticServer(router *mux.Router, inputDir string, emptyPathFile string) error {

	fs := staticFileHandler(gzipped.FileServer(gzipped.Dir(inputDir)), emptyPathFile)

	router.PathPrefix("/").Handler(fs)
	return nil
}

//Wrap a destination if no path is provided
func staticFileHandler(h http.Handler, emptyPathFile string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {

		if strings.HasSuffix(request.URL.Path, "/") || len(request.URL.Path) == 0 {
			request.URL.Path = path.Join(request.URL.Path, emptyPathFile)
		}

		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		if len(contentSecurityPolicy) > 0 {
			w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		}
		if len(permissionPolicy) > 0 {
			w.Header().Set("Permissions-Policy", permissionPolicy)
		}

		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		SetCacheHeader(w)

		h.ServeHTTP(w, request)
	})
}

//Set Content-Type Json header
func SetJSonHeader(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
}
