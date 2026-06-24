package config

import (
	"testing"
)

// validBase returns a Config that passes Validate, so each table case can vary exactly
// one field and isolate the rule under test.
func validBase() Config {
	return Config{
		Addr:             ":8080",
		DatabaseURL:      "postgres://localhost/takuhai",
		LogLevel:         "info",
		QueueMaxAttempts: 3,
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"valid", func(*Config) {}, false},

		// Validations must fire.
		{"blank-database-url", func(c *Config) { c.DatabaseURL = "" }, true},
		{"unknown-log-level", func(c *Config) { c.LogLevel = "trace" }, true},
		{"zero-queue-max-attempts", func(c *Config) { c.QueueMaxAttempts = 0 }, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBase()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if tt.wantErr && err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}
