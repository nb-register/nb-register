package runtime

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"orchestrator/pb"
)

func (s *orchestratorServer) GoPayPaymentStartActivity(ctx context.Context, input GoPayActivityInput) (GoPayPaymentStartOutput, error) {
	output := GoPayPaymentStartOutput{}
	account, err := s.getAccount(ctx, input.GetAccountId())
	if err != nil {
		return output, err
	}
	if err := accountEligibleForActivation(account); err != nil {
		return output, err
	}
	step, err := s.startActivityStep(ctx, input.GetJobId(), stepGoPayPayment, false, true)
	if err != nil {
		return output, err
	}

	output, err = s.startGoPayPayment(ctx, step, input, account)
	if err != nil {
		return output, s.completeGoPayPaymentStep(ctx, input.GetJobId(), input.GetAccountId(), protoDataMap(output.GetData()), err)
	}
	step.update(protoDataMap(output.GetData()))
	return output, nil
}

func (s *orchestratorServer) GoPayPaymentCompleteActivity(ctx context.Context, input GoPayPaymentCompleteInput) (GoPayActivityOutput, error) {
	step := s.activityStep(ctx, input.GetJobId(), stepGoPayPayment, false, true)
	data := protoDataMap(input.GetData())
	if data == nil {
		data = map[string]any{}
	}
	data["payment_otp"] = map[string]any{
		"timeout_seconds":    s.paymentOtpTimeout(),
		"issued_after_unix":  input.GetOtpIssuedAfterUnix(),
		"found":              true,
		"source":             input.GetOtpSource(),
		"manual_allowed":     true,
		"otp_value_recorded": false,
	}
	step.progress("payment otp received", map[string]any{
		"source": input.GetOtpSource(),
	})

	otp, err := s.consumeStoredOTP(ctx, input.GetJobId(), input.GetOtpParam(), input.GetSubmittedAtParam(), input.GetOtpIssuedAfterUnix())
	if err != nil {
		return GoPayActivityOutput{Data: protoData(data)}, s.completeGoPayPaymentStep(ctx, input.GetJobId(), input.GetAccountId(), data, err)
	}

	result, err := s.completeGoPayPayment(ctx, step, input.GetFlowId(), otp, input.GetUseAccountToken(), data)
	if err != nil {
		return GoPayActivityOutput{Data: protoData(data)}, s.completeGoPayPaymentStep(ctx, input.GetJobId(), input.GetAccountId(), data, err)
	}

	output := GoPayActivityOutput{
		ChargeRef:         result.GetChargeRef(),
		SnapToken:         result.GetSnapToken(),
		PlusTrialEligible: true,
		PlusTrialChecked:  true,
		PlusActive:        true,
		Data:              protoData(data),
	}
	return output, step.complete(data, nil)
}

func (s *orchestratorServer) GoPayPaymentCancelActivity(ctx context.Context, input GoPayPaymentCancelInput) error {
	if strings.TrimSpace(input.GetFlowId()) == "" {
		return nil
	}
	resp, err := s.paymentClient.CancelGoPay(ctx, &pb.CancelGoPayRequest{FlowId: input.GetFlowId()})
	if err != nil {
		return err
	}
	if resp != nil && !resp.GetSuccess() {
		return fmt.Errorf("payment cancel failed: %s", resp.GetErrorMessage())
	}
	return nil
}

func (s *orchestratorServer) startGoPayPayment(ctx context.Context, step activityStep, input GoPayActivityInput, account *pb.Account) (output GoPayPaymentStartOutput, err error) {
	sessionToken := strings.TrimSpace(input.GetSessionToken())
	if sessionToken == "" {
		sessionToken = strings.TrimSpace(account.GetSessionToken())
	}
	accessToken := strings.TrimSpace(input.GetAccessToken())
	if accessToken == "" {
		accessToken = strings.TrimSpace(account.GetAccessToken())
	}
	tokenization := strings.TrimSpace(input.GetTokenization())
	checkoutURL := strings.TrimSpace(input.GetCheckoutUrl())
	checkoutSessionID := strings.TrimSpace(input.GetCheckoutSessionId())
	useAccountToken := input.GetUseAccountToken()
	accountPhone := ""

	data := map[string]any{
		"account_id":             account.GetAccountId(),
		"session_token_present":  sessionToken != "",
		"access_token_present":   accessToken != "",
		"used_account_token":     useAccountToken,
		"tokenization":           tokenization,
		"checkout_url_present":   checkoutURL != "",
		"checkout_session_id":    checkoutSessionID,
		"otp_value_recorded":     false,
		"payment_result_present": false,
	}
	output = GoPayPaymentStartOutput{
		OtpTimeoutSeconds: s.paymentOtpTimeout(),
		UseAccountToken:   useAccountToken,
	}
	defer func() {
		output.Data = protoData(data)
	}()
	if !useAccountToken && sessionToken == "" && accessToken == "" {
		return output, fmt.Errorf("session_token or access_token is required")
	}

	step.progress("starting gopay payment", map[string]any{
		"use_account_token": useAccountToken,
		"tokenization":      tokenization,
	})
	if useAccountToken {
		data["account_balance_check"] = map[string]any{"required_before_payment": true}
		step.progress("waiting for gopay min balance", nil)
		if err := s.waitForGoPayMinBalance(ctx, step); err != nil {
			data["account_balance_check"] = map[string]any{
				"required_before_payment": true,
				"ready":                   false,
				"error_message":           err.Error(),
			}
			return output, err
		}
		data["account_balance_check"] = map[string]any{
			"required_before_payment": true,
			"ready":                   true,
		}

		accountToken, phone, err := s.readyGoPayAccountToken(ctx)
		if err != nil {
			data["account_token"] = map[string]any{
				"ready":         false,
				"error_message": err.Error(),
			}
			return output, err
		}
		accountPhone = phone
		if accountPhone == "" {
			accountPhone = configuredGoPayPhone()
		}
		if accountPhone == "" {
			err := fmt.Errorf("account phone is required")
			data["account_token"] = map[string]any{
				"ready":         true,
				"phone_present": false,
				"error_message": err.Error(),
			}
			return output, err
		}
		sessionToken = ""
		accessToken = accountToken
		data["session_token_present"] = false
		data["access_token_present"] = accessToken != ""
		data["account_token"] = map[string]any{
			"ready":         true,
			"phone_present": accountPhone != "",
			"phone":         accountPhone,
		}
	}

	started, err := s.paymentClient.StartGoPay(ctx, &pb.StartGoPayRequest{
		SessionToken:      sessionToken,
		AccessToken:       accessToken,
		UseAccountToken:   useAccountToken,
		Tokenization:      tokenization,
		CheckoutUrl:       checkoutURL,
		CheckoutSessionId: checkoutSessionID,
		GopayPhone:        accountPhone,
	})
	data["payment_start"] = paymentStartData(started)
	if started != nil {
		output.FlowId = started.GetFlowId()
		output.IssuedAfterUnix = started.GetIssuedAfterUnix()
	}
	step.progress("gopay payment started", map[string]any{
		"success":           started != nil && started.GetSuccess(),
		"issued_after_unix": output.GetIssuedAfterUnix(),
	})
	if err != nil {
		return output, err
	}
	if started == nil {
		return output, fmt.Errorf("payment start returned empty response")
	}
	if !started.GetSuccess() {
		return output, fmt.Errorf("payment start failed: %s", started.GetErrorMessage())
	}
	if output.GetFlowId() == "" {
		return output, fmt.Errorf("payment start returned empty flow_id")
	}
	return output, nil
}

func (s *orchestratorServer) completeGoPayPayment(ctx context.Context, step activityStep, flowID, otp string, useAccountToken bool, data map[string]any) (*pb.GoPayResponse, error) {
	completed := false
	defer func() {
		if flowID != "" && !completed {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cancelResp, cancelErr := s.paymentClient.CancelGoPay(cancelCtx, &pb.CancelGoPayRequest{FlowId: flowID})
			data["cleanup"] = cleanupDataFromPayment(cancelResp, cancelErr)
		}
	}()

	result, err := s.paymentClient.CompleteGoPay(ctx, &pb.CompleteGoPayRequest{FlowId: flowID, Otp: otp})
	data["payment_complete"] = paymentResultData(result)
	data["payment_result_present"] = result != nil
	step.progress("gopay payment complete called", map[string]any{
		"success":                      result != nil && result.GetSuccess(),
		"awaiting_manual_confirmation": result != nil && result.GetAwaitingManualConfirmation(),
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("payment complete returned empty response")
	}
	if !result.GetSuccess() {
		return nil, fmt.Errorf("payment complete failed: %s", result.GetErrorMessage())
	}
	if result.GetAwaitingManualConfirmation() {
		data["manual_payment_confirmation"] = map[string]any{
			"required":      true,
			"auto_expected": true,
			"confirmed":     false,
		}
		if !useAccountToken {
			return nil, fmt.Errorf("payment requires manual confirmation; QR autopay did not settle automatically")
		}

		replayResp, replayErr := s.replayGoPayPaymentLink(ctx, result)
		data["gopay_link_payment"] = replayLinkPaymentData(replayResp, replayErr)
		step.progress("gopay link payment replayed", map[string]any{
			"success": replayResp != nil && replayResp.GetSuccess(),
		})
		if replayErr != nil {
			return nil, replayErr
		}

		result, err = s.paymentClient.ConfirmGoPayPayment(ctx, &pb.ConfirmGoPayPaymentRequest{FlowId: flowID})
		data["payment_confirm"] = paymentResultData(result)
		data["payment_result_present"] = result != nil
		step.progress("gopay payment confirmed", map[string]any{
			"success": result != nil && result.GetSuccess(),
		})
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, fmt.Errorf("payment confirm returned empty response")
		}
		if !result.GetSuccess() {
			return nil, fmt.Errorf("payment confirm failed: %s", result.GetErrorMessage())
		}
		data["manual_payment_confirmation"] = map[string]any{
			"required":      true,
			"auto_expected": true,
			"confirmed":     true,
		}
	}
	completed = true

	if useAccountToken {
		if unlinkErr := s.unlinkGoPayAccountToken(ctx); unlinkErr != nil {
			log.Printf("[gopay-app] Unlink after payment failed: %v", unlinkErr)
			data["gopay_unlink"] = cleanupData(false, unlinkErr.Error(), unlinkErr)
		} else {
			data["gopay_unlink"] = cleanupData(true, "", nil)
		}
	}
	return result, nil
}

func (s *orchestratorServer) completeGoPayPaymentStep(ctx context.Context, jobID, accountID string, data map[string]any, err error) error {
	if err != nil && isFreeTrialIneligibleError(err) {
		if updateErr := s.updateAccount(ctx, &pb.Account{
			AccountId:         accountID,
			PlusTrialEligible: boolPtr(false),
		}); updateErr != nil {
			err = fmt.Errorf("%w; additionally failed to mark plus trial ineligible: %v", err, updateErr)
		}
	}
	return s.completeActivityStep(ctx, jobID, stepGoPayPayment, false, true, data, err)
}
