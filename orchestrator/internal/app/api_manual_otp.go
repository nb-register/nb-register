package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"log"
	"orchestrator/db"
	"orchestrator/internal/contracts"
	"orchestrator/pb"
	"strconv"
	"strings"
	"time"
)

func (s *orchestratorServer) SubmitRegistrationOtp(ctx context.Context, req *pb.SubmitRegistrationOtpRequest) (*pb.SubmitRegistrationOtpResponse, error) {
	otp := normalizeOTP(req.GetOtp())
	if otp == "" {
		return &pb.SubmitRegistrationOtpResponse{Success: false, ErrorMessage: "otp is required"}, nil
	}

	jobID, otpParam, submittedAtParam, otpKind, err := s.resolveManualOTPJob(ctx, strings.TrimSpace(req.GetJobId()), strings.TrimSpace(req.GetAccountId()))
	if err != nil {
		return &pb.SubmitRegistrationOtpResponse{Success: false, ErrorMessage: err.Error()}, nil
	}

	if err := s.setJobParams(ctx, jobID, map[string]string{
		otpParam:         otp,
		submittedAtParam: strconv.FormatInt(time.Now().Unix(), 10),
	}); err != nil {
		return &pb.SubmitRegistrationOtpResponse{Success: false, JobId: jobID, ErrorMessage: err.Error()}, nil
	}
	if err := s.signalManualOTP(ctx, jobID, otpKind); err != nil {
		return &pb.SubmitRegistrationOtpResponse{Success: false, JobId: jobID, ErrorMessage: err.Error()}, nil
	}

	log.Printf("[orchestrator] %s otp submitted job=%s source=manual", otpKind, jobID)
	return &pb.SubmitRegistrationOtpResponse{Success: true, JobId: jobID}, nil
}

func (s *orchestratorServer) signalManualOTP(ctx context.Context, jobID, otpKind string) error {
	job, err := s.getJob(ctx, jobID)
	if err != nil {
		return err
	}
	workflowID, required := manualOTPWorkflowID(job)
	if !required {
		return nil
	}
	if workflowID == "" {
		return fmt.Errorf("workflow id not found for job action %s", job.Action)
	}
	return s.temporal.SignalWorkflow(ctx, workflowID, "", manualOTPSignalName, ManualOTPSignal{Kind: otpKind})
}

func manualOTPWorkflowID(job *db.Job) (string, bool) {
	if job == nil {
		return "", false
	}
	return contracts.ManualOTPWorkflowID(job.Action, job.ID)
}

func (s *orchestratorServer) resolveManualOTPJob(ctx context.Context, jobID, accountID string) (string, string, string, string, error) {
	if jobID != "" {
		var job db.Job
		err := s.db.WithContext(ctx).First(&job, "id = ?", jobID).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return "", "", "", "", fmt.Errorf("job not found: %s", jobID)
			}
			return "", "", "", "", err
		}
		if job.Status != statusRunning {
			return "", "", "", "", fmt.Errorf("job is not running: %s", job.Status)
		}
		otpParam, submittedAtParam, otpKind, err := s.manualOTPParamsForJob(ctx, &job)
		if err != nil {
			return "", "", "", "", err
		}
		return jobID, otpParam, submittedAtParam, otpKind, nil
	}
	if accountID == "" {
		return "", "", "", "", fmt.Errorf("job_id or account_id is required")
	}

	var job db.Job
	err := s.db.WithContext(ctx).
		Where("account_id = ? AND action IN ? AND status = ?", accountID, []string{actionRegister, actionActivate, actionAutopay, actionGoPayApp, actionRegisterAndActivate, actionLoginSession}, statusRunning).
		Order("updated_at DESC").
		First(&job).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", "", "", "", fmt.Errorf("running otp-accepting job not found for account %s", accountID)
		}
		return "", "", "", "", err
	}
	otpParam, submittedAtParam, otpKind, err := s.manualOTPParamsForJob(ctx, &job)
	if err != nil {
		return "", "", "", "", err
	}
	return job.ID, otpParam, submittedAtParam, otpKind, nil
}

func (s *orchestratorServer) manualOTPParamsForJob(ctx context.Context, job *db.Job) (string, string, string, error) {
	if job != nil && job.Action == actionRegisterAndActivate && job.LastStep == stepRegisterAccount && job.ID != "" && s != nil && s.db != nil {
		var step db.JobStep
		err := s.db.WithContext(ctx).First(&step, "job_id = ? AND step_name = ?", job.ID, stepRegisterAccount).Error
		if err == nil && step.Status == statusSucceeded {
			return paymentOTPParam, paymentOTPSubmittedAtParam, "payment", nil
		}
		if err != nil && err != gorm.ErrRecordNotFound {
			return "", "", "", err
		}
	}
	return manualOTPParamsForJobSnapshot(job)
}

func manualOTPParamsForJobSnapshot(job *db.Job) (string, string, string, error) {
	if job == nil {
		return "", "", "", fmt.Errorf("job is required")
	}
	switch job.Action {
	case actionRegister, actionLoginSession:
		return registrationOTPParam, registrationOTPSubmittedAtParam, "registration", nil
	case actionActivate, actionAutopay, actionGoPayApp:
		return paymentOTPParam, paymentOTPSubmittedAtParam, "payment", nil
	case actionRegisterAndActivate:
		if job.LastStep == stepEnsureLogon || job.LastStep == stepGoPayPayment {
			return paymentOTPParam, paymentOTPSubmittedAtParam, "payment", nil
		}
		return registrationOTPParam, registrationOTPSubmittedAtParam, "registration", nil
	default:
		return "", "", "", fmt.Errorf("job does not accept otp: %s", job.Action)
	}
}
