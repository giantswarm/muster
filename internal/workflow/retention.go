package workflow

import (
	"sort"
	"time"
)

// RetentionPolicy bounds how old a record may be and how many records are kept
// before the retention GC prunes it. The zero value is a no-op (keep
// everything), so an unset policy never deletes records.
type RetentionPolicy struct {
	// MaxAge deletes records whose StartedAt is older than now-MaxAge. A zero
	// value disables age-based pruning.
	MaxAge time.Duration

	// MaxCount keeps only the newest MaxCount records (by StartedAt). A zero
	// value disables count-based pruning.
	MaxCount int
}

// retentionRecord is the minimal projection of an execution record that the
// retention policy needs: an ID to delete by and the StartedAt it is judged on.
// Both storage backends map their native records into this shape so the policy
// itself is implemented exactly once.
type retentionRecord struct {
	ID        string
	StartedAt time.Time
}

// selectForDeletion returns the IDs of records that violate the policy: records
// older than MaxAge and/or records beyond the newest MaxCount (by StartedAt).
// An empty policy selects nothing, and each ID is returned at most once.
func selectForDeletion(records []retentionRecord, now time.Time, policy RetentionPolicy) []string {
	if policy.MaxAge <= 0 && policy.MaxCount <= 0 {
		return nil
	}

	toDelete := make(map[string]struct{})

	if policy.MaxAge > 0 {
		cutoff := now.Add(-policy.MaxAge)
		for _, r := range records {
			if r.StartedAt.Before(cutoff) {
				toDelete[r.ID] = struct{}{}
			}
		}
	}

	if policy.MaxCount > 0 && len(records) > policy.MaxCount {
		ordered := make([]retentionRecord, len(records))
		copy(ordered, records)
		// Newest first so the oldest records fall past the count cap.
		sort.SliceStable(ordered, func(i, j int) bool {
			return ordered[i].StartedAt.After(ordered[j].StartedAt)
		})
		for _, r := range ordered[policy.MaxCount:] {
			toDelete[r.ID] = struct{}{}
		}
	}

	ids := make([]string, 0, len(toDelete))
	for id := range toDelete {
		ids = append(ids, id)
	}
	return ids
}
