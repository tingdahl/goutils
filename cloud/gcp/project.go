package gcp

import (
	"context"
	"errors"
	"os"

	"cloud.google.com/go/compute/metadata"
	"golang.org/x/oauth2/google"
)

const (
	EnvGoogleCloudProject = "GOOGLE_CLOUD_PROJECT"
	EnvGCPProject         = "GCP_PROJECT"
	EnvGCloudProject      = "GCLOUD_PROJECT"
)

// IsRunningOnGCE reports whether the code is running on Google Compute Engine or Cloud Run/Functions.
func IsRunningOnGCE() bool {
	return metadata.OnGCE()
}

// DetermineProjectID detects the active Google Cloud project ID from environment variables,
// GCE instance metadata server, or Application Default Credentials (ADC).
// Returns an error if the project ID cannot be determined.
func DetermineProjectID() (string, error) {
	// 1. Explicit environment variables
	if projectID := os.Getenv(EnvGoogleCloudProject); projectID != "" {
		return projectID, nil
	}
	if projectID := os.Getenv(EnvGCPProject); projectID != "" {
		return projectID, nil
	}
	if projectID := os.Getenv(EnvGCloudProject); projectID != "" {
		return projectID, nil
	}

	ctx := context.Background()

	// 2. GCE metadata server (if running in GCP)
	if metadata.OnGCE() {
		projectID, err := metadata.ProjectIDWithContext(ctx)
		if err == nil && projectID != "" {
			return projectID, nil
		}
	}

	// 3. Application Default Credentials
	creds, err := google.FindDefaultCredentials(ctx)
	if err == nil && creds != nil && creds.ProjectID != "" {
		return creds.ProjectID, nil
	}

	return "", errors.New("unable to determine GCP project ID: not set in environment (GOOGLE_CLOUD_PROJECT, GCP_PROJECT, GCLOUD_PROJECT), metadata server unavailable, and no default credentials found")
}
