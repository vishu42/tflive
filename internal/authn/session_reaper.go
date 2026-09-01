package authn

import (
	"context"
	"log"
	"time"
)

// DefaultSessionReapInterval is how often expired session rows are swept.
// Sessions expire on a scale of hours, so a sweep this often is prompt enough
// while staying invisible in the database's load.
const DefaultSessionReapInterval = 15 * time.Minute

// ReapSessions deletes session rows that can no longer authenticate anyone,
// until ctx is cancelled. Nothing else removes a row: revoking marks one, and
// expiry is a comparison made at read time, so without this the table only
// ever grows — and every row in it holds an encrypted ID token.
//
// It sweeps on the absolute bound alone. A row past that bound is dead
// whatever its idle bound says, absolute_expires_at is indexed for exactly
// this query, and the absolute TTL caps how long an idle-dead row can outlive
// its usefulness. Sweeping on both bounds would need the idle TTL here and
// would not use the index.
//
// No locking or leader election: DELETE over a closed range is idempotent, so
// several API replicas sweeping at once cost a little contention and nothing
// else.
func ReapSessions(ctx context.Context, sessions SessionStore, interval time.Duration, clock func() time.Time) {
	if sessions == nil {
		return
	}
	if interval <= 0 {
		interval = DefaultSessionReapInterval
	}
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		// Sweep first, then wait: a process that restarts more often than the
		// interval would otherwise never sweep at all.
		if deleted, err := sessions.DeleteSessionsExpiredBefore(ctx, clock()); err != nil {
			// A failed sweep leaves rows behind and nothing else, so it is
			// logged and retried on the next tick rather than ending the loop.
			if ctx.Err() == nil {
				log.Printf("session reaper: sweep failed: %v", err)
			}
		} else if deleted > 0 {
			log.Printf("session reaper: deleted %d expired session(s)", deleted)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
