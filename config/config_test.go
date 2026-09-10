package config

import (
	"os"
	"testing"
)

func TestConfigManager_Defaults(t *testing.T) {
	cm := NewConfigManager(func(string) string { return "" })

	if got := cm.GetEnvironment(); got != "dev" {
		t.Errorf("GetEnvironment() = %q, want %q", got, "dev")
	}
	if got := cm.GetPort(); got != "8080" {
		t.Errorf("GetPort() = %q, want %q", got, "8080")
	}
	if got := cm.GetLogLevel(); got != "INFO" {
		t.Errorf("GetLogLevel() = %q, want %q", got, "INFO")
	}
	if got := cm.GetDefaultTimezone(); got != "Europe/Stockholm" {
		t.Errorf("GetDefaultTimezone() = %q, want %q", got, "Europe/Stockholm")
	}
	if got := cm.GetConfigString("NON_EXISTENT"); got != "" {
		t.Errorf("GetConfigString() = %q, want empty string", got)
	}
}

func TestConfigManager_EnvironmentFallback(t *testing.T) {
	// ENVIRONMENT takes precedence over OTAP
	envMap := map[string]string{
		EnvEnvironment: "prod",
		EnvOTAP:        "stage",
	}
	cm := NewConfigManager(func(k string) string { return envMap[k] })
	if got := cm.GetEnvironment(); got != "prod" {
		t.Errorf("GetEnvironment() = %q, want %q", got, "prod")
	}

	// OTAP is used if ENVIRONMENT is unset
	delete(envMap, EnvEnvironment)
	if got := cm.GetEnvironment(); got != "stage" {
		t.Errorf("GetEnvironment() = %q, want %q", got, "stage")
	}

	// Dev default if both unset
	delete(envMap, EnvOTAP)
	if got := cm.GetEnvironment(); got != "dev" {
		t.Errorf("GetEnvironment() = %q, want %q", got, "dev")
	}
}

func TestConfigManager_ValidateEnvironment(t *testing.T) {
	validEnvs := []string{"dev", "stage", "prod", "test"}
	for _, env := range validEnvs {
		cm := NewConfigManager(func(k string) string {
			if k == EnvEnvironment {
				return env
			}
			return ""
		})
		if err := cm.ValidateEnvironment(); err != nil {
			t.Errorf("ValidateEnvironment() failed for valid env %q: %v", env, err)
		}
	}

	invalidEnvs := []string{"production", "staging", "development", "foo", ""}
	for _, env := range invalidEnvs {
		cm := NewConfigManager(func(k string) string {
			if k == EnvEnvironment {
				return env
			}
			return ""
		})
		// "" falls back to "dev", which is valid, so skip ""
		if env == "" {
			continue
		}
		if err := cm.ValidateEnvironment(); err == nil {
			t.Errorf("ValidateEnvironment() expected error for %q, got nil", env)
		}
	}
}

func TestConfigManager_Fallbacks(t *testing.T) {
	// Assets URL fallback
	cm := NewConfigManager(func(k string) string {
		if k == EnvAssetsBaseURL {
			return "https://fallback.example.com"
		}
		return ""
	})
	if got := cm.GetAssetsURL(); got != "https://fallback.example.com" {
		t.Errorf("GetAssetsURL() fallback = %q, want %q", got, "https://fallback.example.com")
	}

	cmWithPrimary := NewConfigManager(func(k string) string {
		if k == EnvAssetsURL {
			return "https://primary.example.com"
		}
		if k == EnvAssetsBaseURL {
			return "https://fallback.example.com"
		}
		return ""
	})
	if got := cmWithPrimary.GetAssetsURL(); got != "https://primary.example.com" {
		t.Errorf("GetAssetsURL() primary = %q, want %q", got, "https://primary.example.com")
	}

	// CORS origins fallback
	cmCORS := NewConfigManager(func(k string) string {
		if k == EnvAllowedOrigin {
			return "https://example.com"
		}
		return ""
	})
	if got := cmCORS.GetCORSAllowedOrigins(); got != "https://example.com" {
		t.Errorf("GetCORSAllowedOrigins() fallback = %q, want %q", got, "https://example.com")
	}

	// SCW project ID fallback
	cmSCW := NewConfigManager(func(k string) string {
		if k == EnvSCWDefaultProjectID {
			return "default-proj"
		}
		return ""
	})
	if got := cmSCW.GetSCWProjectID(); got != "default-proj" {
		t.Errorf("GetSCWProjectID() fallback = %q, want %q", got, "default-proj")
	}

	// S3 region fallback chain: S3_REGION -> SCW_REGION -> SCW_DEFAULT_ZONE
	cmZone := NewConfigManager(func(k string) string {
		if k == EnvSCWDefaultZone {
			return "zone-1"
		}
		return ""
	})
	if got := cmZone.GetS3Region(); got != "zone-1" {
		t.Errorf("GetS3Region() zone fallback = %q, want %q", got, "zone-1")
	}

	cmRegion := NewConfigManager(func(k string) string {
		if k == EnvSCWRegion {
			return "region-1"
		}
		if k == EnvSCWDefaultZone {
			return "zone-1"
		}
		return ""
	})
	if got := cmRegion.GetS3Region(); got != "region-1" {
		t.Errorf("GetS3Region() region fallback = %q, want %q", got, "region-1")
	}

	cmS3 := NewConfigManager(func(k string) string {
		if k == EnvS3Region {
			return "s3-region"
		}
		if k == EnvSCWRegion {
			return "region-1"
		}
		return ""
	})
	if got := cmS3.GetS3Region(); got != "s3-region" {
		t.Errorf("GetS3Region() primary = %q, want %q", got, "s3-region")
	}
}

func TestConfigManager_AllGetters(t *testing.T) {
	data := map[string]string{
		EnvS3AppDataName:       "my-bucket",
		EnvS3Endpoint:          "https://s3.example.com",
		EnvPublicCloud:         "gcp",
		EnvOIDCProvidersConfig: `{"providers":[]}`,
		EnvHealthCheckAPIKey:   "health-key",
		EnvCronSecret:          "cron-sec",
		EnvAppDomain:           "app.example.com",
		EnvAPIBaseURL:          "https://api.example.com",
		EnvTrustedProxies:      "127.0.0.1,10.0.0.0/8",
		EnvSCWAccessKey:        "scw-access",
		EnvSCWSecretKey:        "scw-secret",
		EnvSCWOrganizationID:   "scw-org",
	}
	cm := NewConfigManager(func(k string) string { return data[k] })

	if cm.GetS3AppDataBucket() != "my-bucket" {
		t.Errorf("GetS3AppDataBucket mismatch")
	}
	if cm.GetS3Endpoint() != "https://s3.example.com" {
		t.Errorf("GetS3Endpoint mismatch")
	}
	if cm.GetPublicCloud() != "gcp" {
		t.Errorf("GetPublicCloud mismatch")
	}
	if cm.GetOIDCProvidersConfig() != `{"providers":[]}` {
		t.Errorf("GetOIDCProvidersConfig mismatch")
	}
	if cm.GetHealthCheckAPIKey() != "health-key" {
		t.Errorf("GetHealthCheckAPIKey mismatch")
	}
	if cm.GetCronSecret() != "cron-sec" {
		t.Errorf("GetCronSecret mismatch")
	}
	if cm.GetAppDomain() != "app.example.com" {
		t.Errorf("GetAppDomain mismatch")
	}
	if cm.GetAPIBaseURL() != "https://api.example.com" {
		t.Errorf("GetAPIBaseURL mismatch")
	}
	if cm.GetTrustedProxies() != "127.0.0.1,10.0.0.0/8" {
		t.Errorf("GetTrustedProxies mismatch")
	}
	if cm.GetSCWAccessKey() != "scw-access" {
		t.Errorf("GetSCWAccessKey mismatch")
	}
	if cm.GetSCWSecretKey() != "scw-secret" {
		t.Errorf("GetSCWSecretKey mismatch")
	}
	if cm.GetSCWOrganizationID() != "scw-org" {
		t.Errorf("GetSCWOrganizationID mismatch")
	}
}

func TestDefaultConfig(t *testing.T) {
	c := Config()
	if c == nil {
		t.Fatal("Config() returned nil")
	}

	// Test package-level helpers
	os.Setenv(EnvEnvironment, "test")
	defer os.Unsetenv(EnvEnvironment)

	if GetEnvironment() != "test" {
		t.Errorf("GetEnvironment() = %q, want 'test'", GetEnvironment())
	}
	if GetPort() == "" {
		t.Error("GetPort() returned empty string")
	}
	if GetConfigString(EnvEnvironment) != "test" {
		t.Errorf("GetConfigString() = %q, want 'test'", GetConfigString(EnvEnvironment))
	}
}
