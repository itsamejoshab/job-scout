package pipeline

import (
	"time"

	"github.com/jobscout/jobscout/internal/domain"
)

const (
	// ScheduledNotifyWorkflowID is the reserved ID for the notify schedule.
	ScheduledNotifyWorkflowID = "jobscout-notify-scheduled"
	manualNotifyIDPrefix      = "jobscout-notify-manual-"
)

// ManualNotifyWorkflowID returns a unique operator notify ID that cannot collide
// with the reserved scheduled ID.
func ManualNotifyWorkflowID(now time.Time) string {
	return manualNotifyIDPrefix + now.UTC().Format(time.RFC3339Nano)
}

// ApplyJobDecisionInput persists one filter decision.
type ApplyJobDecisionInput struct {
	JobID    int64
	Decision domain.Decision
}
