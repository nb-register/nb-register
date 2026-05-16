package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	pb "orchestrator/pb"
)

func (s *orchestratorServer) changePhoneMaxFailureCount() int {
	if s.changePhoneMaxFailures <= 0 {
		return defaultChangePhoneMaxFailures
	}
	return s.changePhoneMaxFailures
}

func (s *orchestratorServer) changePhoneOTPWaitTimeoutSeconds() int32 {
	if s.changePhoneOTPWaitSeconds <= 0 {
		return defaultChangePhoneOTPWaitSeconds
	}
	return s.changePhoneOTPWaitSeconds
}

func (s *orchestratorServer) changePhoneOTPRetryCount() int {
	if s.changePhoneOTPRetryAttempts < 0 {
		return defaultChangePhoneOTPRetryAttempts
	}
	return s.changePhoneOTPRetryAttempts
}

func (s *orchestratorServer) changePhoneGetNumberRetryInterval() time.Duration {
	if s.changePhoneGetNumberRetryDelay < 0 {
		return defaultChangePhoneGetNumberRetryDelay
	}
	return s.changePhoneGetNumberRetryDelay
}

func (s *orchestratorServer) changePhoneSMSCancelWaitTimeout() time.Duration {
	if s.changePhoneSMSCancelTimeout <= 0 {
		return defaultChangePhoneSMSCancelTimeout
	}
	return s.changePhoneSMSCancelTimeout
}

func (s *orchestratorServer) changePhoneSMSCancelRetryDelay() time.Duration {
	if s.changePhoneSMSCancelRetryInterval <= 0 {
		return defaultChangePhoneSMSCancelRetryInterval
	}
	return s.changePhoneSMSCancelRetryInterval
}

func (s *orchestratorServer) recordChangePhoneFailure(ctx context.Context, activationID string, failures *int, reason string) error {
	if activationID != "" {
		if err := s.cancelSMSActivationBeforeRotation(ctx, activationID); err != nil {
			return fmt.Errorf("cancel SMS activation before phone rotation after %s: %w", reason, err)
		}
	}
	*failures++
	maxFailures := s.changePhoneMaxFailureCount()
	log.Printf("[gopay-app] Change phone retryable failure %d/%d: %s", *failures, maxFailures, reason)
	if *failures >= maxFailures {
		return fmt.Errorf("failed to change phone after %d consecutive failures: %s", maxFailures, reason)
	}
	return nil
}

func (s *orchestratorServer) recordCompletedChangePhoneFailure(ctx context.Context, activationID string, failures *int, reason string) error {
	if activationID != "" {
		s.finishSMSActivation(ctx, activationID)
	}
	*failures++
	maxFailures := s.changePhoneMaxFailureCount()
	log.Printf("[gopay-app] Change phone retryable failure %d/%d: %s", *failures, maxFailures, reason)
	if *failures >= maxFailures {
		return fmt.Errorf("failed to change phone after %d consecutive failures: %s", maxFailures, reason)
	}
	return nil
}

func (s *orchestratorServer) finishSMSActivation(ctx context.Context, activationID string) {
	if s.codeReceiverClient == nil || activationID == "" {
		return
	}
	resp, err := s.codeReceiverClient.FinishActivation(ctx, &pb.FinishActivationRequest{ActivationId: activationID})
	if err != nil {
		log.Printf("[gopay-app] FinishActivation failed: %v", err)
		return
	}
	if !resp.GetSuccess() {
		log.Printf("[gopay-app] FinishActivation failed: %s", resp.GetErrorMessage())
	}
}

func (s *orchestratorServer) cancelSMSActivationBeforeRotation(ctx context.Context, activationID string) error {
	if s.codeReceiverClient == nil {
		return fmt.Errorf("code receiver client not configured")
	}
	if activationID == "" {
		return fmt.Errorf("activation id missing")
	}

	deadline := time.Now().Add(s.changePhoneSMSCancelWaitTimeout())
	for {
		resp, err := s.codeReceiverClient.CancelActivation(ctx, &pb.CancelActivationRequest{ActivationId: activationID})
		if err != nil {
			return fmt.Errorf("CancelActivation: %w", err)
		}
		if smsCancelSettled(resp) {
			if resp != nil && !resp.GetSuccess() {
				log.Printf("[gopay-app] CancelActivation settled without ACCESS_CANCEL: %s", smsCancelResponseText(resp))
			}
			return nil
		}

		message := smsCancelResponseText(resp)
		if !smsEarlyCancelDenied(message) {
			return fmt.Errorf("CancelActivation: %s", message)
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("CancelActivation: %s", message)
		}
		delay := minDuration(s.changePhoneSMSCancelRetryDelay(), remaining)
		log.Printf("[gopay-app] CancelActivation denied too early; retrying in %s", delay)
		if err := sleepContext(ctx, delay); err != nil {
			return fmt.Errorf("waiting to retry CancelActivation: %w", err)
		}
	}
}

func (s *orchestratorServer) cancelSMSActivation(ctx context.Context, activationID string) {
	if s.codeReceiverClient == nil || activationID == "" {
		return
	}
	resp, err := s.codeReceiverClient.CancelActivation(ctx, &pb.CancelActivationRequest{ActivationId: activationID})
	if err != nil {
		log.Printf("[gopay-app] CancelActivation failed: %v", err)
		return
	}
	if !resp.GetSuccess() {
		log.Printf("[gopay-app] CancelActivation failed: %s", resp.GetErrorMessage())
	}
}

func smsCancelSettled(resp *pb.ProviderActionResponse) bool {
	if resp == nil {
		return false
	}
	if resp.GetSuccess() {
		return true
	}
	message := strings.ToUpper(smsCancelResponseText(resp))
	return strings.Contains(message, "NO_ACTIVATION") || strings.Contains(message, "STATUS_CANCEL")
}

func smsEarlyCancelDenied(message string) bool {
	return strings.Contains(strings.ToUpper(message), "EARLY_CANCEL_DENIED")
}

func smsCancelResponseText(resp *pb.ProviderActionResponse) string {
	if resp == nil {
		return "empty response"
	}
	parts := []string{}
	if resp.GetErrorMessage() != "" {
		parts = append(parts, resp.GetErrorMessage())
	}
	if resp.GetRawResponse() != "" {
		parts = append(parts, resp.GetRawResponse())
	}
	if len(parts) == 0 {
		return "unknown error"
	}
	return strings.Join(parts, ": ")
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func authOtpIssuedAfterUnix(resp *pb.StatusResponse, fallback int64) int64 {
	if resp == nil {
		return fallback
	}
	var issuedAfter int64
	switch strings.TrimSpace(resp.GetStage()) {
	case "login_otp_pending":
		issuedAfter = resp.GetLoginOtpSentAtUnix()
	case "signup_otp_pending":
		issuedAfter = resp.GetSignupOtpSentAtUnix()
	case "signup_pin_otp_pending":
		issuedAfter = resp.GetSignupPinOtpSentAtUnix()
	}
	if issuedAfter > 0 {
		return issuedAfter
	}
	return fallback
}
