package runtime

import (
	"context"
	"fmt"
	"strings"

	"orchestrator/internal/manualinput"
	"orchestrator/pb"
)

const (
	browserAuthModeRegister = "register"
	browserAuthModeLogin    = "login"
)

func (s *orchestratorServer) BrowserAuthStartActivity(ctx context.Context, input BrowserAuthStartInput) (BrowserAuthStartOutput, error) {
	output := BrowserAuthStartOutput{
		AccountId: input.GetAccountId(),
	}
	data := map[string]any{}
	account, err := s.getAccount(ctx, input.GetAccountId())
	if err != nil {
		return output, err
	}
	if err := rejectUserAlreadyExistsAccount(account); err != nil {
		return output, err
	}
	if input.GetMode() == browserAuthModeLogin {
		if strings.TrimSpace(account.GetEmail()) == "" {
			return output, fmt.Errorf("email is required")
		}
		if strings.TrimSpace(account.GetPassword()) == "" {
			return output, fmt.Errorf("password is required")
		}
	}

	stepName, err := browserAuthStepName(input.GetMode())
	if err != nil {
		return output, err
	}
	step, err := s.startActivityStep(ctx, input.GetJobId(), stepName, false, true)
	if err != nil {
		return output, err
	}

	data["account_id"] = account.GetAccountId()
	data["email"] = account.GetEmail()
	step.progress("starting browser auth", map[string]any{
		"mode":  input.GetMode(),
		"email": account.GetEmail(),
	})
	stopHeartbeat := startActivityHeartbeat(ctx, input.GetJobId(), stepName, "starting browser auth", data)
	defer stopHeartbeat()

	startResp, err := s.browserAuthStart(ctx, input.GetMode(), input.GetJobId(), account)
	data["browser_start"] = browserStartData(startResp)
	if err != nil {
		output.Data = protoData(data)
		return output, s.completeBrowserAuthStep(ctx, input.GetJobId(), stepName, input.GetAccountId(), data, err)
	}
	if startResp == nil {
		err := fmt.Errorf("browser %s start returned empty response", input.GetMode())
		output.Data = protoData(data)
		return output, s.completeBrowserAuthStep(ctx, input.GetJobId(), stepName, input.GetAccountId(), data, err)
	}
	if !startResp.GetSuccess() {
		err := fmt.Errorf("browser %s start failed: %s", input.GetMode(), startResp.GetErrorMessage())
		output.Data = protoData(data)
		return output, s.completeBrowserAuthStep(ctx, input.GetJobId(), stepName, input.GetAccountId(), data, err)
	}

	output.FlowId = startResp.GetFlowId()
	output.Email = account.GetEmail()
	output.OtpRequired = startResp.GetOtpRequired()
	output.OtpIssuedAfterUnix = startResp.GetOtpIssuedAfterUnix()
	output.OtpTimeoutSeconds = s.registrationOtpTimeout()
	step.progress("browser auth started", map[string]any{
		"mode":         input.GetMode(),
		"otp_required": output.GetOtpRequired(),
	})

	if !output.GetOtpRequired() {
		result := startResp.GetResult()
		data["browser_complete"] = registerResultData(result)
		if result == nil {
			err := fmt.Errorf("browser %s completed without result", input.GetMode())
			output.Data = protoData(data)
			return output, s.completeBrowserAuthStep(ctx, input.GetJobId(), stepName, input.GetAccountId(), data, err)
		}
		if !result.GetSuccess() {
			err := fmt.Errorf("browser %s failed: %s", input.GetMode(), result.GetErrorMessage())
			output.Data = protoData(data)
			return output, s.completeBrowserAuthStep(ctx, input.GetJobId(), stepName, input.GetAccountId(), data, err)
		}
		resultOutput := registerActivityOutputFromResponse(result, data)
		output.Result = &resultOutput
		output.Data = protoData(data)
		return output, step.complete(data, nil)
	}

	output.Data = protoData(data)
	step.update(data)
	return output, nil
}

func (s *orchestratorServer) BrowserAuthCompleteActivity(ctx context.Context, input BrowserAuthCompleteInput) (RegisterActivityOutput, error) {
	stepName, err := browserAuthStepName(input.Mode)
	if err != nil {
		return RegisterActivityOutput{}, err
	}
	data := map[string]any{
		"account_id": input.GetAccountId(),
		"flow_id":    input.GetFlowId(),
		"mode":       input.GetMode(),
		"otp_source": input.GetOtpSource(),
	}
	step := s.activityStep(ctx, input.GetJobId(), stepName, false, true)
	step.progress("completing browser auth", map[string]any{
		"mode":       input.GetMode(),
		"otp_source": input.GetOtpSource(),
	})
	stopHeartbeat := startActivityHeartbeat(ctx, input.GetJobId(), stepName, "completing browser auth", data)
	defer stopHeartbeat()

	otp, err := s.consumeStoredOTP(ctx, input.GetJobId(), input.GetOtpParam(), input.GetSubmittedAtParam(), input.GetOtpIssuedAfterUnix())
	if err != nil {
		return RegisterActivityOutput{Data: protoData(data)}, s.completeBrowserAuthStep(ctx, input.GetJobId(), stepName, input.GetAccountId(), data, err)
	}
	result, err := s.browserAuthComplete(ctx, input.GetMode(), input.GetFlowId(), otp)
	data["browser_complete"] = registerResultData(result)
	if err != nil {
		return RegisterActivityOutput{Data: protoData(data)}, s.completeBrowserAuthStep(ctx, input.GetJobId(), stepName, input.GetAccountId(), data, err)
	}
	if result == nil {
		err := fmt.Errorf("browser %s complete returned empty response", input.GetMode())
		return RegisterActivityOutput{Data: protoData(data)}, s.completeBrowserAuthStep(ctx, input.GetJobId(), stepName, input.GetAccountId(), data, err)
	}
	if !result.GetSuccess() {
		err := fmt.Errorf("browser %s complete failed: %s", input.GetMode(), result.GetErrorMessage())
		return RegisterActivityOutput{Data: protoData(data)}, s.completeBrowserAuthStep(ctx, input.GetJobId(), stepName, input.GetAccountId(), data, err)
	}

	output := registerActivityOutputFromResponse(result, data)
	return output, step.complete(data, nil)
}

func (s *orchestratorServer) BrowserAuthCancelActivity(ctx context.Context, input BrowserAuthCancelInput) error {
	if strings.TrimSpace(input.GetFlowId()) == "" {
		return nil
	}
	resp, err := s.browserAuthCancel(ctx, input.GetMode(), input.GetFlowId())
	if err != nil {
		return err
	}
	if resp != nil && !resp.GetSuccess() {
		return fmt.Errorf("browser %s cancel failed: %s", input.Mode, resp.GetErrorMessage())
	}
	return nil
}

func (s *orchestratorServer) FetchManualOTPActivity(ctx context.Context, input OTPWaitInput) (OTPWaitOutput, error) {
	value, found, err := s.getJobParam(ctx, input.GetJobId(), input.GetOtpParam())
	if err != nil || !found {
		return OTPWaitOutput{}, err
	}
	if !manualinput.SubmittedAfter(ctx, s.jobStore, input.GetJobId(), input.GetOtpParam(), input.GetSubmittedAtParam(), input.GetIssuedAfterUnix()) {
		return OTPWaitOutput{}, nil
	}
	return OTPWaitOutput{Found: normalizeOTP(value) != "", Source: "manual"}, nil
}

func (s *orchestratorServer) consumeStoredOTP(ctx context.Context, jobID, otpParam, submittedAtParam string, issuedAfterUnix int64) (string, error) {
	value, found, err := s.getJobParam(ctx, jobID, otpParam)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("otp not found")
	}
	if !manualinput.SubmittedAfter(ctx, s.jobStore, jobID, otpParam, submittedAtParam, issuedAfterUnix) {
		return "", fmt.Errorf("otp is stale")
	}
	code := normalizeOTP(value)
	if code == "" {
		_ = s.deleteJobParam(ctx, jobID, otpParam)
		_ = s.deleteJobParam(ctx, jobID, submittedAtParam)
		return "", fmt.Errorf("otp is empty")
	}
	if err := s.deleteJobParam(ctx, jobID, otpParam); err != nil {
		return "", err
	}
	_ = s.deleteJobParam(ctx, jobID, submittedAtParam)
	return code, nil
}

func (s *orchestratorServer) browserAuthStart(ctx context.Context, mode, jobID string, account *pb.Account) (*pb.StartRegisterResponse, error) {
	req := &pb.RegisterRequest{
		JobId:         jobID,
		AssignedEmail: account.GetEmail(),
		Password:      account.GetPassword(),
		FirstName:     account.GetFirstName(),
		LastName:      account.GetLastName(),
		Birthday:      account.GetDob(),
	}
	if mode == browserAuthModeLogin {
		return s.browserClient.StartLogin(ctx, req)
	}
	return s.browserClient.StartRegister(ctx, req)
}

func (s *orchestratorServer) browserAuthComplete(ctx context.Context, mode, flowID, otp string) (*pb.RegisterResponse, error) {
	req := &pb.CompleteRegisterRequest{FlowId: flowID, Otp: otp}
	if mode == browserAuthModeLogin {
		return s.browserClient.CompleteLogin(ctx, req)
	}
	return s.browserClient.CompleteRegister(ctx, req)
}

func (s *orchestratorServer) browserAuthCancel(ctx context.Context, mode, flowID string) (*pb.CancelRegisterResponse, error) {
	req := &pb.CancelRegisterRequest{FlowId: flowID}
	if mode == browserAuthModeLogin {
		return s.browserClient.CancelLogin(ctx, req)
	}
	return s.browserClient.CancelRegister(ctx, req)
}

func (s *orchestratorServer) completeBrowserAuthStep(ctx context.Context, jobID, stepName, accountID string, data map[string]any, err error) error {
	if isAccountAlreadyExistsError(err) {
		if data != nil {
			data["terminal_reason"] = "openai_user_already_exists"
		}
		if updateErr := s.updateAccount(ctx, &pb.Account{
			AccountId:    accountID,
			Status:       accountStatusUserAlreadyExists,
			ErrorMessage: err.Error(),
		}); updateErr != nil {
			err = fmt.Errorf("%w; additionally failed to mark account user already exists: %v", err, updateErr)
		}
	}
	return s.completeActivityStep(ctx, jobID, stepName, false, true, data, err)
}

func browserAuthStepName(mode string) (string, error) {
	switch mode {
	case browserAuthModeRegister:
		return stepRegisterAccount, nil
	case browserAuthModeLogin:
		return stepLoginSession, nil
	default:
		return "", fmt.Errorf("unsupported browser auth mode: %s", mode)
	}
}

func registerActivityOutputFromResponse(resp *pb.RegisterResponse, data map[string]any) RegisterActivityOutput {
	if resp == nil {
		return RegisterActivityOutput{Data: protoData(data)}
	}
	return RegisterActivityOutput{
		SessionToken:      resp.GetSessionToken(),
		AccessToken:       resp.GetAccessToken(),
		DeviceId:          resp.GetDeviceId(),
		PlusTrialEligible: resp.GetPlusTrialEligible(),
		PlusTrialChecked:  resp.GetPlusTrialChecked(),
		CheckoutUrl:       resp.GetCheckoutUrl(),
		Data:              protoData(data),
	}
}
