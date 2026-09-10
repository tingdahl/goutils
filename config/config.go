package config

import (
	"fmt"
	"os"
	"sync"
)

const (
	EnvEnvironment         = "ENVIRONMENT"
	EnvOTAP                = "OTAP"
	EnvPort                = "PORT"
	EnvLogLevel            = "LOG_LEVEL"
	EnvDefaultTimezone     = "DEFAULT_TIMEZONE"
	EnvAssetsURL           = "ASSETS_URL"
	EnvAssetsBaseURL       = "ASSETS_BASE_URL"
	EnvCORSAllowedOrigins  = "CORS_ALLOWED_ORIGINS"
	EnvAllowedOrigin       = "ALLOWED_ORIGIN"
	EnvSCWProjectID        = "SCW_PROJECT_ID"
	EnvSCWDefaultProjectID = "SCW_DEFAULT_PROJECT_ID"
	EnvS3Region            = "S3_REGION"
	EnvSCWRegion           = "SCW_REGION"
	EnvSCWDefaultZone      = "SCW_DEFAULT_ZONE"
	EnvS3AppDataName       = "S3_APPDATA_NAME"
	EnvS3Endpoint          = "S3_ENDPOINT"
	EnvPublicCloud         = "PUBLIC_CLOUD"
	EnvOIDCProvidersConfig = "OIDC_PROVIDERS_CONFIG"
	EnvHealthCheckAPIKey   = "HEALTHCHECK_API_KEY"
	EnvCronSecret          = "CRON_SECRET"
	EnvAppDomain           = "APP_DOMAIN"
	EnvAPIBaseURL          = "API_BASE_URL"
	EnvTrustedProxies      = "TRUSTED_PROXIES"
	EnvSCWAccessKey        = "SCW_ACCESS_KEY"
	EnvSCWSecretKey        = "SCW_SECRET_KEY"
	EnvSCWOrganizationID   = "SCW_DEFAULT_ORGANIZATION_ID"

	DefaultEnvironment = "dev"
	DefaultPort        = "8080"
	DefaultTimezone    = "Europe/Stockholm"
	DefaultLogLevel    = "INFO"
)

// ConfigManager provides centralized access to application configuration and secrets from the environment.
type ConfigManager struct {
	lookup func(key string) string
}

var (
	defaultConfigOnce sync.Once
	defaultConfig     *ConfigManager
)

// Config returns the default application ConfigManager singleton backed by os.Getenv.
func Config() *ConfigManager {
	defaultConfigOnce.Do(func() {
		defaultConfig = NewConfigManager(os.Getenv)
	})
	return defaultConfig
}

// NewConfigManager returns a new ConfigManager using the supplied key lookup function.
// Useful for unit testing without altering the process environment.
func NewConfigManager(lookup func(key string) string) *ConfigManager {
	if lookup == nil {
		lookup = os.Getenv
	}
	return &ConfigManager{lookup: lookup}
}

// GetConfigString retrieves a raw configuration or secret value by key from the environment.
func (c *ConfigManager) GetConfigString(key string) string {
	if c.lookup == nil {
		return ""
	}
	return c.lookup(key)
}

// GetEnvironment returns ENVIRONMENT, falling back to OTAP for backward compatibility,
// or defaults to "dev" if both are unset.
func (c *ConfigManager) GetEnvironment() string {
	if val := c.GetConfigString(EnvEnvironment); val != "" {
		return val
	}
	if val := c.GetConfigString(EnvOTAP); val != "" {
		return val
	}
	return DefaultEnvironment
}

// ValidateEnvironment checks if the configured environment is valid ("dev", "stage", "prod", or "test").
func (c *ConfigManager) ValidateEnvironment() error {
	env := c.GetEnvironment()
	if env != "dev" && env != "stage" && env != "prod" && env != "test" {
		return fmt.Errorf("invalid ENVIRONMENT specified: %q (must be 'dev', 'stage', 'prod', or 'test')", env)
	}
	return nil
}

// GetPort returns PORT or defaults to "8080" if unset.
func (c *ConfigManager) GetPort() string {
	val := c.GetConfigString(EnvPort)
	if val == "" {
		return DefaultPort
	}
	return val
}

// GetLogLevel returns LOG_LEVEL or defaults to "INFO" if unset.
func (c *ConfigManager) GetLogLevel() string {
	val := c.GetConfigString(EnvLogLevel)
	if val == "" {
		return DefaultLogLevel
	}
	return val
}

// GetDefaultTimezone returns DEFAULT_TIMEZONE or defaults to "Europe/Stockholm" if unset.
func (c *ConfigManager) GetDefaultTimezone() string {
	val := c.GetConfigString(EnvDefaultTimezone)
	if val == "" {
		return DefaultTimezone
	}
	return val
}

// GetAssetsURL returns ASSETS_URL, falling back to ASSETS_BASE_URL.
func (c *ConfigManager) GetAssetsURL() string {
	if val := c.GetConfigString(EnvAssetsURL); val != "" {
		return val
	}
	return c.GetConfigString(EnvAssetsBaseURL)
}

// GetCORSAllowedOrigins returns CORS_ALLOWED_ORIGINS, falling back to ALLOWED_ORIGIN.
func (c *ConfigManager) GetCORSAllowedOrigins() string {
	if val := c.GetConfigString(EnvCORSAllowedOrigins); val != "" {
		return val
	}
	return c.GetConfigString(EnvAllowedOrigin)
}

// GetSCWProjectID returns SCW_PROJECT_ID, falling back to SCW_DEFAULT_PROJECT_ID.
func (c *ConfigManager) GetSCWProjectID() string {
	if val := c.GetConfigString(EnvSCWProjectID); val != "" {
		return val
	}
	return c.GetConfigString(EnvSCWDefaultProjectID)
}

// GetS3Region returns S3_REGION, falling back to SCW_REGION, then SCW_DEFAULT_ZONE.
func (c *ConfigManager) GetS3Region() string {
	if val := c.GetConfigString(EnvS3Region); val != "" {
		return val
	}
	if val := c.GetConfigString(EnvSCWRegion); val != "" {
		return val
	}
	return c.GetConfigString(EnvSCWDefaultZone)
}

// GetS3AppDataBucket returns S3_APPDATA_NAME.
func (c *ConfigManager) GetS3AppDataBucket() string {
	return c.GetConfigString(EnvS3AppDataName)
}

// GetS3Endpoint returns S3_ENDPOINT.
func (c *ConfigManager) GetS3Endpoint() string {
	return c.GetConfigString(EnvS3Endpoint)
}

// GetPublicCloud returns PUBLIC_CLOUD.
func (c *ConfigManager) GetPublicCloud() string {
	return c.GetConfigString(EnvPublicCloud)
}

// GetOIDCProvidersConfig returns OIDC_PROVIDERS_CONFIG.
func (c *ConfigManager) GetOIDCProvidersConfig() string {
	return c.GetConfigString(EnvOIDCProvidersConfig)
}

// GetHealthCheckAPIKey returns HEALTHCHECK_API_KEY.
func (c *ConfigManager) GetHealthCheckAPIKey() string {
	return c.GetConfigString(EnvHealthCheckAPIKey)
}

// GetCronSecret returns CRON_SECRET.
func (c *ConfigManager) GetCronSecret() string {
	return c.GetConfigString(EnvCronSecret)
}

// GetAppDomain returns APP_DOMAIN.
func (c *ConfigManager) GetAppDomain() string {
	return c.GetConfigString(EnvAppDomain)
}

// GetAPIBaseURL returns API_BASE_URL.
func (c *ConfigManager) GetAPIBaseURL() string {
	return c.GetConfigString(EnvAPIBaseURL)
}

// GetTrustedProxies returns TRUSTED_PROXIES.
func (c *ConfigManager) GetTrustedProxies() string {
	return c.GetConfigString(EnvTrustedProxies)
}

// GetSCWAccessKey returns SCW_ACCESS_KEY.
func (c *ConfigManager) GetSCWAccessKey() string {
	return c.GetConfigString(EnvSCWAccessKey)
}

// GetSCWSecretKey returns SCW_SECRET_KEY.
func (c *ConfigManager) GetSCWSecretKey() string {
	return c.GetConfigString(EnvSCWSecretKey)
}

// GetSCWOrganizationID returns SCW_DEFAULT_ORGANIZATION_ID.
func (c *ConfigManager) GetSCWOrganizationID() string {
	return c.GetConfigString(EnvSCWOrganizationID)
}

// Package-level helper functions delegating to default singleton

// GetEnvironment returns the current environment from default Config.
func GetEnvironment() string {
	return Config().GetEnvironment()
}

// GetPort returns the port from default Config.
func GetPort() string {
	return Config().GetPort()
}

// GetConfigString retrieves a configuration value from default Config.
func GetConfigString(key string) string {
	return Config().GetConfigString(key)
}
