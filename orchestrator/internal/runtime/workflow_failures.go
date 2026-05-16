package runtime

import (
	"go.temporal.io/sdk/workflow"
)

func failRegisterWorkflow(ctx workflow.Context, activityCtx workflow.Context, result RegisterAccountWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) RegisterAccountWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}
func failActivateWorkflow(ctx workflow.Context, activityCtx workflow.Context, result ActivateAccountWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) ActivateAccountWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}
func failAutoPayWorkflow(ctx workflow.Context, activityCtx workflow.Context, result AutoPayWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) AutoPayWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}
func failGoPayAppWorkflow(ctx workflow.Context, activityCtx workflow.Context, result GoPayAppWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) GoPayAppWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}
func failProbeAccountWorkflow(ctx workflow.Context, activityCtx workflow.Context, result ProbeAccountWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) ProbeAccountWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}
func failLoginSessionWorkflow(ctx workflow.Context, activityCtx workflow.Context, result LoginSessionWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) LoginSessionWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}
func failRegisterAndActivateWorkflow(ctx workflow.Context, activityCtx workflow.Context, result RegisterAndActivateWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) RegisterAndActivateWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}
func failRegisterMailboxWorkflow(ctx workflow.Context, activityCtx workflow.Context, result RegisterMailboxWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) RegisterMailboxWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}
func failMailboxOAuthWorkflow(ctx workflow.Context, activityCtx workflow.Context, result MailboxOAuthWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) MailboxOAuthWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}
func markWorkflowFailure(ctx workflow.Context, activityCtx workflow.Context, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) {
	_ = workflow.ExecuteActivity(activityCtx, markJobFailedActivityName, JobFailureInput{
		JobId:        jobID,
		StepName:     stepName,
		Status:       status,
		Recoverable:  recoverable,
		Retryable:    retryable,
		ErrorMessage: err.Error(),
		Result:       protoData(data),
	}).Get(ctx, nil)
}
