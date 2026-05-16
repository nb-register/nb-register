package runtime

import (
	"context"
	"time"
)

type activityStep struct {
	server      *orchestratorServer
	ctx         context.Context
	jobID       string
	stepName    string
	recoverable bool
	retryable   bool
}

func (s *orchestratorServer) startActivityStep(ctx context.Context, jobID, stepName string, recoverable bool, retryable bool) (activityStep, error) {
	step := activityStep{
		server:      s,
		ctx:         ctx,
		jobID:       jobID,
		stepName:    stepName,
		recoverable: recoverable,
		retryable:   retryable,
	}
	if err := s.jobStore.StartStep(ctx, jobID, stepName, recoverable, retryable); err != nil {
		return activityStep{}, err
	}
	return step, nil
}

func (s *orchestratorServer) activityStep(ctx context.Context, jobID, stepName string, recoverable bool, retryable bool) activityStep {
	return activityStep{
		server:      s,
		ctx:         ctx,
		jobID:       jobID,
		stepName:    stepName,
		recoverable: recoverable,
		retryable:   retryable,
	}
}

func (s *orchestratorServer) completeActivityStep(ctx context.Context, jobID, stepName string, recoverable bool, retryable bool, data map[string]any, stepErr error) error {
	return s.jobStore.CompleteStep(ctx, jobID, stepName, recoverable, retryable, data, stepErr)
}

func (s *orchestratorServer) updateActivityStepData(ctx context.Context, jobID, stepName string, data map[string]any) {
	s.updateRunningStepData(ctx, jobID, stepName, data)
}

func (s *orchestratorServer) progressActivityStep(ctx context.Context, jobID, stepName, message string, fields map[string]any) {
	s.recordActivityProgress(ctx, jobID, stepName, message, fields)
}

func (step activityStep) complete(data map[string]any, stepErr error) error {
	return step.server.completeActivityStep(step.ctx, step.jobID, step.stepName, step.recoverable, step.retryable, data, stepErr)
}

func (step activityStep) run(fn func() (any, error)) (any, error) {
	return step.server.runAtomicStep(step.ctx, step.jobID, step.stepName, step.recoverable, step.retryable, fn)
}

func (step activityStep) update(data map[string]any) {
	step.server.updateActivityStepData(step.ctx, step.jobID, step.stepName, data)
}

func (step activityStep) progress(message string, fields map[string]any) {
	step.server.progressActivityStep(step.ctx, step.jobID, step.stepName, message, fields)
}

func (step activityStep) progressEvery(last *time.Time, message string, fields map[string]any) {
	step.server.recordActivityProgressEvery(step.ctx, last, step.jobID, step.stepName, message, fields)
}
