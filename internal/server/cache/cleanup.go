// Removes cache entries that were not used for a while, and lets the store tidy up after itself.
// How long an entry lives depends on the kind of scope in its key; downloads refresh its modification time.
package cache

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"nx-caching-server/internal/config"
	"nx-caching-server/internal/scope"
	"nx-caching-server/internal/store"
)

// RunCleanup cleans s immediately, then at the pace of the shortest retention,
// until ctx is done.
func RunCleanup(ctx context.Context, s store.Sweeper, retention config.Retention) {
	ticker := time.NewTicker(retention.Shortest())
	defer ticker.Stop()

	for {
		Cleanup(ctx, s, retention)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Cleanup removes entries unused for longer than the retention of their scope,
// then runs the store's own housekeeping.
func Cleanup(ctx context.Context, s store.Sweeper, retention config.Retention) {
	err := s.Walk(ctx, func(entry store.Entry) error {
		if age := time.Since(entry.ModTime); age > retentionFor(retention, entry.Key) {
			slog.Info("removing a stale cache entry", slog.String("key", entry.Key), slog.Duration("unused", age))
			// One entry that cannot be removed must not stop the others.
			if err := s.Delete(ctx, entry.Key); err != nil {
				slog.Error("failed to remove a stale cache entry", slog.String("key", entry.Key), slog.Any("error", err))
			}
		}
		return nil
	})
	// A cancelled context is a shutdown, not a failure.
	if err != nil && ctx.Err() == nil {
		slog.Error("failed to walk the entries", slog.Any("error", err))
	}

	if h, ok := s.(store.Housekeeper); ok {
		h.Housekeep(ctx, retention.Default)
	}
}

// retentionFor picks an entry's retention from its scope kind in the key layout
// <forge>/<repository>/<scope>/<hash> built by accessFor; anything else gets the default.
func retentionFor(retention config.Retention, key string) time.Duration {
	parts := strings.Split(key, "/")
	if len(parts) != 4 || !scope.Valid(parts[2]) {
		return retention.Default
	}

	switch name := parts[2]; {
	case strings.HasPrefix(name, scope.PrefixTrusted):
		return retention.Trusted
	case strings.HasPrefix(name, scope.PrefixPR):
		return retention.PR
	case strings.HasPrefix(name, scope.PrefixDev):
		return retention.Dev
	}
	return retention.Default
}
