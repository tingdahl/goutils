package goutils

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/tingdahl/goutils/logging"
)

func InitRestrictiveRobotsTxt(router *mux.Router) {
	if logging.ShouldLog(logging.Info) {
		logging.Log("Setting up restrictive robots.txt", logging.Info)
	}

	router.HandleFunc("/robots.txt", handleRestrictiveRobotsTxt)
}

//Send restrictive robots.txt content.
func handleRestrictiveRobotsTxt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")

	SetCacheHeader(w)

	const res string = "User-agent: *\nDisallow: /"

	w.Write([]byte(res))
}
