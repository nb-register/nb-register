package runtime

import (
	"context"
	"time"

	"go.temporal.io/sdk/activity"
)

const activityHeartbeatInterval = 5 * time.Second

type ActivityProgress struct {
	JobID   string         `json:"job_id,omitempty"`
	Step    string         `json:"step,omitempty"`
	Message string         `json:"message,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
	AtUnix  int64          `json:"at_unix,omitempty"`
}

func recordActivityProgress(ctx context.Context, jobID, step, message string, fields map[string]any) {
	activity.RecordHeartbeat(ctx, ActivityProgress{
		JobID:   jobID,
		Step:    step,
		Message: message,
		Fields:  fields,
		AtUnix:  time.Now().Unix(),
	})
}

func recordActivityProgressEvery(ctx context.Context, last *time.Time, jobID, step, message string, fields map[string]any) {
	if last != nil && !last.IsZero() && time.Since(*last) < activityHeartbeatInterval {
		return
	}
	recordActivityProgress(ctx, jobID, step, message, fields)
	if last != nil {
		*last = time.Now()
	}
}

func startActivityHeartbeat(ctx context.Context, jobID, step, message string, fields map[string]any) func() {
	done := make(chan struct{})
	snapshot := copyActivityProgressFields(fields)
	go func() {
		ticker := time.NewTicker(activityHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				recordActivityProgress(ctx, jobID, step, message, snapshot)
			case <-ctx.Done():
				return
			case <-done:
				return
			}
		}
	}()
	return func() {
		close(done)
	}
}

func copyActivityProgressFields(fields map[string]any) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]any, len(fields))
	for key, value := range fields {
		out[key] = value
	}
	return out
}

func (s *orchestratorServer) recordActivityProgress(ctx context.Context, jobID, step, message string, fields map[string]any) {
	recordActivityProgress(ctx, jobID, step, message, fields)
	if s == nil || s.jobStore == nil || jobID == "" || step == "" {
		return
	}
	s.updateRunningStepData(ctx, jobID, step, activityProgressData(message, fields))
}

func (s *orchestratorServer) recordActivityProgressEvery(ctx context.Context, last *time.Time, jobID, step, message string, fields map[string]any) {
	if last != nil && !last.IsZero() && time.Since(*last) < activityHeartbeatInterval {
		return
	}
	s.recordActivityProgress(ctx, jobID, step, message, fields)
	if last != nil {
		*last = time.Now()
	}
}

func activityProgressData(message string, fields map[string]any) map[string]any {
	at := time.Now().Unix()
	out := map[string]any{
		"progress_message": message,
		"progress_at_unix": at,
		"progress": map[string]any{
			"message": message,
			"at_unix": at,
			"fields":  fields,
		},
	}
	for key, value := range fields {
		out[key] = value
	}
	return out
}
