package activities

import (
	"context"
	"fmt"
	"strings"
	"time"

	pb "orchestrator/pb"
)

const goPayAppCreatePinOperation = "create_pin"

func (s *Server) GoPayAppCreatePinStartActivity(ctx context.Context, input GoPayAppCreatePinStartInput) (GoPayAppOTPOutput, error) {
	return s.startGoPayAppCreatePin(ctx, input)
}

func (s *Server) GoPayAppCreatePinRetryActivity(ctx context.Context, input GoPayAppCreatePinStartInput) (GoPayAppOTPOutput, error) {
	return s.retryGoPayAppCreatePin(ctx, input)
}

func (s *Server) GoPayAppCreatePinCompleteActivity(ctx context.Context, input GoPayAppCreatePinCompleteInput) (GoPayAppOTPOutput, error) {
	return s.completeGoPayAppCreatePin(ctx, input)
}

func (s *Server) startGoPayAppCreatePin(ctx context.Context, input GoPayAppCreatePinStartInput) (GoPayAppOTPOutput, error) {
	stepName := stepGoPayAppCreatePin
	output := GoPayAppOTPOutput{
		Operation:      goPayAppCreatePinOperation,
		TimeoutSeconds: s.paymentOtpTimeout(),
		StateJson:      normalizeGoPayWorkflowStateJSON(input.GetStateJson()),
	}
	otpChannel := normalizeGoPayOTPChannel(input.GetOtpChannel())
	output.OtpChannel = otpChannel
	data := map[string]any{
		"operation":   goPayAppCreatePinOperation,
		"otp_channel": otpChannel,
	}
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

	statusBefore, statusErr := s.goPayStatusForState(ctx, output.GetStateJson())
	output.StateJson = goPayWorkflowStateAfter(output.GetStateJson(), responseStateJSON(statusBefore))
	data["status_before"] = goPayStatusSnapshotData(goPayStatusSnapshot(statusBefore, statusErr))
	if statusErr != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, statusErr)
	}
	output.Stage = statusBefore.GetStage()
	output.Phone = statusBefore.GetPhone()
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
	startResp, err := s.gopayClient.CreatePinStart(ctx, &pb.CreatePinStartRequest{Pin: pin, OtpChannel: goPayOTPMethod(otpChannel), StateJson: output.GetStateJson()})
	output.StateJson = goPayWorkflowStateAfter(output.GetStateJson(), responseStateJSON(startResp))
	data["create_pin_start"] = createPinStartData(startResp)
	if err != nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay create pin start: %w", err))
	}
	if startResp == nil {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay create pin start returned empty response"))
	}
	output.VerificationMethod = startResp.GetVerificationMethod()
	if output.GetOtpChannel() == "" {
		output.OtpChannel = goPayOTPChannelFromMethod(startResp.GetVerificationMethod())
	}
	if !startResp.GetSuccess() {
		return output, s.completeGoPayAppOTPStep(ctx, input.GetJobId(), stepName, data, fmt.Errorf("gopay create pin start: %s", startResp.GetErrorMessage()))
	}

	statusAfter, statusErr := s.goPayStatusForState(ctx, output.GetStateJson())
	output.StateJson = goPayWorkflowStateAfter(output.GetStateJson(), responseStateJSON(statusAfter))
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
	data["verification_method"] = output.GetVerificationMethod()
	data["otp_channel"] = output.GetOtpChannel()
	step.update(data)
	return output, nil
}

func (s *Server) retryGoPayAppCreatePin(ctx context.Context, input GoPayAppCreatePinStartInput) (GoPayAppOTPOutput, error) {
	stepName := stepGoPayAppCreatePin
	output := GoPayAppOTPOutput{
		Operation:      goPayAppCreatePinOperation,
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

	step := s.activityStep(ctx, input.GetJobId(), stepName, false, true)
	step.progress("retrying gopay create pin otp", map[string]any{
		"otp_channel": output.GetOtpChannel(),
	})
	resp, err := s.gopayClient.CreatePinRetry(ctx, &pb.CreatePinRetryRequest{StateJson: output.GetStateJson()})
	output.StateJson = goPayWorkflowStateAfter(output.GetStateJson(), responseStateJSON(resp))
	data["create_pin_retry"] = createPinRetryData(resp)
	if err != nil {
		step.update(data)
		return output, fmt.Errorf("gopay create pin retry: %w", err)
	}
	if resp == nil {
		step.update(data)
		return output, fmt.Errorf("gopay create pin retry returned empty response")
	}
	if !resp.GetSuccess() {
		step.update(data)
		return output, fmt.Errorf("gopay create pin retry: %s", resp.GetErrorMessage())
	}

	statusAfter, statusErr := s.goPayStatusForState(ctx, output.GetStateJson())
	output.StateJson = goPayWorkflowStateAfter(output.GetStateJson(), responseStateJSON(statusAfter))
	data["status_after_retry"] = goPayStatusSnapshotData(goPayStatusSnapshot(statusAfter, statusErr))
	if statusErr != nil {
		step.update(data)
		return output, fmt.Errorf("Status after gopay create pin retry: %w", statusErr)
	}
	output.Stage = statusAfter.GetStage()
	output.Phone = statusAfter.GetPhone()
	output.OtpRequired = resp.GetOtpSent()
	output.IssuedAfterUnix = statusAfter.GetSignupPinOtpSentAtUnix()
	if output.GetIssuedAfterUnix() <= 0 {
		output.IssuedAfterUnix = time.Now().Unix()
	}
	if output.GetOtpChannel() == "" {
		output.OtpChannel = normalizeGoPayOTPChannel(input.GetOtpChannel())
	}
	data["otp_required"] = output.GetOtpRequired()
	data["issued_after_unix"] = output.GetIssuedAfterUnix()
	data["otp_channel"] = output.GetOtpChannel()
	step.update(data)
	return output, nil
}

func (s *Server) completeGoPayAppCreatePin(ctx context.Context, input GoPayAppCreatePinCompleteInput) (GoPayAppOTPOutput, error) {
	stepName := stepGoPayAppCreatePin
	output := GoPayAppOTPOutput{
		Operation:      goPayAppCreatePinOperation,
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
	completeResp, err := s.gopayClient.CreatePinComplete(ctx, &pb.CreatePinCompleteRequest{Otp: otp, Pin: pin, StateJson: output.GetStateJson()})
	output.StateJson = goPayWorkflowStateAfter(output.GetStateJson(), responseStateJSON(completeResp))
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

	statusAfter, statusErr := s.goPayStatusForState(ctx, output.GetStateJson())
	output.StateJson = goPayWorkflowStateAfter(output.GetStateJson(), responseStateJSON(statusAfter))
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
