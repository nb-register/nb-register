package app

import (
	"context"
	"fmt"
	"strings"

	pb "orchestrator/pb"
)

func (s *orchestratorServer) finishGoPayAppOTPReady(ctx context.Context, jobID, stepName string, output GoPayAppOTPOutput, data map[string]any) (GoPayAppOTPOutput, error) {
	tokenResp, err := s.validateGoPayAccountToken(ctx)
	data["check_token_valid_after"] = checkTokenValidData(tokenResp)
	if err != nil {
		return output, s.completeGoPayAppOTPStep(ctx, jobID, stepName, data, err)
	}
	if !tokenResp.GetTokenValid() {
		message := strings.TrimSpace(tokenResp.GetErrorMessage())
		if message == "" {
			message = "token invalid"
		}
		return output, s.completeGoPayAppOTPStep(ctx, jobID, stepName, data, fmt.Errorf("%s", message))
	}
	statusAfter, statusErr := s.goPayStatus(ctx)
	data["status_after"] = goPayStatusSnapshotData(goPayStatusSnapshot(statusAfter, statusErr))
	if statusErr != nil {
		return output, s.completeGoPayAppOTPStep(ctx, jobID, stepName, data, statusErr)
	}
	output.Ready = goPayStatusTokenReady(statusAfter)
	output.AccountTokenReady = true
	output.Stage = statusAfter.GetStage()
	output.Phone = statusAfter.GetPhone()
	data["ready"] = output.GetReady()
	data["account_token_ready"] = true
	return output, s.completeGoPayAppOTPStep(ctx, jobID, stepName, data, nil)
}

func (s *orchestratorServer) completeGoPayAppOTPStep(ctx context.Context, jobID, stepName string, data map[string]any, stepErr error) error {
	if data == nil {
		data = map[string]any{}
	}
	if stepErr != nil {
		data["error_message"] = stepErr.Error()
	}
	return s.completeActivityStep(ctx, jobID, stepName, false, true, data, stepErr)
}

func gopayAppOTPStepName(input GoPayAppOTPStartInput) string {
	if stepName := strings.TrimSpace(input.GetStepName()); stepName != "" {
		return stepName
	}
	switch input.GetOperation() {
	case goPayAppOTPOperationCreatePin:
		return stepGoPayAppCreatePin
	default:
		return stepGoPayAppLogin
	}
}

func authStartData(resp *pb.AuthStartResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present":    true,
		"success":             resp.GetSuccess(),
		"error_message":       resp.GetErrorMessage(),
		"mode":                resp.GetMode(),
		"stage":               resp.GetStage(),
		"otp_sent":            resp.GetOtpSent(),
		"verification_method": resp.GetVerificationMethod(),
		"ready":               resp.GetReady(),
		"pin_setup_required":  resp.GetPinSetupRequired(),
	}
}

func authCompleteData(resp *pb.AuthCompleteResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present":     true,
		"success":              resp.GetSuccess(),
		"error_message":        resp.GetErrorMessage(),
		"mode":                 resp.GetMode(),
		"stage":                resp.GetStage(),
		"phone":                resp.GetPhone(),
		"ready":                resp.GetReady(),
		"pin_setup_required":   resp.GetPinSetupRequired(),
		"pin_setup_complete":   resp.GetPinSetupComplete(),
		"sensitive_values_set": false,
	}
}

func createPinStartData(resp *pb.CreatePinStartResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present":    true,
		"success":             resp.GetSuccess(),
		"error_message":       resp.GetErrorMessage(),
		"otp_sent":            resp.GetOtpSent(),
		"verification_method": resp.GetVerificationMethod(),
	}
}

func signupStartData(resp *pb.SignupStartResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present":    true,
		"success":             resp.GetSuccess(),
		"error_message":       resp.GetErrorMessage(),
		"otp_sent":            resp.GetOtpSent(),
		"verification_method": resp.GetVerificationMethod(),
	}
}

func signupCompleteData(resp *pb.SignupCompleteResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present":     true,
		"success":              resp.GetSuccess(),
		"error_message":        resp.GetErrorMessage(),
		"phone":                resp.GetPhone(),
		"pin_setup_required":   resp.GetPinSetupRequired(),
		"sensitive_values_set": false,
	}
}

func createPinCompleteData(resp *pb.CreatePinCompleteResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present":     true,
		"success":              resp.GetSuccess(),
		"error_message":        resp.GetErrorMessage(),
		"phone":                resp.GetPhone(),
		"pin_setup_complete":   resp.GetPinSetupComplete(),
		"sensitive_values_set": false,
	}
}
