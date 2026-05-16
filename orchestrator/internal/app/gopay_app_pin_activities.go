package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	pb "orchestrator/pb"
)

func (s *orchestratorServer) startGoPayAppCreatePin(ctx context.Context, input GoPayAppOTPStartInput) (GoPayAppOTPOutput, error) {
	stepName := stepGoPayAppCreatePin
	output := GoPayAppOTPOutput{Operation: goPayAppOTPOperationCreatePin, TimeoutSeconds: s.paymentOtpTimeout()}
	data := map[string]any{"operation": goPayAppOTPOperationCreatePin}
	defer func() {
		output.Data = protoData(data)
	}()

	step, err := s.startActivityStep(ctx, input.GetJobId(), stepName, false, true)
	if err != nil {
		return output, err
	}
	pin := configuredGoPayPIN()
	if pin == "" {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("GOPAY_PIN is required"))
	}

	statusBefore, statusErr := s.goPayStatus(ctx)
	data["status_before"] = goPayStatusSnapshotData(goPayStatusSnapshot(statusBefore, statusErr))
	if statusErr != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, statusErr)
	}
	output.Stage = statusBefore.GetStage()
	output.Phone = statusBefore.GetPhone()
	if goPayStatusTokenReady(statusBefore) && strings.TrimSpace(statusBefore.GetStage()) != "signup_pin_required" {
		output.Ready = true
		output.AccountTokenReady = true
		output.SignupPinComplete = true
		data["ready"] = true
		data["account_token_ready"] = true
		data["signup_pin_complete"] = true
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, nil)
	}
	if strings.TrimSpace(statusBefore.GetStage()) == "signup_pin_otp_pending" {
		output.OtpRequired = true
		output.IssuedAfterUnix = statusBefore.GetSignupPinOtpSentAtUnix()
		if output.GetIssuedAfterUnix() <= 0 {
			output.IssuedAfterUnix = time.Now().Unix()
		}
		data["otp_required"] = true
		data["issued_after_unix"] = output.GetIssuedAfterUnix()
		step.update(data)
		return output, nil
	}

	startedAt := time.Now().Unix()
	stateJSON, err := s.loadGoPayAppState(ctx)
	if err != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, err)
	}
	startResp, err := s.gopayClient.CreatePinStart(ctx, &pb.CreatePinStartRequest{Pin: pin, StateJson: stateJSON})
	if err == nil && startResp != nil {
		err = s.saveGoPayAppState(ctx, startResp.GetStateJson())
	}
	data["create_pin_start"] = createPinStartData(startResp)
	if err != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay create pin start: %w", err))
	}
	if startResp == nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay create pin start returned empty response"))
	}
	if !startResp.GetSuccess() {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay create pin start: %s", startResp.GetErrorMessage()))
	}

	statusAfter, statusErr := s.goPayStatus(ctx)
	data["status_after"] = goPayStatusSnapshotData(goPayStatusSnapshot(statusAfter, statusErr))
	if statusErr != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("Status after gopay create pin start: %w", statusErr))
	}
	output.Stage = statusAfter.GetStage()
	output.Phone = statusAfter.GetPhone()
	if !startResp.GetOtpSent() {
		if goPayStatusTokenReady(statusAfter) {
			output.Ready = true
			output.AccountTokenReady = true
			output.SignupPinComplete = true
			data["ready"] = true
			data["account_token_ready"] = true
			data["signup_pin_complete"] = true
			return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, nil)
		}
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay create pin start did not send OTP and gopay app not ready: stage=%s", statusAfter.GetStage()))
	}

	output.OtpRequired = true
	output.IssuedAfterUnix = authOtpIssuedAfterUnix(statusAfter, startedAt)
	data["otp_required"] = true
	data["issued_after_unix"] = output.GetIssuedAfterUnix()
	step.update(data)
	return output, nil
}

func (s *orchestratorServer) completeGoPayAppCreatePin(ctx context.Context, input GoPayAppOTPCompleteInput) (GoPayAppOTPOutput, error) {
	stepName := stepGoPayAppCreatePin
	output := GoPayAppOTPOutput{Operation: goPayAppOTPOperationCreatePin, TimeoutSeconds: s.paymentOtpTimeout()}
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
	completeResp, err := s.gopayClient.CreatePinComplete(ctx, &pb.CreatePinCompleteRequest{Otp: otp, Pin: pin, StateJson: stateJSON})
	if err == nil && completeResp != nil {
		err = s.saveGoPayAppState(ctx, completeResp.GetStateJson())
	}
	data["create_pin_complete"] = createPinCompleteData(completeResp)
	data["otp_source"] = input.GetOtpSource()
	if err != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay create pin complete: %w", err))
	}
	if completeResp == nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay create pin complete returned empty response"))
	}
	if !completeResp.GetSuccess() {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay create pin complete: %s", completeResp.GetErrorMessage()))
	}

	statusAfter, statusErr := s.goPayStatus(ctx)
	data["status_after"] = goPayStatusSnapshotData(goPayStatusSnapshot(statusAfter, statusErr))
	if statusErr != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("Status after gopay create pin complete: %w", statusErr))
	}
	output.Stage = statusAfter.GetStage()
	output.Phone = statusAfter.GetPhone()
	output.Ready = goPayStatusTokenReady(statusAfter)
	output.AccountTokenReady = output.GetReady()
	output.SignupPinComplete = true
	data["ready"] = output.GetReady()
	data["account_token_ready"] = output.GetAccountTokenReady()
	data["signup_pin_complete"] = true
	return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, nil)
}
