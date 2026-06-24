// Package config owns takuhai's flag + env (TAKUHAI_-prefixed) configuration and
// its validation (design §10, implementation-plan 1d). It moves config out of
// cmd/takuhai/main.go's inline helper into a first-class package.
//
// Flag/env binding (the TAKUHAI_ prefix) is wired in cmd/takuhai; this package
// owns the Config shape and its Validate rules.
package config

import (
	"fmt"
	"slices"
)

var validLogLevels = []string{"debug", "info", "warn", "error"}

// Config is the validated runtime configuration (design §10). Fields mirror the
// cmd/takuhai flags; Phase 1 adds binding + ingestion knobs.
type Config struct {
	Addr             string // listen address
	DatabaseURL      string // PostgreSQL connection string (required)
	LogLevel         string // "debug" | "info" | "warn" | "error"
	QueueMaxAttempts int
}

// Validate rejects an invalid configuration: a blank DatabaseURL, an unknown
// Transport, or an unknown LogLevel must each return a descriptive error (design
// §10, Phase 1 gate #7).
func (c Config) Validate() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("config: --database-url is required")
	}
	if !slices.Contains(validLogLevels, c.LogLevel) {
		return fmt.Errorf("config: unknown --log-level %q (want one of %v)", c.LogLevel, validLogLevels)
	}
	if c.QueueMaxAttempts <= 0 {
		return fmt.Errorf("config: --queue-max-attempts must be > 0")
	}
	return nil
}
