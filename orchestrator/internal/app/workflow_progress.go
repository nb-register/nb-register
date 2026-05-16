package app

import "go.temporal.io/sdk/workflow"

func newWorkflowProgress(ctx workflow.Context, workflowName string, jobID string) *WorkflowProgress {
	progress := &WorkflowProgress{
		JobId:         jobID,
		Workflow:      workflowName,
		StepName:      "started",
		Status:        "running",
		UpdatedAtUnix: workflow.Now(ctx).Unix(),
	}
	if err := workflow.SetQueryHandler(ctx, workflowProgressQueryName, func() (*WorkflowProgress, error) {
		return progress, nil
	}); err != nil {
		workflow.GetLogger(ctx).Warn("failed to register workflow progress query", "error", err)
	}
	return progress
}

func setWorkflowProgress(ctx workflow.Context, progress *WorkflowProgress, stepName string) {
	if progress == nil {
		return
	}
	progress.StepName = stepName
	progress.Status = "running"
	progress.ErrorMessage = ""
	progress.UpdatedAtUnix = workflow.Now(ctx).Unix()
}

func setWorkflowProgressFailed(ctx workflow.Context, progress *WorkflowProgress, stepName string, err error) {
	if progress == nil {
		return
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	setWorkflowProgressFailedMessage(ctx, progress, stepName, message)
}

func setWorkflowProgressFailedMessage(ctx workflow.Context, progress *WorkflowProgress, stepName string, message string) {
	if progress == nil {
		return
	}
	progress.StepName = stepName
	progress.Status = "failed"
	progress.ErrorMessage = message
	progress.UpdatedAtUnix = workflow.Now(ctx).Unix()
}

func setWorkflowProgressSucceeded(ctx workflow.Context, progress *WorkflowProgress) {
	if progress == nil {
		return
	}
	progress.StepName = "completed"
	progress.Status = "succeeded"
	progress.ErrorMessage = ""
	progress.UpdatedAtUnix = workflow.Now(ctx).Unix()
}

func finishWorkflowProgressOnError(ctx workflow.Context, progress *WorkflowProgress, errorMessage string) {
	if progress == nil || errorMessage == "" || progress.GetStatus() != "running" {
		return
	}
	setWorkflowProgressFailedMessage(ctx, progress, progress.GetStepName(), errorMessage)
}
