package goutils

import (
	"fmt"
	"os"
	"sync"

	"github.com/tingdahl/goutils/logging"
)

//Variable with the
var otap string
var otaplock sync.Mutex

//Retuns string with current OTAP (dev/prod/staging) as set by the OTAP environment
func GetOtap() string {
	otaplock.Lock()
	if len(otap) == 0 {
		otap = os.Getenv("OTAP")
		if otap == "" {
			if logging.ShouldLog(logging.Info) {
				logging.Log("Setting OTAP to default (dev)", logging.Info)
			}
			otap = "dev"

		} else {
			if logging.ShouldLog(logging.Info) {
				logging.Log(fmt.Sprintf("Setting OTAP to %s from OTAP environment variable", otap),
					logging.Info)
			}
		}
	}

	otaplock.Unlock()
	return otap
}
