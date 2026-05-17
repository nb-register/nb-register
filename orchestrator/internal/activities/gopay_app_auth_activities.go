package activities

import (
	"context"
	"fmt"
	"strings"
	"time"

	pb "orchestrator/pb"
)

func (s *Server) startGoPayAppAuth(ctx context.Context, input GoPayAppOTPStartInput) (GoPayAppOTPOutput, error) {
	stepName := gopayAppOTPStepName(input)
	output := GoPayAppOTPOutput{
		Operation:      goPayAppOTPOperationAuth,
		TimeoutSeconds: s.paymentOtpTimeout(),
		StateJson:      normalizeGoPayWorkflowStateJSON(input.GetStateJson()),
	}
	otpChannel := normalizeGoPayOTPChannel(input.GetOtpChannel())
	output.OtpChannel = otpChannel
	data := map[string]any{"operation": goPayAppOTPOperationAuth, "otp_channel": otpChannel}
	defer func() {
		output.Data = protoData(data)
	}()

	step, err := s.startActivityStep(ctx, input.GetJobId(), stepName, false, true)
	if err != nil {
		return output, err
	}

	statusBefore, statusErr := s.goPayStatusForState(ctx, output.GetStateJson())
	output.StateJson = goPayWorkflowStateAfter(output.GetStateJson(), responseStateJSON(statusBefore))
	data["status_before"] = goPayStatusSnapshotData(goPayStatusSnapshot(statusBefore, statusErr))
	if statusErr != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, statusErr)
	}

	tokenResp, nextStateJSON, err := s.validateGoPayAccountTokenForState(ctx, output.GetStateJson())
	output.StateJson = nextStateJSON
	data["check_token_valid"] = checkTokenValidData(tokenResp)
	if err != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, err)
	}
	if tokenResp.GetTokenValid() {
		output.Ready = true
		output.AccountTokenReady = true
		statusAfter, statusErr := s.goPayStatusForState(ctx, output.GetStateJson())
		output.StateJson = goPayWorkflowStateAfter(output.GetStateJson(), responseStateJSON(statusAfter))
		data["status_after"] = goPayStatusSnapshotData(goPayStatusSnapshot(statusAfter, statusErr))
		if statusErr == nil {
			output.Stage = statusAfter.GetStage()
			output.Phone = statusAfter.GetPhone()
		}
		data["ready"] = true
		data["account_token_ready"] = true
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, statusErr)
	}

	message := strings.TrimSpace(tokenResp.GetErrorMessage())
	if message == "" {
		message = "token invalid"
	}
	data["token_invalid_reason"] = message

	phone := normalizeIndonesiaPhone(input.GetPhone())
	if phone == "" {
		phone = configuredGoPayPhone()
	}
	if phone == "" {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay login phone is required"))
	}
	pin := configuredGoPayPIN()
	if pin == "" {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("GOPAY_PIN is required"))
	}
	startedAt := time.Now().Unix()
	startResp, err := s.gopayClient.AuthStart(ctx, &pb.AuthStartRequest{
		Phone:       phone,
		CountryCode: configuredGoPayCountryCode(),
		Pin:         pin,
		OtpChannel:  goPayOTPMethod(otpChannel),
		StateJson:   output.GetStateJson(),
	})
	output.StateJson = goPayWorkflowStateAfter(output.GetStateJson(), responseStateJSON(startResp))
	data["auth_start"] = authStartData(startResp)
	if err != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay auth start: %w", err))
	}
	if startResp == nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay auth start returned empty response"))
	}
	output.VerificationMethod = startResp.GetVerificationMethod()
	if output.GetOtpChannel() == "" {
		output.OtpChannel = goPayOTPChannelFromMethod(startResp.GetVerificationMethod())
	}
	if !startResp.GetSuccess() {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay auth start: %s", startResp.GetErrorMessage()))
	}
	if startResp.GetReady() {
		return s.finishGoPayAppOTPReady(ctx, input.GetJobId(), stepName, output, data)
	}

	statusAfter, statusErr := s.goPayStatusForState(ctx, output.GetStateJson())
	output.StateJson = goPayWorkflowStateAfter(output.GetStateJson(), responseStateJSON(statusAfter))
	data["status_after"] = goPayStatusSnapshotData(goPayStatusSnapshot(statusAfter, statusErr))
	if statusErr != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("Status after gopay auth start: %w", statusErr))
	}
	output.Stage = statusAfter.GetStage()
	output.Phone = statusAfter.GetPhone()
	if !startResp.GetOtpSent() {
		if goPayStatusTokenReady(statusAfter) {
			return s.finishGoPayAppOTPReady(ctx, input.GetJobId(), stepName, output, data)
		}
		if strings.TrimSpace(statusAfter.GetStage()) == "signup_pin_required" {
			output.PinSetupRequired = true
			output.SignupComplete = true
			output.AccountTokenReady = true
			data["pin_setup_required"] = true
			data["signup_complete"] = true
			data["account_token_ready"] = true
			step.update(data)
			return output, nil
		}
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay auth start did not send OTP and gopay app not logged on: stage=%s", statusAfter.GetStage()))
	}

	output.OtpRequired = true
	output.IssuedAfterUnix = authOtpIssuedAfterUnix(statusAfter, startedAt)
	data["otp_required"] = true
	data["issued_after_unix"] = output.GetIssuedAfterUnix()
	data["verification_method"] = output.GetVerificationMethod()
	data["otp_channel"] = output.GetOtpChannel()
	step.update(data)
	return output, nil
}

func (s *Server) completeGoPayAppAuth(ctx context.Context, input GoPayAppOTPCompleteInput) (GoPayAppOTPOutput, error) {
	stepName := stepGoPayAppLogin
	output := GoPayAppOTPOutput{
		Operation:      goPayAppOTPOperationAuth,
		TimeoutSeconds: s.paymentOtpTimeout(),
		StateJson:      normalizeGoPayWorkflowStateJSON(input.GetStateJson()),
	}
	output.OtpChannel = normalizeGoPayOTPChannel(input.GetOtpChannel())
	data := protoDataMap(input.GetData())
	if data == nil {
		data = map[string]any{}
	}
	defer func() {
		output.Data = protoData(data)
	}()

	otp, err := s.consumeStoredOTP(ctx, input.GetJobId(), input.GetOtpParam(), input.GetSubmittedAtParam(), input.GetIssuedAfterUnix())
	if err != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, err)
	}
	pin := configuredGoPayPIN()
	if pin == "" {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("GOPAY_PIN is required"))
	}
	completeResp, err := s.gopayClient.AuthComplete(ctx, &pb.AuthCompleteRequest{Otp: otp, Pin: pin, StateJson: output.GetStateJson()})
	output.StateJson = goPayWorkflowStateAfter(output.GetStateJson(), responseStateJSON(completeResp))
	data["auth_complete"] = authCompleteData(completeResp)
	data["otp_source"] = input.GetOtpSource()
	if err != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay auth complete: %w", err))
	}
	if completeResp == nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay auth complete returned empty response"))
	}
	if !completeResp.GetSuccess() {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay auth complete: %s", completeResp.GetErrorMessage()))
	}
	if completeResp.GetReady() {
		return s.finishGoPayAppOTPReady(ctx, input.GetJobId(), stepName, output, data)
	}
	if completeResp.GetPinSetupRequired() || strings.TrimSpace(completeResp.GetStage()) == "signup_pin_required" {
		output.PinSetupRequired = true
		output.SignupComplete = true
		output.AccountTokenReady = true
		output.Stage = completeResp.GetStage()
		output.Phone = completeResp.GetPhone()
		data["pin_setup_required"] = true
		data["signup_complete"] = true
		data["account_token_ready"] = true
		s.activityStep(ctx, input.GetJobId(), stepName, false, true).update(data)
		return output, nil
	}

	statusAfter, statusErr := s.goPayStatusForState(ctx, output.GetStateJson())
	output.StateJson = goPayWorkflowStateAfter(output.GetStateJson(), responseStateJSON(statusAfter))
	data["status_after"] = goPayStatusSnapshotData(goPayStatusSnapshot(statusAfter, statusErr))
	if statusErr != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("Status after gopay auth complete: %w", statusErr))
	}
	output.Stage = statusAfter.GetStage()
	output.Phone = statusAfter.GetPhone()
	if goPayStatusTokenReady(statusAfter) {
		return s.finishGoPayAppOTPReady(ctx, input.GetJobId(), stepName, output, data)
	}
	if strings.TrimSpace(statusAfter.GetStage()) == "signup_pin_required" {
		output.PinSetupRequired = true
		output.SignupComplete = true
		output.AccountTokenReady = true
		data["pin_setup_required"] = true
		data["signup_complete"] = true
		data["account_token_ready"] = true
		s.activityStep(ctx, input.GetJobId(), stepName, false, true).update(data)
		return output, nil
	}
	return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay-app not logged on after auth: stage=%s", statusAfter.GetStage()))
}
