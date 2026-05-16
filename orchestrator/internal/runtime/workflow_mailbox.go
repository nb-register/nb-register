package runtime

import (
	"strconv"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func RegisterMailboxWorkflow(ctx workflow.Context, input RegisterMailboxWorkflowInput) (RegisterMailboxWorkflowResult, error) {
	progress := newWorkflowProgress(ctx, "RegisterMailboxWorkflow", input.GetJobId())
	result := RegisterMailboxWorkflowResult{JobId: input.GetJobId()}
	defer func() {
		finishWorkflowProgressOnError(ctx, progress, result.GetErrorMessage())
	}()
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	atomicCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(30*time.Minute))

	setWorkflowProgress(ctx, progress, "create_job")
	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobId:  input.GetJobId(),
		Action: actionRegisterMailbox,
		Params: map[string]string{
			"import_only": boolString(input.GetImportOnly()),
		},
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	var registration MailboxRegistrationActivityOutput
	setWorkflowProgress(ctx, progress, stepRegisterMailbox)
	if err := workflow.ExecuteActivity(atomicCtx, registerMailboxActivityName, MailboxRegistrationActivityInput{
		JobId:      input.GetJobId(),
		Enabled:    !input.GetImportOnly(),
		ImportOnly: input.GetImportOnly(),
	}).Get(ctx, &registration); err != nil {
		return failRegisterMailboxWorkflow(ctx, retryCtx, result, input.GetJobId(), stepRegisterMailbox, statusFailedRetryable, false, true, err, protoDataMap(registration.GetData())), nil
	}
	if !registration.GetSuccess() {
		err := temporal.NewApplicationError(registration.GetErrorMessage(), "MailboxRegistrationFailed")
		return failRegisterMailboxWorkflow(ctx, retryCtx, result, input.GetJobId(), stepRegisterMailbox, statusFailedRetryable, false, true, err, protoDataMap(registration.GetData())), nil
	}

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobId:  input.GetJobId(),
		Result: registration.GetData(),
	}).Get(ctx, nil)
	if input.GetAutoOauth() {
		startMailboxOAuthSideEffects(ctx, input.GetJobId(), registration.GetMailboxes())
	}

	result.Success = registration.GetSuccess()
	result.ExitCode = registration.GetExitCode()
	result.Mailboxes = registration.GetMailboxes()
	setWorkflowProgressSucceeded(ctx, progress)
	return result, nil
}
func startMailboxOAuthSideEffects(ctx workflow.Context, sourceJobID string, mailboxes []*RegisteredMailboxResult) {
	logger := workflow.GetLogger(ctx)
	for index, mailbox := range mailboxes {
		if mailbox.GetEmailAddress() == "" {
			continue
		}
		jobID := sourceJobID + "-oauth-" + strconv.Itoa(index+1)
		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID:        "mailbox-oauth-" + jobID,
			ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
		})
		future := workflow.ExecuteChildWorkflow(childCtx, MailboxOAuthWorkflow, MailboxOAuthWorkflowInput{
			JobId:        jobID,
			EmailAddress: mailbox.GetEmailAddress(),
			OnlyMissing:  true,
			Limit:        1,
		})
		if err := future.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
			logger.Warn("failed to start mailbox OAuth side effect", "email", mailbox.GetEmailAddress(), "error", err)
		}
	}
}
func MailboxOAuthWorkflow(ctx workflow.Context, input MailboxOAuthWorkflowInput) (MailboxOAuthWorkflowResult, error) {
	progress := newWorkflowProgress(ctx, "MailboxOAuthWorkflow", input.GetJobId())
	result := MailboxOAuthWorkflowResult{JobId: input.GetJobId()}
	defer func() {
		finishWorkflowProgressOnError(ctx, progress, result.GetErrorMessage())
	}()
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	atomicCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(30*time.Minute))

	setWorkflowProgress(ctx, progress, "create_job")
	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobId:  input.GetJobId(),
		Action: actionMailboxOAuth,
		Params: map[string]string{
			"email_address": input.GetEmailAddress(),
			"only_missing":  boolString(input.GetOnlyMissing()),
			"limit":         int32String(input.GetLimit()),
		},
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	var oauth MailboxOAuthActivityOutput
	setWorkflowProgress(ctx, progress, stepMailboxOAuth)
	if err := workflow.ExecuteActivity(atomicCtx, mailboxOAuthActivityName, MailboxOAuthActivityInput{
		JobId:        input.GetJobId(),
		EmailAddress: input.GetEmailAddress(),
		OnlyMissing:  input.GetOnlyMissing(),
		Limit:        input.GetLimit(),
	}).Get(ctx, &oauth); err != nil {
		return failMailboxOAuthWorkflow(ctx, retryCtx, result, input.GetJobId(), stepMailboxOAuth, statusFailedRetryable, false, true, err, protoDataMap(oauth.GetData())), nil
	}
	if !oauth.GetSuccess() {
		msg := oauth.GetErrorMessage()
		if msg == "" {
			msg = "mailbox OAuth failed"
		}
		err := temporal.NewApplicationError(msg, "MailboxOAuthFailed")
		return failMailboxOAuthWorkflow(ctx, retryCtx, result, input.GetJobId(), stepMailboxOAuth, statusFailedRetryable, false, true, err, protoDataMap(oauth.GetData())), nil
	}

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobId:  input.GetJobId(),
		Result: oauth.GetData(),
	}).Get(ctx, nil)

	result.Success = oauth.GetSuccess()
	result.Processed = oauth.GetProcessed()
	result.Succeeded = oauth.GetSucceeded()
	result.Failed = oauth.GetFailed()
	setWorkflowProgressSucceeded(ctx, progress)
	return result, nil
}
