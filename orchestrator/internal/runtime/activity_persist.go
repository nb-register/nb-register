package runtime

import (
	"context"
	"orchestrator/db"
	"orchestrator/internal/jobprojection"
	"orchestrator/pb"
)

func (s *orchestratorServer) PersistRegisteredActivity(ctx context.Context, input PersistRegisteredInput) error {
	account := &pb.Account{
		AccountId:    input.GetAccountId(),
		Status:       "REGISTERED",
		SessionToken: input.GetSessionToken(),
		AccessToken:  input.GetAccessToken(),
	}
	if input.GetPlusTrialChecked() {
		account.PlusTrialEligible = boolPtr(input.GetPlusTrialEligible())
	}
	return s.updateAccount(ctx, account)
}

func (s *orchestratorServer) PersistActivatedActivity(ctx context.Context, input PersistActivatedInput) error {
	account, err := s.getAccount(ctx, input.GetAccountId())
	if err != nil {
		return err
	}
	sessionToken := input.GetSessionToken()
	if sessionToken == "" {
		sessionToken = account.GetSessionToken()
	}
	accessToken := input.GetAccessToken()
	if accessToken == "" {
		accessToken = account.GetAccessToken()
	}
	update := &pb.Account{
		AccountId:    input.GetAccountId(),
		Status:       accountStatusActivated,
		SessionToken: sessionToken,
		AccessToken:  accessToken,
		ChargeRef:    input.GetChargeRef(),
		PlusActive:   boolPtr(true),
		Tier:         "plus",
	}
	if input.GetPlusTrialChecked() {
		update.PlusTrialEligible = boolPtr(input.GetPlusTrialEligible())
	}
	return s.updateAccount(ctx, update)
}

func (s *orchestratorServer) MarkJobFailedActivity(ctx context.Context, input JobFailureInput) error {
	if input.Status == "" {
		input.Status = failedStatus(input.Recoverable, input.Retryable)
	}
	s.updateJob(ctx, input.GetJobId(), input.GetStatus(), input.GetErrorMessage(), protoDataMap(input.GetResult()))
	if input.GetStepName() != "" {
		return s.markStepFailed(ctx, input)
	}
	return nil
}

func (s *orchestratorServer) MarkJobSucceededActivity(ctx context.Context, input JobSuccessInput) error {
	s.updateJob(ctx, input.GetJobId(), statusSucceeded, "", protoDataMap(input.GetResult()))
	return nil
}

func (s *orchestratorServer) createJobWithID(ctx context.Context, jobID, accountID, action string, params map[string]string) (*db.Job, error) {
	return s.jobStore.CreateWithID(ctx, jobID, accountID, action, params)
}

func (s *orchestratorServer) markStepFailed(ctx context.Context, input JobFailureInput) error {
	return s.jobStore.MarkStepFailed(ctx, jobprojection.StepFailure{
		JobID:        input.GetJobId(),
		StepName:     input.GetStepName(),
		Status:       input.GetStatus(),
		Recoverable:  input.GetRecoverable(),
		Retryable:    input.GetRetryable(),
		ErrorMessage: input.GetErrorMessage(),
		Result:       protoDataMap(input.GetResult()),
	})
}
