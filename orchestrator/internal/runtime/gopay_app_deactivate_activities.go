package runtime

import (
	"context"
	"fmt"
	"strings"

	pb "orchestrator/pb"
)

func (s *orchestratorServer) GoPayAppDeactivateStartActivity(ctx context.Context, input GoPayAppDeactivateStartInput) (GoPayAppDeactivateStartOutput, error) {
	output := GoPayAppDeactivateStartOutput{
		ActivationId:   input.GetActivationId(),
		TimeoutSeconds: defaultChangePhoneOTPWaitSeconds,
	}
	data := map[string]any{"activation_id": input.GetActivationId()}
	step := s.activityStep(ctx, input.GetJobId(), stepGoPayAppDeactivateStart, false, true)
	_, err := step.run(func() (any, error) {
		if s.gopayClient == nil || s.codeReceiverClient == nil {
			err := fmt.Errorf("gopay app or code receiver client not configured")
			data["error_message"] = err.Error()
			return data, err
		}
		if input.GetActivationId() == "" {
			err := fmt.Errorf("activation id missing")
			data["error_message"] = err.Error()
			return data, err
		}
		stateJSON, err := s.loadGoPayAppState(ctx)
		if err != nil {
			s.finishSMSActivation(ctx, input.GetActivationId())
			data["error_message"] = err.Error()
			return data, err
		}
		pin := configuredGoPayPIN()
		if pin == "" {
			s.finishSMSActivation(ctx, input.GetActivationId())
			err := fmt.Errorf("GOPAY_PIN is required")
			data["error_message"] = err.Error()
			return data, err
		}
		resp, err := s.gopayClient.DeactivateStart(ctx, &pb.DeactivateStartRequest{Pin: pin, StateJson: stateJSON})
		if err == nil && resp != nil {
			err = s.saveGoPayAppState(ctx, resp.GetStateJson())
		}
		data["deactivate_start"] = deactivateStartData(resp)
		if err != nil {
			s.finishSMSActivation(ctx, input.GetActivationId())
			err = fmt.Errorf("DeactivateStart: %w", err)
			data["error_message"] = err.Error()
			return data, err
		}
		if resp == nil {
			s.finishSMSActivation(ctx, input.GetActivationId())
			err := fmt.Errorf("DeactivateStart returned empty response")
			data["error_message"] = err.Error()
			return data, err
		}
		if !resp.GetSuccess() {
			s.finishSMSActivation(ctx, input.GetActivationId())
			err := fmt.Errorf("DeactivateStart: %s", resp.GetErrorMessage())
			data["error_message"] = err.Error()
			return data, err
		}
		if !resp.GetOtpSent() {
			s.finishSMSActivation(ctx, input.GetActivationId())
			err := fmt.Errorf("DeactivateStart did not send OTP")
			data["error_message"] = err.Error()
			return data, err
		}
		output.OtpRequired = true
		data["otp_required"] = true
		data["timeout_seconds"] = output.GetTimeoutSeconds()
		return data, nil
	})
	output.Data = protoData(data)
	return output, err
}

func (s *orchestratorServer) GoPayAppDeactivateCompleteActivity(ctx context.Context, input GoPayAppDeactivateCompleteInput) (GoPayAppDeactivateCompleteOutput, error) {
	output := GoPayAppDeactivateCompleteOutput{ActivationId: input.GetActivationId()}
	data := map[string]any{"activation_id": input.GetActivationId()}
	step := s.activityStep(ctx, input.GetJobId(), stepGoPayAppDeactivateComplete, false, true)
	_, err := step.run(func() (any, error) {
		if s.gopayClient == nil || s.codeReceiverClient == nil {
			err := fmt.Errorf("gopay app or code receiver client not configured")
			data["error_message"] = err.Error()
			return data, err
		}
		if input.GetActivationId() == "" {
			err := fmt.Errorf("activation id missing")
			data["error_message"] = err.Error()
			return data, err
		}
		if strings.TrimSpace(input.GetCode()) == "" {
			s.finishSMSActivation(ctx, input.GetActivationId())
			err := fmt.Errorf("WaitCode deactivate returned empty code")
			data["error_message"] = err.Error()
			return data, err
		}
		stateJSON, err := s.loadGoPayAppState(ctx)
		if err != nil {
			s.finishSMSActivation(ctx, input.GetActivationId())
			data["error_message"] = err.Error()
			return data, err
		}
		resp, err := s.gopayClient.DeactivateComplete(ctx, &pb.DeactivateCompleteRequest{Otp: input.GetCode(), StateJson: stateJSON})
		if err == nil && resp != nil {
			err = s.saveGoPayAppState(ctx, resp.GetStateJson())
		}
		data["deactivate_complete"] = deactivateCompleteData(resp)
		if err != nil {
			s.finishSMSActivation(ctx, input.GetActivationId())
			err = fmt.Errorf("DeactivateComplete: %w", err)
			data["error_message"] = err.Error()
			return data, err
		}
		if resp == nil {
			s.finishSMSActivation(ctx, input.GetActivationId())
			err := fmt.Errorf("DeactivateComplete returned empty response")
			data["error_message"] = err.Error()
			return data, err
		}
		if !resp.GetSuccess() {
			s.finishSMSActivation(ctx, input.GetActivationId())
			err := fmt.Errorf("DeactivateComplete: %s", resp.GetErrorMessage())
			data["error_message"] = err.Error()
			return data, err
		}
		s.finishSMSActivation(ctx, input.GetActivationId())
		output.DeactivateComplete = true
		data["deactivate_complete"] = true
		return data, nil
	})
	output.Data = protoData(data)
	return output, err
}

func deactivateStartData(resp *pb.DeactivateStartResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present": true,
		"success":          resp.GetSuccess(),
		"error_message":    resp.GetErrorMessage(),
		"otp_sent":         resp.GetOtpSent(),
	}
}

func deactivateCompleteData(resp *pb.DeactivateCompleteResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present": true,
		"success":          resp.GetSuccess(),
		"error_message":    resp.GetErrorMessage(),
		"deactivated_at":   resp.GetDeactivatedAt(),
	}
}
