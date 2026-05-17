package activities

import (
	"orchestrator/db"
	"orchestrator/internal/jobprojection"
	"orchestrator/pb"
)

func jobToProto(job *db.Job, steps []db.JobStep) *pb.Job {
	return jobprojection.ToProto(job, steps)
}

func browserStartData(resp *pb.StartRegisterResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present":                   true,
		"success":                            resp.GetSuccess(),
		"error_message":                      resp.GetErrorMessage(),
		"flow_id":                            resp.GetFlowId(),
		"otp_required":                       resp.GetOtpRequired(),
		"otp_issued_after":                   resp.GetOtpIssuedAfterUnix(),
		"otp_wait_started_at_unix":           resp.GetOtpWaitStartedAtUnix(),
		"otp_request_action_started_at_unix": resp.GetOtpRequestActionStartedAtUnix(),
		"stage":                              resp.GetStage(),
		"status_message":                     resp.GetStatusMessage(),
		"result":                             registerResultData(resp.GetResult()),
	}
}

func browserStatusData(resp *pb.BrowserFlowStatusResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present":                   true,
		"found":                              resp.GetFound(),
		"flow_id":                            resp.GetFlowId(),
		"mode":                               resp.GetMode(),
		"stage":                              resp.GetStage(),
		"status_message":                     resp.GetStatusMessage(),
		"otp_required":                       resp.GetOtpRequired(),
		"done":                               resp.GetDone(),
		"success":                            resp.GetSuccess(),
		"error_message":                      resp.GetErrorMessage(),
		"started_at_unix":                    resp.GetStartedAtUnix(),
		"updated_at_unix":                    resp.GetUpdatedAtUnix(),
		"otp_issued_after_unix":              resp.GetOtpIssuedAfterUnix(),
		"otp_wait_started_at_unix":           resp.GetOtpWaitStartedAtUnix(),
		"otp_request_action_started_at_unix": resp.GetOtpRequestActionStartedAtUnix(),
		"result":                             registerResultData(resp.GetResult()),
	}
}

func registerResultData(resp *pb.RegisterResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present":         true,
		"success":                  resp.GetSuccess(),
		"error_message":            resp.GetErrorMessage(),
		"session_token_present":    resp.GetSessionToken() != "",
		"access_token_present":     resp.GetAccessToken() != "",
		"device_id_present":        resp.GetDeviceId() != "",
		"plus_trial_eligible":      resp.GetPlusTrialEligible(),
		"plus_trial_checked":       resp.GetPlusTrialChecked(),
		"checkout_url_present":     resp.GetCheckoutUrl() != "",
		"sensitive_values_stored":  false,
		"credential_values_stored": false,
	}
}

func paymentStartData(resp *pb.StartGoPayResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present":    true,
		"success":             resp.GetSuccess(),
		"error_message":       resp.GetErrorMessage(),
		"flow_id":             resp.GetFlowId(),
		"snap_token_present":  resp.GetSnapToken() != "",
		"issued_after_unix":   resp.GetIssuedAfterUnix(),
		"expires_at_unix":     resp.GetExpiresAtUnix(),
		"checkout_url":        resp.GetCheckoutUrl(),
		"checkout_session_id": resp.GetCheckoutSessionId(),
		"otp_required":        resp.GetOtpRequired(),
	}
}

func paymentPrepareData(resp *pb.PrepareGoPayResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present":    true,
		"success":             resp.GetSuccess(),
		"error_message":       resp.GetErrorMessage(),
		"flow_id":             resp.GetFlowId(),
		"snap_token_present":  resp.GetSnapToken() != "",
		"checkout_url":        resp.GetCheckoutUrl(),
		"checkout_session_id": resp.GetCheckoutSessionId(),
	}
}

func paymentOTPResendData(resp *pb.ResendGoPayOTPResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present":  true,
		"success":           resp.GetSuccess(),
		"error_message":     resp.GetErrorMessage(),
		"flow_id":           resp.GetFlowId(),
		"issued_after_unix": resp.GetIssuedAfterUnix(),
	}
}

func plusTrialProbeData(resp *pb.ProbePlusTrialPaymentResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present":     true,
		"success":              resp.GetSuccess(),
		"error_message":        resp.GetErrorMessage(),
		"checked":              resp.GetChecked(),
		"plus_trial_eligible":  resp.GetPlusTrialEligible(),
		"plus_active":          resp.GetPlusActive(),
		"plan_type":            resp.GetPlanType(),
		"tier":                 normalizeTier(resp.GetPlanType()),
		"amount":               resp.GetAmount(),
		"currency":             resp.GetCurrency(),
		"source":               resp.GetSource(),
		"checkout_url_present": resp.GetCheckoutUrl() != "",
		"checkout_session_id":  resp.GetCheckoutSessionId(),
	}
}

func tierProbeData(resp *pb.ProbeTierPaymentResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present": true,
		"success":          resp.GetSuccess(),
		"error_message":    resp.GetErrorMessage(),
		"checked":          resp.GetChecked(),
		"tier":             resp.GetTier(),
		"plus_active":      resp.GetPlusActive(),
		"source":           resp.GetSource(),
	}
}

func paymentResultData(resp *pb.GoPayResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present":             true,
		"success":                      resp.GetSuccess(),
		"error_message":                resp.GetErrorMessage(),
		"charge_ref":                   resp.GetChargeRef(),
		"snap_token_present":           resp.GetSnapToken() != "",
		"awaiting_manual_confirmation": resp.GetAwaitingManualConfirmation(),
		"deeplink_url":                 resp.GetDeeplinkUrl(),
		"qr_code_url":                  resp.GetQrCodeUrl(),
		"finish_redirect_url":          resp.GetFinishRedirectUrl(),
		"finish_200_redirect_url":      resp.GetFinish_200RedirectUrl(),
	}
}

func replayLinkPaymentData(resp *pb.ReplayLinkPaymentResponse, err error) map[string]any {
	data := map[string]any{
		"response_present": resp != nil,
	}
	if err != nil {
		data["error_message"] = err.Error()
	}
	if resp == nil {
		return data
	}
	data["success"] = resp.GetSuccess()
	data["payment_id"] = resp.GetPaymentId()
	data["status"] = resp.GetStatus()
	if resp.GetErrorMessage() != "" {
		data["error_message"] = resp.GetErrorMessage()
	}
	if len(resp.GetSteps()) > 0 {
		steps := make([]map[string]any, 0, len(resp.GetSteps()))
		for _, step := range resp.GetSteps() {
			steps = append(steps, map[string]any{
				"label":         step.GetLabel(),
				"status_code":   step.GetStatusCode(),
				"error_message": step.GetErrorMessage(),
			})
		}
		data["steps"] = steps
	}
	return data
}

func cleanupData(success bool, errorMessage string, err error) map[string]any {
	data := map[string]any{
		"called":        true,
		"success":       success,
		"error_message": errorMessage,
	}
	if err != nil {
		data["rpc_error"] = err.Error()
	}
	return data
}

func cleanupDataFromBrowser(resp *pb.CancelRegisterResponse, err error) map[string]any {
	if resp == nil {
		return cleanupData(false, "", err)
	}
	return cleanupData(resp.GetSuccess(), resp.GetErrorMessage(), err)
}

func cleanupDataFromPayment(resp *pb.CancelGoPayResponse, err error) map[string]any {
	if resp == nil {
		return cleanupData(false, "", err)
	}
	return cleanupData(resp.GetSuccess(), resp.GetErrorMessage(), err)
}
