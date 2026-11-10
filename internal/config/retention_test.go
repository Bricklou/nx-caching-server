// Tests the retention settings read from the environment.
// Unset per-kind durations follow CLEANUP_THRESHOLD, and invalid values stop the server.
package config

import (
	"strings"
	"testing"
	"time"
)

func setRetentionEnv(t *testing.T, values map[string]string) {
	t.Helper()
	for _, key := range []string{cleanupThresholdKey, cleanupThresholdTrustedKey, cleanupThresholdPRKey, cleanupThresholdDevKey} {
		t.Setenv(key, values[key])
	}
}

func TestRetentionDefaultsToOneHourEverywhere(t *testing.T) {
	setRetentionEnv(t, nil)

	got, err := loadRetention()

	want := Retention{Default: time.Hour, Trusted: time.Hour, PR: time.Hour, Dev: time.Hour}
	if err != nil || got != want {
		t.Errorf("got (%+v, %v), want %+v", got, err, want)
	}
}

func TestRetentionThresholdFeedsTheUnsetKinds(t *testing.T) {
	setRetentionEnv(t, map[string]string{cleanupThresholdKey: "24h"})

	got, err := loadRetention()

	want := Retention{Default: 24 * time.Hour, Trusted: 24 * time.Hour, PR: 24 * time.Hour, Dev: 24 * time.Hour}
	if err != nil || got != want {
		t.Errorf("got (%+v, %v), want %+v", got, err, want)
	}
}

func TestRetentionPerKindOverrides(t *testing.T) {
	setRetentionEnv(t, map[string]string{
		cleanupThresholdKey:        "2h",
		cleanupThresholdTrustedKey: "168h",
		cleanupThresholdPRKey:      "24h",
	})

	got, err := loadRetention()

	want := Retention{Default: 2 * time.Hour, Trusted: 168 * time.Hour, PR: 24 * time.Hour, Dev: 2 * time.Hour}
	if err != nil || got != want {
		t.Errorf("got (%+v, %v), want %+v", got, err, want)
	}
}

func TestRetentionRejectsInvalidValues(t *testing.T) {
	for _, key := range []string{cleanupThresholdKey, cleanupThresholdTrustedKey, cleanupThresholdPRKey, cleanupThresholdDevKey} {
		for _, value := range []string{"soon", "1d", "0", "0s", "-5m"} {
			t.Run(key+"="+value, func(t *testing.T) {
				setRetentionEnv(t, map[string]string{key: value})

				got, err := loadRetention()

				if err == nil {
					t.Fatalf("accepted %q, got %+v", value, got)
				}
				if !strings.Contains(err.Error(), key) {
					t.Errorf("error %q does not name %s", err, key)
				}
			})
		}
	}
}

func TestRetentionShortest(t *testing.T) {
	r := Retention{Default: time.Hour, Trusted: 168 * time.Hour, PR: 10 * time.Minute, Dev: 24 * time.Hour}

	if got := r.Shortest(); got != 10*time.Minute {
		t.Errorf("Shortest = %v, want 10m", got)
	}
}

func TestLoadIncludesTheRetention(t *testing.T) {
	t.Setenv(authConfigKey, "")
	setRetentionEnv(t, map[string]string{cleanupThresholdPRKey: "30m"})

	cfg, err := Load()

	if err != nil || cfg.Retention.PR != 30*time.Minute {
		t.Errorf("got (%+v, %v)", cfg.Retention, err)
	}
}
