package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/internal/otpwait"
	"orchestrator/pb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	otpWaitChannelEmail   = otpwait.ChannelEmail
	otpWaitChannelPayment = otpwait.ChannelPayment
	otpWaitChannelSMS     = otpwait.ChannelSMS
)

func (s *orchestratorServer) OTPWaitActivity(ctx context.Context, input OTPWaitInput) (OTPWaitOutput, error) {
	switch otpWaitInputChannel(input) {
	case otpWaitChannelEmail:
		return s.waitEmailOTP(ctx, input)
	case otpWaitChannelPayment:
		return s.waitPaymentWebhookOTP(ctx, input)
	case otpWaitChannelSMS:
		return s.waitSMSOTP(ctx, input)
	default:
		return OTPWaitOutput{}, fmt.Errorf("otp wait target missing")
	}
}

func otpWaitInputChannel(input OTPWaitInput) string {
	return otpwait.Channel(&input)
}

func (s *orchestratorServer) waitEmailOTP(ctx context.Context, input OTPWaitInput) (OTPWaitOutput, error) {
	target := input.GetEmail()
	email := ""
	if target != nil {
		email = strings.TrimSpace(target.GetEmail())
	}
	if email == "" {
		return OTPWaitOutput{}, fmt.Errorf("email otp target missing")
	}
	timeoutSeconds := input.GetTimeoutSeconds()
	if timeoutSeconds <= 0 {
		timeoutSeconds = 120
	}
	data := map[string]any{
		"channel":           otpWaitChannelEmail,
		"email":             email,
		"timeout_seconds":   timeoutSeconds,
		"issued_after_unix": input.GetIssuedAfterUnix(),
	}
	step := s.activityStep(ctx, input.GetJobId(), input.GetStepName(), false, true)
	step.progress("waiting for email otp", data)
	stopHeartbeat := startActivityHeartbeat(ctx, input.GetJobId(), input.GetStepName(), "waiting for email otp", data)
	defer stopHeartbeat()
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds+5)*time.Second)
	defer cancel()
	resp, err := s.emailClient.WaitForEmail(reqCtx, &pb.WaitForEmailRequest{
		EmailAddress:    email,
		TimeoutSeconds:  timeoutSeconds,
		IssuedAfterUnix: input.GetIssuedAfterUnix(),
	})
	if err != nil {
		data["error_message"] = err.Error()
		return OTPWaitOutput{Data: protoData(data)}, err
	}
	if resp == nil {
		err := fmt.Errorf("email service returned empty otp response")
		data["error_message"] = err.Error()
		return OTPWaitOutput{Data: protoData(data)}, err
	}
	code := normalizeOTP(resp.GetContentExtracted())
	if resp.GetFound() && code != "" {
		if err := s.setJobParams(ctx, input.GetJobId(), map[string]string{
			input.GetOtpParam():         code,
			input.GetSubmittedAtParam(): fmt.Sprintf("%d", time.Now().Unix()),
		}); err != nil {
			data["error_message"] = err.Error()
			return OTPWaitOutput{Data: protoData(data)}, err
		}
		data["found"] = true
		return OTPWaitOutput{Found: true, Source: otpWaitChannelEmail, Data: protoData(data)}, nil
	}
	err = fmt.Errorf("email otp not found")
	data["found"] = false
	data["error_message"] = err.Error()
	return OTPWaitOutput{Data: protoData(data)}, err
}

func (s *orchestratorServer) waitPaymentWebhookOTP(ctx context.Context, input OTPWaitInput) (OTPWaitOutput, error) {
	target := input.GetPayment()
	if target == nil {
		return OTPWaitOutput{}, fmt.Errorf("payment otp target missing")
	}
	addr := s.otpAddr
	if addr == "" {
		addr = "whatsapp-otp-relay:50051"
	}
	purpose := strings.TrimSpace(target.GetPurpose())
	if purpose == "" {
		var err error
		purpose, err = goPayOTPQueueKey(target.GetSource(), "gopay")
		if err != nil {
			return OTPWaitOutput{}, err
		}
	}
	timeoutSeconds := input.GetTimeoutSeconds()
	if timeoutSeconds <= 0 {
		timeoutSeconds = s.paymentOtpTimeout()
	}

	type otpServiceResult struct {
		code string
		err  error
	}
	otpCtx, cancelOTP := context.WithCancel(ctx)
	defer cancelOTP()

	otpCh := make(chan otpServiceResult, 1)
	go func() {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			otpCh <- otpServiceResult{err: err}
			return
		}
		defer conn.Close()

		reqCtx, cancel := context.WithTimeout(otpCtx, time.Duration(timeoutSeconds+10)*time.Second)
		defer cancel()

		resp, err := pb.NewOtpServiceClient(conn).WaitForOtp(reqCtx, &pb.WaitForOtpRequest{
			Purpose:         purpose,
			TimeoutSeconds:  timeoutSeconds,
			IssuedAfterUnix: input.GetIssuedAfterUnix(),
		})
		if err != nil {
			otpCh <- otpServiceResult{err: fmt.Errorf("otp not received after %ds: %w", timeoutSeconds, err)}
			return
		}
		code := ""
		if resp != nil {
			code = normalizeOTP(resp.GetOtp())
		}
		if resp != nil && resp.GetFound() && code != "" {
			otpCh <- otpServiceResult{code: code}
			return
		}
		lastErr := ""
		if resp != nil {
			lastErr = resp.GetErrorMessage()
		}
		if lastErr == "" {
			lastErr = "otp not found"
		}
		otpCh <- otpServiceResult{err: fmt.Errorf("otp not received after %ds: %s", timeoutSeconds, lastErr)}
	}()

	deadline := time.NewTimer(time.Duration(timeoutSeconds) * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var heartbeatAt time.Time
	var lastErr error
	data := map[string]any{
		"channel":           otpWaitChannelPayment,
		"purpose":           purpose,
		"timeout_seconds":   timeoutSeconds,
		"issued_after_unix": input.GetIssuedAfterUnix(),
	}
	step := s.activityStep(ctx, input.GetJobId(), input.GetStepName(), false, true)

	for {
		select {
		case otpResult := <-otpCh:
			code := normalizeOTP(otpResult.code)
			if code != "" {
				if err := s.setJobParams(ctx, input.GetJobId(), map[string]string{
					input.GetOtpParam():         code,
					input.GetSubmittedAtParam(): fmt.Sprintf("%d", time.Now().Unix()),
				}); err != nil {
					data["error_message"] = err.Error()
					return OTPWaitOutput{Data: protoData(data)}, err
				}
				data["found"] = true
				return OTPWaitOutput{Found: true, Source: "webhook", Data: protoData(data)}, nil
			}
			if otpResult.err != nil {
				lastErr = otpResult.err
			}
			otpCh = nil
		case <-ticker.C:
			step.progressEvery(&heartbeatAt, "waiting for payment otp", data)
		case <-deadline.C:
			if lastErr != nil {
				err := fmt.Errorf("payment otp not received after %ds: %w", timeoutSeconds, lastErr)
				data["error_message"] = err.Error()
				return OTPWaitOutput{Data: protoData(data)}, err
			}
			err := fmt.Errorf("payment otp not received after %ds", timeoutSeconds)
			data["error_message"] = err.Error()
			return OTPWaitOutput{Data: protoData(data)}, err
		case <-ctx.Done():
			return OTPWaitOutput{Data: protoData(data)}, ctx.Err()
		}
	}
}

func (s *orchestratorServer) waitSMSOTP(ctx context.Context, input OTPWaitInput) (OTPWaitOutput, error) {
	target := input.GetSms()
	activationID := ""
	if target != nil {
		activationID = strings.TrimSpace(target.GetActivationId())
	}
	output := OTPWaitOutput{
		ActivationId: activationID,
		Source:       otpWaitChannelSMS,
	}
	data := map[string]any{
		"channel":       otpWaitChannelSMS,
		"activation_id": activationID,
	}
	stepName := input.GetStepName()
	if stepName == "" {
		stepName = stepGoPayAppChangePhoneSMSWait
	}
	step := s.activityStep(ctx, input.GetJobId(), stepName, false, true)
	_, err := step.run(func() (any, error) {
		if s.codeReceiverClient == nil {
			err := fmt.Errorf("code receiver client not configured")
			data["error_message"] = err.Error()
			return data, err
		}
		if activationID == "" {
			err := fmt.Errorf("activation id missing")
			data["error_message"] = err.Error()
			return data, err
		}
		timeoutSeconds := input.GetTimeoutSeconds()
		if timeoutSeconds <= 0 {
			timeoutSeconds = defaultChangePhoneOTPWaitSeconds
		}
		data["timeout_seconds"] = timeoutSeconds
		step.progress("waiting for sms otp", data)
		stopHeartbeat := startActivityHeartbeat(ctx, input.GetJobId(), stepName, "waiting for sms otp", data)
		defer stopHeartbeat()
		otpResp, err := s.codeReceiverClient.WaitCode(ctx, &pb.WaitCodeRequest{
			ActivationId:   activationID,
			TimeoutSeconds: timeoutSeconds,
		})
		if err != nil {
			err = fmt.Errorf("WaitCode: %w", err)
			data["error_message"] = err.Error()
			return data, err
		}
		if otpResp != nil && otpResp.GetSuccess() && strings.TrimSpace(otpResp.GetCode()) != "" {
			output.Found = true
			output.Code = normalizeOTP(otpResp.GetCode())
			data["found"] = true
			return data, nil
		}
		message := ""
		if otpResp != nil {
			message = otpResp.GetErrorMessage()
		}
		if message == "" {
			message = "otp not found"
		}
		output.ErrorMessage = message
		data["found"] = false
		data["error_message"] = message
		return data, nil
	})
	output.Data = protoData(data)
	return output, err
}
