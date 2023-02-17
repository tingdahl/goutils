package gcputils

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
)

func InitOtap() {

        otap = os.Getenv("OTAP")
        if otap == "" {
                if logging.ShouldLog(logging.Info) {
                        logging.Log("Setting OTAP to default (dev)", logging.Info, logging.NoTrace)
                }
                otap = "dev"
        } else {
                if logging.ShouldLog(logging.Info) {
                        logging.Log(fmt.Sprintf("Setting OTAP to %s from OTAP environment variable", otap),
                                logging.Info, logging.NoTrace)
                }
        }
}
