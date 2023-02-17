package goutils

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/tingdahl/goutils/logging"

	"golang.org/x/oauth2/google"
	compute "google.golang.org/api/compute/v1"
)

var gcpMutex = &sync.Mutex{}
var googleProjectID string = ""
var inCloud bool = false

//Returns if we are running in GCP cloud.
func RunningInGCP() bool {
	if googleProjectID == "" {
		log.Fatal("Google project not set.")
	}

	return inCloud
}

func GCPProject() string {
	if googleProjectID == "" {
		log.Fatal("Google project not set.")
	}

	return googleProjectID
}

//Get the GCP project, and if we are running in cloud
func InitGCPEnvironment(defaultProj string) (string, bool) {

	var logMessage string = ""
	gcpMutex.Lock()

	if googleProjectID == "" {
		ctx := context.Background()
		credentials, err := google.FindDefaultCredentials(ctx, compute.ComputeScope)

		if credentials != nil && credentials.ProjectID != "" && err == nil {
			googleProjectID = credentials.ProjectID
			inCloud = true
			gcpMutex.Unlock()
			logMessage = fmt.Sprintf("Determining GCP project %s from FindDefaultCredentials", googleProjectID)
		} else {
			googleProjectID = os.Getenv("GCP_PROJECT")
			if googleProjectID != "" {
				logMessage = fmt.Sprintf("Determining GCP project %s from GCP_PROJECT", googleProjectID)
			} else {
				googleProjectID = defaultProj
				logMessage = fmt.Sprintf("Using default GCP project: %s", googleProjectID)
			}

			gcpMutex.Unlock()
		}

		if logging.ShouldLog(logging.Info) {
			logging.Log(logMessage, logging.Info)
		}
	} else {
		gcpMutex.Unlock()
	}

	return googleProjectID, inCloud
}
