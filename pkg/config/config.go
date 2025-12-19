package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Azure        AzureConfig        `yaml:"azure"`
	HealthChecks HealthCheckConfig  `yaml:"healthChecks"`
	Alerting     AlertingConfig     `yaml:"alerting"`
	Controller   ControllerConfig   `yaml:"controller"`
	Autoscaling  AutoscalingConfig  `yaml:"autoscaling"`
}

type AzureConfig struct {
	SubscriptionID    string `yaml:"subscriptionId"`
	ResourceGroup     string `yaml:"resourceGroup"`
	ClusterName       string `yaml:"clusterName"`
	UseManagedIdentity bool  `yaml:"useManagedIdentity"`
	TenantID          string `yaml:"tenantId"`
	ClientID          string `yaml:"clientId"`
	ClientSecret      string `yaml:"clientSecret"`
}

type HealthCheckConfig struct {
	Interval           time.Duration `yaml:"interval"`
	UnhealthyThreshold int           `yaml:"unhealthyThreshold"`
	Checks             []string      `yaml:"checks"`
}

type AlertingConfig struct {
	Enabled     bool              `yaml:"enabled"`
	SlackURL    string            `yaml:"slackWebhookUrl"`
	SlackChannel string           `yaml:"slackChannel"`
	WebhookURLs []string          `yaml:"webhookUrls"`
}

type ControllerConfig struct {
	ReconcileInterval  time.Duration `yaml:"reconcileInterval"`
	DrainTimeout       time.Duration `yaml:"drainTimeout"`
	DrainGracePeriod   time.Duration `yaml:"drainGracePeriod"`
	IgnoreDaemonSets   bool          `yaml:"ignoreDaemonSets"`
	DeleteLocalData    bool          `yaml:"deleteLocalData"`
	MaxConcurrentDrains int          `yaml:"maxConcurrentDrains"`
}

type AutoscalingConfig struct {
	Enabled            bool          `yaml:"enabled"`
	ScaleUpThreshold   float64       `yaml:"scaleUpThreshold"`
	ScaleDownThreshold float64       `yaml:"scaleDownThreshold"`
	ScaleUpCooldown    time.Duration `yaml:"scaleUpCooldown"`
	ScaleDownCooldown  time.Duration `yaml:"scaleDownCooldown"`
	MinNodes           int           `yaml:"minNodes"`
	MaxNodes           int           `yaml:"maxNodes"`
	NodePools          []string      `yaml:"nodePools"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultConfig(), nil
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	applyDefaults(cfg)
	return cfg, nil
}

func DefaultConfig() *Config {
	return &Config{
		HealthChecks: HealthCheckConfig{
			Interval:           30 * time.Second,
			UnhealthyThreshold: 3,
			Checks:             []string{"node-condition", "kubelet", "disk-pressure"},
		},
		Controller: ControllerConfig{
			ReconcileInterval:   60 * time.Second,
			DrainTimeout:        5 * time.Minute,
			DrainGracePeriod:    30 * time.Second,
			IgnoreDaemonSets:    true,
			DeleteLocalData:     false,
			MaxConcurrentDrains: 1,
		},
		Autoscaling: AutoscalingConfig{
			Enabled:            false,
			ScaleUpThreshold:   0.8,
			ScaleDownThreshold: 0.3,
			ScaleUpCooldown:    5 * time.Minute,
			ScaleDownCooldown:  10 * time.Minute,
			MinNodes:           1,
			MaxNodes:           10,
		},
	}
}

func applyDefaults(cfg *Config) {
	defaults := DefaultConfig()

	if cfg.HealthChecks.Interval == 0 {
		cfg.HealthChecks.Interval = defaults.HealthChecks.Interval
	}
	if cfg.HealthChecks.UnhealthyThreshold == 0 {
		cfg.HealthChecks.UnhealthyThreshold = defaults.HealthChecks.UnhealthyThreshold
	}
	if cfg.Controller.ReconcileInterval == 0 {
		cfg.Controller.ReconcileInterval = defaults.Controller.ReconcileInterval
	}
	if cfg.Controller.DrainTimeout == 0 {
		cfg.Controller.DrainTimeout = defaults.Controller.DrainTimeout
	}
	if cfg.Controller.MaxConcurrentDrains == 0 {
		cfg.Controller.MaxConcurrentDrains = defaults.Controller.MaxConcurrentDrains
	}
}
