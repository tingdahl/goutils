package gcp

import (
	"os"
	"testing"
)

func TestDetermineProjectID_EnvVars(t *testing.T) {
	// Clean env
	origGCP := os.Getenv(EnvGCPProject)
	origGoogle := os.Getenv(EnvGoogleCloudProject)
	origGCloud := os.Getenv(EnvGCloudProject)
	defer func() {
		os.Setenv(EnvGCPProject, origGCP)
		os.Setenv(EnvGoogleCloudProject, origGoogle)
		os.Setenv(EnvGCloudProject, origGCloud)
	}()

	os.Unsetenv(EnvGCPProject)
	os.Unsetenv(EnvGoogleCloudProject)
	os.Unsetenv(EnvGCloudProject)

	// GOOGLE_CLOUD_PROJECT has top priority
	os.Setenv(EnvGoogleCloudProject, "proj-primary")
	os.Setenv(EnvGCPProject, "proj-secondary")
	os.Setenv(EnvGCloudProject, "proj-tertiary")

	id, err := DetermineProjectID()
	if err != nil || id != "proj-primary" {
		t.Errorf("DetermineProjectID() = %q, %v; want 'proj-primary', nil", id, err)
	}

	// GCP_PROJECT has second priority
	os.Unsetenv(EnvGoogleCloudProject)
	id, err = DetermineProjectID()
	if err != nil || id != "proj-secondary" {
		t.Errorf("DetermineProjectID() = %q, %v; want 'proj-secondary', nil", id, err)
	}

	// GCLOUD_PROJECT has third priority
	os.Unsetenv(EnvGCPProject)
	id, err = DetermineProjectID()
	if err != nil || id != "proj-tertiary" {
		t.Errorf("DetermineProjectID() = %q, %v; want 'proj-tertiary', nil", id, err)
	}
}

func TestIsRunningOnGCE(t *testing.T) {
	_ = IsRunningOnGCE()
}
