package runtime

import (
	"context"
	"fmt"
	"orchestrator/db"
	"orchestrator/internal/jobprojection"
	"orchestrator/internal/jobstatus"
	"orchestrator/pb"
)

func (s *orchestratorServer) createAccount(ctx context.Context, account *pb.Account) (*pb.Account, error) {
	resp, err := s.accountClient.CreateAccount(ctx, &pb.CreateAccountRequest{Account: account})
	if err != nil {
		return nil, fmt.Errorf("create account: %w", err)
	}
	if resp.GetAccount() == nil || resp.GetAccount().GetAccountId() == "" {
		return nil, fmt.Errorf("account-db returned empty account")
	}
	return resp.GetAccount(), nil
}

func (s *orchestratorServer) getAccount(ctx context.Context, accountID string) (*pb.Account, error) {
	resp, err := s.accountClient.GetAccount(ctx, &pb.GetAccountRequest{AccountId: accountID})
	if err != nil {
		return nil, err
	}
	if resp.GetAccount() == nil {
		return nil, fmt.Errorf("account not found: %s", accountID)
	}
	return resp.GetAccount(), nil
}

func (s *orchestratorServer) updateAccount(ctx context.Context, account *pb.Account) error {
	_, err := s.accountClient.UpdateAccount(ctx, &pb.UpdateAccountRequest{Account: account})
	return err
}

func (s *orchestratorServer) createJob(ctx context.Context, accountID, action string, params map[string]string) (*db.Job, error) {
	return s.jobStore.Create(ctx, accountID, action, params)
}

func (s *orchestratorServer) setJobParams(ctx context.Context, jobID string, params map[string]string) error {
	return s.jobStore.SetParams(ctx, jobID, params)
}

func (s *orchestratorServer) getJobParam(ctx context.Context, jobID, key string) (string, bool, error) {
	return s.jobStore.GetParam(ctx, jobID, key)
}

func (s *orchestratorServer) deleteJobParam(ctx context.Context, jobID, key string) error {
	return s.jobStore.DeleteParam(ctx, jobID, key)
}

func (s *orchestratorServer) updateJob(ctx context.Context, jobID, statusValue, errorMessage string, result any) {
	s.jobStore.Update(ctx, jobID, statusValue, errorMessage, result)
}

func (s *orchestratorServer) getJob(ctx context.Context, jobID string) (*db.Job, error) {
	return s.jobStore.Get(ctx, jobID)
}

func (s *orchestratorServer) runAtomicStep(ctx context.Context, jobID, stepName string, recoverable bool, retryable bool, fn func() (any, error)) (any, error) {
	return s.jobStore.RunAtomicStep(ctx, jobID, stepName, recoverable, retryable, fn)
}

func (s *orchestratorServer) updateRunningStepData(ctx context.Context, jobID, stepName string, result any) {
	s.jobStore.UpdateRunningStepData(ctx, jobID, stepName, result)
}

func failedStatus(recoverable bool, retryable bool) string {
	return jobstatus.Failed(recoverable, retryable)
}

func marshalStepResult(jobID, stepName string, result any) string {
	return jobprojection.MarshalStepResult(jobID, stepName, result)
}
