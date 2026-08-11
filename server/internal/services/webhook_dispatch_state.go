package services

import "time"

// A non-nil dispatch marker is bound to the current claim generation:
//
//   - marker == locked_at: a new worker prepared the claim but has not
//     committed dispatch authorization;
//   - marker > locked_at: dispatch authorization committed;
//   - marker < locked_at: a mixed-version worker changed the claim generation
//     without changing the marker, so the state is conservatively unknown;
//   - nil: a legacy worker may already have started dispatch.
//
// The database boundary rejects the marker < locked_at transition. Keeping the
// runtime classification fail-closed protects imported/drifted rows as well.
func isWebhookDispatchPrepared(
	marker *time.Time,
	lockedAt *time.Time,
) bool {
	return marker != nil &&
		lockedAt != nil &&
		marker.UTC().Equal(lockedAt.UTC())
}

func isWebhookDispatchStarted(
	marker *time.Time,
	lockedAt *time.Time,
) bool {
	if marker == nil {
		return false
	}
	if lockedAt == nil {
		// Terminal success/expired history clears the claim lock while
		// preserving a committed dispatch marker.
		return true
	}
	return marker.UTC().After(lockedAt.UTC())
}

func isWebhookDispatchUnknown(
	marker *time.Time,
	lockedAt *time.Time,
) bool {
	if marker == nil {
		return true
	}
	return lockedAt != nil && marker.UTC().Before(lockedAt.UTC())
}

func webhookDispatchStartedAt(
	now time.Time,
	lockedAt time.Time,
) time.Time {
	// PostgreSQL timestamps have microsecond precision. Force the committed
	// marker at least one database tick past the claim generation so it can
	// never collapse back to the prepared equality state.
	minimum := lockedAt.UTC().Add(time.Microsecond)
	startedAt := now.UTC()
	if startedAt.Before(minimum) {
		return minimum
	}
	return startedAt
}
