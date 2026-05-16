package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	pb "orchestrator/pb"
)

func (s *orchestratorServer) startGoPayAppSignup(ctx context.Context, input GoPayAppOTPStartInput) (GoPayAppOTPOutput, error) {
	stepName := stepGoPayAppSignup
	output := GoPayAppOTPOutput{Operation: goPayAppOTPOperationSignup, TimeoutSeconds: s.paymentOtpTimeout()}
	data := map[string]any{"operation": goPayAppOTPOperationSignup}
	defer func() {
		output.Data = protoData(data)
	}()

	step, err := s.startActivityStep(ctx, input.GetJobId(), stepName, false, true)
	if err != nil {
		return output, err
	}
	if s.gopayClient == nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay app client not configured"))
	}
	phone := configuredGoPayPhone()
	if phone == "" {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("GOPAY_PHONE_NUMBER is required"))
	}

	statusBefore, statusErr := s.goPayStatus(ctx)
	data["status_before"] = goPayStatusSnapshotData(goPayStatusSnapshot(statusBefore, statusErr))
	if statusErr != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("Status before signup: %w", statusErr))
	}
	output.Stage = statusBefore.GetStage()
	output.Phone = statusBefore.GetPhone()
	stage := strings.TrimSpace(statusBefore.GetStage())
	if goPayStatusTokenReady(statusBefore) {
		output.Ready = true
		output.AccountTokenReady = true
		output.SignupComplete = true
		data["ready"] = true
		data["account_token_ready"] = true
		data["signup_complete"] = true
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, nil)
	}
	if stage == "signup_pin_required" || stage == "signup_pin_otp_pending" {
		output.SignupComplete = true
		output.PinSetupRequired = true
		data["signup_complete"] = true
		data["pin_setup_required"] = true
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, nil)
	}
	if stage == "signup_otp_pending" {
		output.OtpRequired = true
		output.IssuedAfterUnix = statusBefore.GetSignupOtpSentAtUnix()
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
	startResp, err := s.gopayClient.SignupStart(ctx, &pb.SignupStartRequest{
		Phone:       phone,
		CountryCode: configuredGoPayCountryCode(),
		StateJson:   stateJSON,
	})
	if err == nil && startResp != nil {
		err = s.saveGoPayAppState(ctx, startResp.GetStateJson())
	}
	data["signup_start"] = signupStartData(startResp)
	if err != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay signup start: %w", err))
	}
	if startResp == nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay signup start returned empty response"))
	}
	if !startResp.GetSuccess() {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay signup start: %s", startResp.GetErrorMessage()))
	}

	statusAfter, statusErr := s.goPayStatus(ctx)
	data["status_after"] = goPayStatusSnapshotData(goPayStatusSnapshot(statusAfter, statusErr))
	if statusErr != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("Status after gopay signup start: %w", statusErr))
	}
	output.Stage = statusAfter.GetStage()
	output.Phone = statusAfter.GetPhone()
	if !startResp.GetOtpSent() {
		stage = strings.TrimSpace(statusAfter.GetStage())
		if goPayStatusTokenReady(statusAfter) || stage == "signup_pin_required" || stage == "signup_pin_otp_pending" {
			output.SignupComplete = true
			output.PinSetupRequired = stage == "signup_pin_required" || stage == "signup_pin_otp_pending"
			data["signup_complete"] = true
			data["pin_setup_required"] = output.GetPinSetupRequired()
			return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, nil)
		}
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay signup start did not send OTP and gopay app not ready: stage=%s", statusAfter.GetStage()))
	}

	output.OtpRequired = true
	output.IssuedAfterUnix = authOtpIssuedAfterUnix(statusAfter, startedAt)
	data["otp_required"] = true
	data["issued_after_unix"] = output.GetIssuedAfterUnix()
	step.update(data)
	return output, nil
}

func (s *orchestratorServer) completeGoPayAppSignup(ctx context.Context, input GoPayAppOTPCompleteInput) (GoPayAppOTPOutput, error) {
	stepName := stepGoPayAppSignup
	output := GoPayAppOTPOutput{Operation: goPayAppOTPOperationSignup, TimeoutSeconds: s.paymentOtpTimeout()}
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
	stateJSON, err := s.loadGoPayAppState(ctx)
	if err != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, err)
	}
	completeResp, err := s.gopayClient.SignupComplete(ctx, &pb.SignupCompleteRequest{Otp: otp, StateJson: stateJSON})
	if err == nil && completeResp != nil {
		err = s.saveGoPayAppState(ctx, completeResp.GetStateJson())
	}
	data["signup_complete"] = signupCompleteData(completeResp)
	data["otp_source"] = input.GetOtpSource()
	if err != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay signup complete: %w", err))
	}
	if completeResp == nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay signup complete returned empty response"))
	}
	if !completeResp.GetSuccess() {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay signup complete: %s", completeResp.GetErrorMessage()))
	}

	output.SignupComplete = true
	output.PinSetupRequired = completeResp.GetPinSetupRequired()
	output.Phone = completeResp.GetPhone()
	data["signup_complete"] = true
	data["pin_setup_required"] = output.GetPinSetupRequired()
	statusAfter, statusErr := s.goPayStatus(ctx)
	data["status_after"] = goPayStatusSnapshotData(goPayStatusSnapshot(statusAfter, statusErr))
	if statusErr == nil {
		output.Stage = statusAfter.GetStage()
		output.Phone = statusAfter.GetPhone()
		output.Ready = goPayStatusTokenReady(statusAfter)
		output.AccountTokenReady = output.GetReady()
	}
	return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, statusErr)
}
