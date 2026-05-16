package app

import (
	"context"
	"fmt"
	"strings"

	pb "orchestrator/pb"
)

func (s *orchestratorServer) GoPayAppChangePhoneStartActivity(ctx context.Context, input GoPayAppChangePhoneStartInput) (GoPayAppChangePhoneStartOutput, error) {
	output := GoPayAppChangePhoneStartOutput{}
	data := map[string]any{}
	step := s.activityStep(ctx, input.GetJobId(), stepGoPayAppChangePhoneStart, false, true)
	_, err := step.run(func() (any, error) {
		if s.changePhoneDisabled {
			err := fmt.Errorf("gopay change phone disabled by GOPAY_CHANGE_PHONE_DISABLED")
			data["error_message"] = err.Error()
			return data, err
		}
		if s.gopayClient == nil || s.codeReceiverClient == nil {
			err := fmt.Errorf("gopay app or code receiver client not configured")
			data["error_message"] = err.Error()
			return data, err
		}

		statusBefore, statusErr := s.goPayStatus(ctx)
		data["status_before"] = goPayStatusSnapshotData(goPayStatusSnapshot(statusBefore, statusErr))
		if statusErr != nil {
			data["error_message"] = statusErr.Error()
			return data, statusErr
		}

		maxFailures := s.changePhoneMaxFailureCount()
		failures := int(input.GetFailureCount())
		if failures < 0 {
			failures = 0
		}
		otpWaitSeconds := s.changePhoneOTPWaitTimeoutSeconds()
		otpRetryAttempts := s.changePhoneOTPRetryCount()
		data["failure_count"] = failures
		data["max_failures"] = maxFailures
		data["otp_timeout_seconds"] = otpWaitSeconds
		data["otp_retry_attempts"] = otpRetryAttempts

		for failures < maxFailures {
			step.progress("acquiring phone number", map[string]any{
				"failures":     failures,
				"max_failures": maxFailures,
			})
			numResp, err := s.codeReceiverClient.AcquireNumber(ctx, &pb.AcquireNumberRequest{})
			if err != nil {
				err = fmt.Errorf("GetNumber: %w", err)
				data["error_message"] = err.Error()
				return data, err
			}
			if numResp == nil || !numResp.GetSuccess() {
				message := ""
				if numResp != nil {
					message = numResp.GetErrorMessage()
				}
				if message == "" {
					message = "empty response"
				}
				if err := s.recordChangePhoneFailure(ctx, "", &failures, fmt.Sprintf("GetNumber: %s", message)); err != nil {
					data["failure_count"] = failures
					data["error_message"] = err.Error()
					return data, err
				}
				data["failure_count"] = failures
				if delay := s.changePhoneGetNumberRetryInterval(); delay > 0 {
					if err := sleepContext(ctx, delay); err != nil {
						err = fmt.Errorf("waiting to retry GetNumber: %w", err)
						data["error_message"] = err.Error()
						return data, err
					}
				}
				continue
			}

			phone := normalizeIndonesiaPhone(numResp.GetPhone())
			activationID := numResp.GetActivationId()
			data["activation_id"] = activationID
			data["phone_present"] = phone != ""
			step.progress("phone number acquired", map[string]any{
				"activation_id": activationID,
				"phone_present": phone != "",
			})
			if phone == "" {
				if err := s.recordChangePhoneFailure(ctx, activationID, &failures, "empty phone from SMS service"); err != nil {
					data["failure_count"] = failures
					data["error_message"] = err.Error()
					return data, err
				}
				data["failure_count"] = failures
				continue
			}

			checkResp, err := s.gopayClient.CheckPhone(ctx, &pb.CheckPhoneRequest{Phone: phone})
			if err != nil {
				if cancelErr := s.recordChangePhoneFailure(ctx, activationID, &failures, fmt.Sprintf("CheckPhone: %v", err)); cancelErr != nil {
					data["failure_count"] = failures
					data["error_message"] = cancelErr.Error()
					return data, cancelErr
				}
				data["failure_count"] = failures
				continue
			}
			status := checkPhoneStatus(checkResp)
			data["phone_status"] = status
			step.progress("phone availability checked", map[string]any{
				"activation_id": activationID,
				"status":        status,
			})
			if status != "available" {
				reason := fmt.Sprintf("CheckPhone status=%s", status)
				if checkResp != nil && checkResp.GetErrorMessage() != "" {
					reason = fmt.Sprintf("%s: %s", reason, checkResp.GetErrorMessage())
				}
				if err := s.recordChangePhoneFailure(ctx, activationID, &failures, reason); err != nil {
					data["failure_count"] = failures
					data["error_message"] = err.Error()
					return data, err
				}
				data["failure_count"] = failures
				continue
			}

			stateJSON, err := s.loadGoPayAppState(ctx)
			if err != nil {
				s.cancelSMSActivation(ctx, activationID)
				data["error_message"] = err.Error()
				return data, err
			}
			pin := configuredGoPayPIN()
			if pin == "" {
				s.cancelSMSActivation(ctx, activationID)
				err := fmt.Errorf("GOPAY_PIN is required")
				data["error_message"] = err.Error()
				return data, err
			}
			changeResp, err := s.gopayClient.ChangePhoneStart(ctx, &pb.ChangePhoneStartRequest{
				NewPhone:  phone,
				Pin:       pin,
				StateJson: stateJSON,
			})
			if err == nil && changeResp != nil {
				err = s.saveGoPayAppState(ctx, changeResp.GetStateJson())
			}
			if err != nil {
				s.cancelSMSActivation(ctx, activationID)
				err = fmt.Errorf("ChangePhoneStart: %w", err)
				data["error_message"] = err.Error()
				return data, err
			}
			if changeResp == nil {
				s.cancelSMSActivation(ctx, activationID)
				err := fmt.Errorf("ChangePhoneStart returned empty response")
				data["error_message"] = err.Error()
				return data, err
			}
			if !changeResp.GetSuccess() {
				if changeResp.GetErrorMessage() == "PHONE_REGISTERED" || changeResp.GetErrorMessage() == "PHONE_EXHAUSTED" {
					if err := s.recordChangePhoneFailure(ctx, activationID, &failures, fmt.Sprintf("ChangePhoneStart: %s", changeResp.GetErrorMessage())); err != nil {
						data["failure_count"] = failures
						data["error_message"] = err.Error()
						return data, err
					}
					data["failure_count"] = failures
					continue
				}
				s.cancelSMSActivation(ctx, activationID)
				err := fmt.Errorf("ChangePhoneStart: %s", changeResp.GetErrorMessage())
				data["error_message"] = err.Error()
				return data, err
			}

			step.progress("change phone otp sent", map[string]any{
				"activation_id": activationID,
			})
			if sentResp, err := s.codeReceiverClient.MarkMessageSent(ctx, &pb.MarkMessageSentRequest{ActivationId: activationID}); err != nil {
				s.cancelSMSActivation(ctx, activationID)
				err = fmt.Errorf("MarkMessageSent: %w", err)
				data["error_message"] = err.Error()
				return data, err
			} else if sentResp == nil || !sentResp.GetSuccess() {
				s.cancelSMSActivation(ctx, activationID)
				message := ""
				if sentResp != nil {
					message = sentResp.GetErrorMessage()
				}
				if message == "" {
					message = "empty response"
				}
				err := fmt.Errorf("MarkMessageSent: %s", message)
				data["error_message"] = err.Error()
				return data, err
			}

			output.ActivationId = activationID
			output.Phone = phone
			output.FailureCount = int32(failures)
			output.MaxFailures = int32(maxFailures)
			output.OtpTimeoutSeconds = otpWaitSeconds
			output.OtpRetryAttempts = int32(otpRetryAttempts)
			data["failure_count"] = failures
			data["change_phone_start_complete"] = true
			return data, nil
		}

		err := fmt.Errorf("failed to change phone after %d consecutive failures", maxFailures)
		data["failure_count"] = failures
		data["error_message"] = err.Error()
		return data, err
	})
	output.Data = protoData(data)
	return output, err
}

func (s *orchestratorServer) GoPayAppChangePhoneRetryActivity(ctx context.Context, input GoPayAppChangePhoneRetryInput) (GoPayAppChangePhoneRetryOutput, error) {
	output := GoPayAppChangePhoneRetryOutput{ActivationId: input.GetActivationId()}
	data := map[string]any{
		"activation_id": input.GetActivationId(),
		"otp_attempt":   input.GetOtpAttempt(),
	}
	step := s.activityStep(ctx, input.GetJobId(), stepGoPayAppChangePhoneRetry, false, true)
	_, err := step.run(func() (any, error) {
		if s.gopayClient == nil {
			err := fmt.Errorf("gopay app client not configured")
			data["error_message"] = err.Error()
			return data, err
		}
		stateJSON, err := s.loadGoPayAppState(ctx)
		if err != nil {
			data["error_message"] = err.Error()
			return data, err
		}
		retryResp, err := s.gopayClient.ChangePhoneRetry(ctx, &pb.ChangePhoneRetryRequest{StateJson: stateJSON})
		if err == nil && retryResp != nil {
			err = s.saveGoPayAppState(ctx, retryResp.GetStateJson())
		}
		if err != nil {
			err = fmt.Errorf("ChangePhoneRetry: %w", err)
			data["error_message"] = err.Error()
			return data, err
		}
		if retryResp == nil {
			err := fmt.Errorf("ChangePhoneRetry returned empty response")
			data["error_message"] = err.Error()
			return data, err
		}
		output.OtpSent = retryResp.GetSuccess() && retryResp.GetOtpSent()
		if !retryResp.GetSuccess() {
			output.ErrorMessage = retryResp.GetErrorMessage()
			data["error_message"] = retryResp.GetErrorMessage()
		}
		data["otp_sent"] = output.GetOtpSent()
		return data, nil
	})
	output.Data = protoData(data)
	return output, err
}

func (s *orchestratorServer) GoPayAppSMSCancelBeforeRotationActivity(ctx context.Context, input GoPayAppSMSActivationInput) (GoPayAppSMSActivationOutput, error) {
	output := GoPayAppSMSActivationOutput{ActivationId: input.GetActivationId()}
	data := map[string]any{
		"activation_id": input.GetActivationId(),
		"reason":        input.GetReason(),
	}
	step := s.activityStep(ctx, input.GetJobId(), stepGoPayAppChangePhoneCancel, false, true)
	_, err := step.run(func() (any, error) {
		failures := int(input.GetFailureCount())
		reason := input.GetReason()
		if reason == "" {
			reason = "change phone code not received"
		}
		if err := s.recordChangePhoneFailure(ctx, input.GetActivationId(), &failures, reason); err != nil {
			output.FailureCount = int32(failures)
			output.MaxFailures = int32(s.changePhoneMaxFailureCount())
			output.ErrorMessage = err.Error()
			data["failure_count"] = failures
			data["max_failures"] = s.changePhoneMaxFailureCount()
			data["error_message"] = err.Error()
			return data, err
		}
		output.FailureCount = int32(failures)
		output.MaxFailures = int32(s.changePhoneMaxFailureCount())
		data["failure_count"] = failures
		data["max_failures"] = s.changePhoneMaxFailureCount()
		return data, nil
	})
	output.Data = protoData(data)
	return output, err
}

func (s *orchestratorServer) GoPayAppSMSFinishActivity(ctx context.Context, input GoPayAppSMSActivationInput) (GoPayAppSMSActivationOutput, error) {
	output := GoPayAppSMSActivationOutput{ActivationId: input.GetActivationId()}
	data := map[string]any{
		"activation_id": input.GetActivationId(),
		"reason":        input.GetReason(),
	}
	step := s.activityStep(ctx, input.GetJobId(), stepGoPayAppDeactivateSMSFinish, false, true)
	_, err := step.run(func() (any, error) {
		if input.GetActivationId() == "" {
			err := fmt.Errorf("activation id missing")
			data["error_message"] = err.Error()
			return data, err
		}
		s.finishSMSActivation(ctx, input.GetActivationId())
		data["finished"] = true
		return data, nil
	})
	output.Data = protoData(data)
	return output, err
}

func (s *orchestratorServer) GoPayAppChangePhoneCompleteActivity(ctx context.Context, input GoPayAppChangePhoneCompleteInput) (GoPayAppChangePhoneCompleteOutput, error) {
	output := GoPayAppChangePhoneCompleteOutput{ActivationId: input.GetActivationId()}
	data := map[string]any{
		"activation_id": input.GetActivationId(),
	}
	step := s.activityStep(ctx, input.GetJobId(), stepGoPayAppChangePhoneComplete, false, true)
	_, err := step.run(func() (any, error) {
		if s.gopayClient == nil {
			err := fmt.Errorf("gopay app client not configured")
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
			err := fmt.Errorf("WaitCode returned empty code")
			data["error_message"] = err.Error()
			return data, err
		}
		stateJSON, err := s.loadGoPayAppState(ctx)
		if err != nil {
			s.finishSMSActivation(ctx, input.GetActivationId())
			data["error_message"] = err.Error()
			return data, err
		}
		completeResp, err := s.gopayClient.ChangePhoneComplete(ctx, &pb.ChangePhoneCompleteRequest{Otp: input.GetCode(), StateJson: stateJSON})
		if err == nil && completeResp != nil {
			err = s.saveGoPayAppState(ctx, completeResp.GetStateJson())
		}
		if err != nil {
			s.finishSMSActivation(ctx, input.GetActivationId())
			err = fmt.Errorf("ChangePhoneComplete: %w", err)
			data["error_message"] = err.Error()
			return data, err
		}
		if completeResp == nil {
			s.finishSMSActivation(ctx, input.GetActivationId())
			err := fmt.Errorf("ChangePhoneComplete returned empty response")
			data["error_message"] = err.Error()
			return data, err
		}
		if !completeResp.GetSuccess() {
			failures := int(input.GetFailureCount())
			reason := fmt.Sprintf("ChangePhoneComplete: %s", completeResp.GetErrorMessage())
			if err := s.recordCompletedChangePhoneFailure(ctx, input.GetActivationId(), &failures, reason); err != nil {
				output.FailureCount = int32(failures)
				output.MaxFailures = int32(s.changePhoneMaxFailureCount())
				output.RetryableFailure = true
				output.ErrorMessage = err.Error()
				data["failure_count"] = failures
				data["max_failures"] = s.changePhoneMaxFailureCount()
				data["retryable_failure"] = true
				data["error_message"] = err.Error()
				return data, err
			}
			output.FailureCount = int32(failures)
			output.MaxFailures = int32(s.changePhoneMaxFailureCount())
			output.RetryableFailure = true
			output.ErrorMessage = reason
			data["failure_count"] = failures
			data["max_failures"] = s.changePhoneMaxFailureCount()
			data["retryable_failure"] = true
			data["error_message"] = reason
			return data, nil
		}

		statusAfter, statusErr := s.goPayStatus(ctx)
		data["status_after"] = goPayStatusSnapshotData(goPayStatusSnapshot(statusAfter, statusErr))
		if statusErr != nil {
			data["error_message"] = statusErr.Error()
			return data, statusErr
		}
		output.ChangePhoneComplete = true
		output.Stage = statusAfter.GetStage()
		output.Phone = statusAfter.GetPhone()
		data["change_phone_complete"] = true
		step.progress("phone changed", map[string]any{
			"activation_id": input.GetActivationId(),
		})
		return data, nil
	})
	output.Data = protoData(data)
	return output, err
}
