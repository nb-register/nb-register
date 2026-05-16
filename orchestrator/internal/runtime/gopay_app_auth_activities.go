package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	pb "orchestrator/pb"
)

func (s *orchestratorServer) startGoPayAppAuth(ctx context.Context, input GoPayAppOTPStartInput) (GoPayAppOTPOutput, error) {
	stepName := gopayAppOTPStepName(input)
	output := GoPayAppOTPOutput{Operation: goPayAppOTPOperationAuth, TimeoutSeconds: s.paymentOtpTimeout()}
	data := map[string]any{"operation": goPayAppOTPOperationAuth}
	defer func() {
		output.Data = protoData(data)
	}()

	step, err := s.startActivityStep(ctx, input.GetJobId(), stepName, false, true)
	if err != nil {
		return output, err
	}

	statusBefore, statusErr := s.goPayStatus(ctx)
	data["status_before"] = goPayStatusSnapshotData(goPayStatusSnapshot(statusBefore, statusErr))
	if statusErr != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, statusErr)
	}

	tokenResp, err := s.validateGoPayAccountToken(ctx)
	data["check_token_valid"] = checkTokenValidData(tokenResp)
	if err != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, err)
	}
	if tokenResp.GetTokenValid() {
		output.Ready = true
		output.AccountTokenReady = true
		statusAfter, statusErr := s.goPayStatus(ctx)
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

	phone := configuredGoPayPhone()
	if phone == "" {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("GOPAY_PHONE_NUMBER is required"))
	}
	pin := configuredGoPayPIN()
	if pin == "" {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("GOPAY_PIN is required"))
	}
	startedAt := time.Now().Unix()
	stateJSON, err := s.loadGoPayAppState(ctx)
	if err != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, err)
	}
	startResp, err := s.gopayClient.AuthStart(ctx, &pb.AuthStartRequest{
		Phone:       phone,
		CountryCode: configuredGoPayCountryCode(),
		Pin:         pin,
		StateJson:   stateJSON,
	})
	if err == nil && startResp != nil {
		err = s.saveGoPayAppState(ctx, startResp.GetStateJson())
	}
	data["auth_start"] = authStartData(startResp)
	if err != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay auth start: %w", err))
	}
	if startResp == nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay auth start returned empty response"))
	}
	if !startResp.GetSuccess() {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay auth start: %s", startResp.GetErrorMessage()))
	}
	if startResp.GetReady() {
		return s.finishGoPayAppOTPReady(ctx, input.GetJobId(), stepName, output, data)
	}

	statusAfter, statusErr := s.goPayStatus(ctx)
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
			data["pin_setup_required"] = true
			step.update(data)
			return output, nil
		}
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay auth start did not send OTP and gopay app not logged on: stage=%s", statusAfter.GetStage()))
	}

	output.OtpRequired = true
	output.IssuedAfterUnix = authOtpIssuedAfterUnix(statusAfter, startedAt)
	data["otp_required"] = true
	data["issued_after_unix"] = output.GetIssuedAfterUnix()
	step.update(data)
	return output, nil
}

func (s *orchestratorServer) completeGoPayAppAuth(ctx context.Context, input GoPayAppOTPCompleteInput) (GoPayAppOTPOutput, error) {
	stepName := stepGoPayAppLogin
	output := GoPayAppOTPOutput{Operation: goPayAppOTPOperationAuth, TimeoutSeconds: s.paymentOtpTimeout()}
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
	stateJSON, err := s.loadGoPayAppState(ctx)
	if err != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, err)
	}
	completeResp, err := s.gopayClient.AuthComplete(ctx, &pb.AuthCompleteRequest{Otp: otp, Pin: pin, StateJson: stateJSON})
	if err == nil && completeResp != nil {
		err = s.saveGoPayAppState(ctx, completeResp.GetStateJson())
	}
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
		output.Stage = completeResp.GetStage()
		output.Phone = completeResp.GetPhone()
		data["pin_setup_required"] = true
		s.activityStep(ctx, input.GetJobId(), stepName, false, true).update(data)
		return output, nil
	}

	statusAfter, statusErr := s.goPayStatus(ctx)
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
		data["pin_setup_required"] = true
		s.activityStep(ctx, input.GetJobId(), stepName, false, true).update(data)
		return output, nil
	}
	return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay-app not logged on after auth: stage=%s", statusAfter.GetStage()))
}
