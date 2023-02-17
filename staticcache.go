package goutils

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/tingdahl/goutils/logging"
)

var cacheExpiryDate time.Time
var cacheValidity int

//Sets the cache-Control header on the writer with the settings of Cache Expiry
func setCacheHeader(w http.ResponseWriter) {

	var cacheStatement string

	timediff := time.Until(cacheExpiryDate).Seconds()
	if timediff < 0 {
		cacheStatement = "no-store, max-age=0"
	} else if timediff > float64(cacheValidity) {
		cacheStatement = fmt.Sprintf("max-age=%v", cacheValidity)
	} else {
		cacheStatement = fmt.Sprintf("max-age=%v", int(timediff))
	}

	w.Header().Set("Cache-Control", cacheStatement)
}

//Setup caching variables
func SetupStaticCache() {

	var timeFarAway = time.Date(2100, time.November, 10, 23, 0, 0, 0, time.UTC)
	const standardValidity = 86000 //1 day

	input := os.Getenv("CACHE_EXPIRY_DATE")
	if input == "" {
		cacheExpiryDate = timeFarAway
		if logging.ShouldLog(logging.Info) {
			logging.Log("CACHE_EXPIRY_DATE not defined, setting it to far in the future", logging.Info)
		}
	} else {
		format := "2006-01-02T15:04:05.000Z"
		inputTime, err := time.Parse(format, input)
		if err != nil {
			cacheExpiryDate = timeFarAway
			if logging.ShouldLog(logging.Info) {
				logging.Log("CACHE_EXPIRY_DATE could not be parsed, setting it to far in the future", logging.Info)
			}
		} else {
			cacheExpiryDate = inputTime
			if logging.ShouldLog(logging.Info) {
				logging.Log(fmt.Sprintf("CACHE_EXPIRY_DATE set to %v", cacheExpiryDate.Format(format)), logging.Info)
			}
		}
	}

	input = os.Getenv("CACHE_VALIDITY")
	if input == "" {
		cacheValidity = standardValidity
		if logging.ShouldLog(logging.Info) {
			logging.Log("CACHE_VALIDITY not defined, setting it to 1 day", logging.Info)
		}
	} else {

		const maxValidity int = 3000000 //1 month
		inputValidity, err := strconv.Atoi(input)
		if err != nil || inputValidity < 0 || inputValidity > maxValidity {
			cacheValidity = standardValidity
			if logging.ShouldLog(logging.Info) {
				logging.Log("CACHE_VALIDITY could not be parsed, setting it to 1 day", logging.Info)
			}
		} else {
			cacheValidity = inputValidity
			if logging.ShouldLog(logging.Info) {
				logging.Log(fmt.Sprintf("CACHE_VALIDITY set to %v", cacheValidity), logging.Info)
			}
		}
	}
}
