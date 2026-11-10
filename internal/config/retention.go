// How long unused cache entries are kept, per kind of scope.
// Each duration is read from its own environment variable and falls back to CLEANUP_THRESHOLD.
package config

import (
	"fmt"
	"os"
	"time"
)

const (
	cleanupThresholdKey        = "CLEANUP_THRESHOLD"
	cleanupThresholdTrustedKey = "CLEANUP_THRESHOLD_TRUSTED"
	cleanupThresholdPRKey      = "CLEANUP_THRESHOLD_PR"
	cleanupThresholdDevKey     = "CLEANUP_THRESHOLD_DEV"

	defaultCleanupThreshold = time.Hour
)

// Retention is the time after which an unused entry is removed.
type Retention struct {
	// Default applies to entries outside any scope, such as the flat directory
	// used without authentication.
	Default time.Duration
	// Trusted applies to the default branch and the trusted branches.
	Trusted time.Duration
	// PR applies to pull and merge requests.
	PR time.Duration
	// Dev applies to the other branches.
	Dev time.Duration
}

// Shortest returns the smallest duration, which is how often cleanup must run.
func (r Retention) Shortest() time.Duration {
	return min(r.Default, r.Trusted, r.PR, r.Dev)
}

func loadRetention() (Retention, error) {
	def, err := durationFromEnv(cleanupThresholdKey, defaultCleanupThreshold)
	if err != nil {
		return Retention{}, err
	}

	retention := Retention{Default: def}
	for key, target := range map[string]*time.Duration{
		cleanupThresholdTrustedKey: &retention.Trusted,
		cleanupThresholdPRKey:      &retention.PR,
		cleanupThresholdDevKey:     &retention.Dev,
	} {
		if *target, err = durationFromEnv(key, def); err != nil {
			return Retention{}, err
		}
	}
	return retention, nil
}

// durationFromEnv parses a strictly positive duration, or returns fallback when unset.
func durationFromEnv(key string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}

	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s: %q must be greater than zero", key, raw)
	}
	return value, nil
}
