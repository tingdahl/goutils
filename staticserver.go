package goutils

import (
	"net/http"
	"path"
	"strings"

	"github.com/gorilla/mux"
	"github.com/lpar/gzipped/v2"
)

var (
	permissionPolicy      string
	contentSecurityPolicy string
)

func SetContentSecurityPolicy(csp string) {
	contentSecurityPolicy = csp
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
