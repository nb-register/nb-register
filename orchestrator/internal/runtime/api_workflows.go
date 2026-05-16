package runtime

import (
	"context"
	"github.com/google/uuid"
	"orchestrator/internal/contracts"
	"orchestrator/pb"
	"strings"
)

func (s *orchestratorServer) RegisterAccount(ctx context.Context, req *pb.RegisterAccountRequest) (*pb.RegisterAccountResponse, error) {
	jobID := uuid.NewString()
	accountID := strings.TrimSpace(req.GetAccountId())
	if accountID == "" {
		accountID = uuid.NewString()
	}
	var result RegisterAccountWorkflowResult
	run, err := s.temporal.ExecuteWorkflow(ctx, s.workflowOptions(workflowIDForAction(actionRegister, jobID)), RegisterAccountWorkflow, RegisterAccountWorkflowInput{
		JobId: jobID,
		Account: &AccountSpec{
			AccountId: accountID,
			Email:     req.GetEmail(),
			Password:  req.GetPassword(),
		},
	})
	if err != nil {
		return nil, err
	}
	if err := run.Get(ctx, &result); err != nil {
		return &pb.RegisterAccountResponse{JobId: jobID, ErrorMessage: err.Error()}, nil
	}

	return &pb.RegisterAccountResponse{
		JobId:             result.GetJobId(),
		SessionToken:      result.GetSessionToken(),
		AccessToken:       result.GetAccessToken(),
		PlusTrialEligible: result.GetPlusTrialEligible(),
		ErrorMessage:      result.GetErrorMessage(),
		CheckoutUrl:       result.GetCheckoutUrl(),
	}, nil
}

func (s *orchestratorServer) ActivateAccount(ctx context.Context, req *pb.ActivateAccountRequest) (*pb.ActivateAccountResponse, error) {
	jobID := uuid.NewString()
	var result ActivateAccountWorkflowResult
	run, err := s.temporal.ExecuteWorkflow(ctx, s.workflowOptions(workflowIDForAction(actionActivate, jobID)), ActivateAccountWorkflow, ActivateAccountWorkflowInput{
		JobId:       jobID,
		AccountId:   strings.TrimSpace(req.GetAccountId()),
		SourceJobId: req.GetJobId(),
		Action:      actionActivate,
	})
	if err != nil {
		return nil, err
	}
	if err := run.Get(ctx, &result); err != nil {
		return &pb.ActivateAccountResponse{JobId: jobID, ErrorMessage: err.Error()}, nil
	}

	return &pb.ActivateAccountResponse{
		JobId:        result.GetJobId(),
		Success:      result.GetSuccess(),
		ErrorMessage: result.GetErrorMessage(),
		ChargeRef:    result.GetChargeRef(),
		SnapToken:    result.GetSnapToken(),
	}, nil
}

func (s *orchestratorServer) AutopayAccount(ctx context.Context, req *pb.ActivateAccountRequest) (*pb.ActivateAccountResponse, error) {
	jobID := uuid.NewString()
	var result AutoPayWorkflowResult
	run, err := s.temporal.ExecuteWorkflow(ctx, s.workflowOptions(workflowIDForAction(actionAutopay, jobID)), AutoPayWorkflow, AutoPayWorkflowInput{
		JobId:       jobID,
		AccountId:   strings.TrimSpace(req.GetAccountId()),
		SourceJobId: req.GetJobId(),
	})
	if err != nil {
		return nil, err
	}
	if err := run.Get(ctx, &result); err != nil {
		return &pb.ActivateAccountResponse{JobId: jobID, ErrorMessage: err.Error()}, nil
	}

	return &pb.ActivateAccountResponse{
		JobId:        result.GetJobId(),
		Success:      result.GetSuccess(),
		ErrorMessage: result.GetErrorMessage(),
		ChargeRef:    result.GetChargeRef(),
		SnapToken:    result.GetSnapToken(),
	}, nil
}

func (s *orchestratorServer) LoginAccount(ctx context.Context, req *pb.LoginAccountRequest) (*pb.LoginAccountResponse, error) {
	accountID := strings.TrimSpace(req.GetAccountId())
	if accountID == "" {
		return &pb.LoginAccountResponse{ErrorMessage: "account_id is required"}, nil
	}
	jobID := uuid.NewString()
	_, err := s.temporal.ExecuteWorkflow(ctx, s.workflowOptions(workflowIDForAction(actionLoginSession, jobID)), LoginSessionWorkflow, LoginSessionWorkflowInput{
		JobId:     jobID,
		AccountId: accountID,
	})
	if err != nil {
		return &pb.LoginAccountResponse{JobId: jobID, ErrorMessage: err.Error()}, nil
	}
	return &pb.LoginAccountResponse{JobId: jobID, Started: true}, nil
}

func (s *orchestratorServer) ProbeAccount(ctx context.Context, req *pb.ProbeAccountRequest) (*pb.ProbeAccountResponse, error) {
	accountID := strings.TrimSpace(req.GetAccountId())
	if accountID == "" {
		return &pb.ProbeAccountResponse{ErrorMessage: "account_id is required"}, nil
	}
	jobID := uuid.NewString()
	_, err := s.temporal.ExecuteWorkflow(ctx, s.workflowOptions(workflowIDForAction(actionProbeAccount, jobID)), ProbeAccountWorkflow, ProbeAccountWorkflowInput{
		JobId:     jobID,
		AccountId: accountID,
	})
	if err != nil {
		return &pb.ProbeAccountResponse{JobId: jobID, ErrorMessage: err.Error()}, nil
	}
	return &pb.ProbeAccountResponse{JobId: jobID, Started: true}, nil
}

func (s *orchestratorServer) RunGoPayApp(ctx context.Context, req *pb.GoPayAppRequest) (*pb.GoPayAppResponse, error) {
	jobID := uuid.NewString()
	_, err := s.temporal.ExecuteWorkflow(ctx, s.workflowOptions(workflowIDForAction(actionGoPayApp, jobID)), GoPayAppWorkflow, GoPayAppWorkflowInput{
		JobId: jobID,
	})
	if err != nil {
		return &pb.GoPayAppResponse{JobId: jobID, ErrorMessage: err.Error()}, nil
	}
	return &pb.GoPayAppResponse{JobId: jobID, Started: true}, nil
}

func (s *orchestratorServer) RegisterAndActivateAccount(ctx context.Context, req *pb.RegisterAndActivateAccountRequest) (*pb.RegisterAndActivateAccountResponse, error) {
	jobID := uuid.NewString()
	accountID := strings.TrimSpace(req.GetAccountId())
	if accountID == "" {
		accountID = uuid.NewString()
	}
	var result RegisterAndActivateWorkflowResult
	run, err := s.temporal.ExecuteWorkflow(ctx, s.workflowOptions(workflowIDForAction(actionRegisterAndActivate, jobID)), RegisterAndActivateWorkflow, RegisterAndActivateWorkflowInput{
		JobId: jobID,
		Account: &AccountSpec{
			AccountId: accountID,
			Email:     req.GetEmail(),
			Password:  req.GetPassword(),
		},
	})
	if err != nil {
		return nil, err
	}
	if err := run.Get(ctx, &result); err != nil {
		return &pb.RegisterAndActivateAccountResponse{JobId: jobID, ErrorMessage: err.Error()}, nil
	}

	return &pb.RegisterAndActivateAccountResponse{
		JobId:             result.GetJobId(),
		SessionToken:      result.GetSessionToken(),
		AccessToken:       result.GetAccessToken(),
		PlusTrialEligible: result.GetPlusTrialEligible(),
		CheckoutUrl:       result.GetCheckoutUrl(),
		ActivationSuccess: result.GetActivationSuccess(),
		ErrorMessage:      result.GetErrorMessage(),
		ChargeRef:         result.GetChargeRef(),
		SnapToken:         result.GetSnapToken(),
	}, nil
}

func (s *orchestratorServer) RegisterMailbox(ctx context.Context, req *pb.RegisterMailboxRequest) (*pb.RegisterMailboxResponse, error) {
	jobID := uuid.NewString()
	var result RegisterMailboxWorkflowResult
	run, err := s.temporal.ExecuteWorkflow(ctx, s.workflowOptions(workflowIDForAction(actionRegisterMailbox, jobID)), RegisterMailboxWorkflow, RegisterMailboxWorkflowInput{
		JobId:      jobID,
		ImportOnly: req.GetImportOnly(),
		AutoOauth:  !req.GetImportOnly() && s.outlookRegisterEnableOAuth2,
	})
	if err != nil {
		return nil, err
	}
	if err := run.Get(ctx, &result); err != nil {
		return &pb.RegisterMailboxResponse{JobId: jobID, ErrorMessage: err.Error()}, nil
	}
	mailboxes := make([]*pb.RegisteredMailbox, 0, len(result.GetMailboxes()))
	for _, mailbox := range result.GetMailboxes() {
		mailboxes = append(mailboxes, &pb.RegisteredMailbox{
			EmailAddress: mailbox.GetEmailAddress(),
			Status:       mailbox.GetStatus(),
		})
	}
	return &pb.RegisterMailboxResponse{
		JobId:        result.GetJobId(),
		Success:      result.GetSuccess(),
		ExitCode:     result.GetExitCode(),
		ErrorMessage: result.GetErrorMessage(),
		Mailboxes:    mailboxes,
	}, nil
}

func (s *orchestratorServer) RunMailboxOAuth(ctx context.Context, req *pb.StartMailboxOAuthRequest) (*pb.StartMailboxOAuthResponse, error) {
	jobID := uuid.NewString()
	limit := req.GetLimit()
	if limit <= 0 {
		limit = 100
	}
	onlyMissing := req.GetOnlyMissing()
	if strings.TrimSpace(req.GetEmailAddress()) == "" {
		onlyMissing = true
	}
	_, err := s.temporal.ExecuteWorkflow(ctx, s.workflowOptions(workflowIDForAction(actionMailboxOAuth, jobID)), MailboxOAuthWorkflow, MailboxOAuthWorkflowInput{
		JobId:        jobID,
		EmailAddress: strings.TrimSpace(req.GetEmailAddress()),
		OnlyMissing:  onlyMissing,
		Limit:        limit,
	})
	if err != nil {
		return &pb.StartMailboxOAuthResponse{JobId: jobID, ErrorMessage: err.Error()}, nil
	}
	return &pb.StartMailboxOAuthResponse{JobId: jobID, Started: true}, nil
}

func workflowIDForAction(action string, jobID string) string {
	workflowID, ok := contracts.WorkflowID(action, jobID)
	if !ok {
		return jobID
	}
	return workflowID
}
