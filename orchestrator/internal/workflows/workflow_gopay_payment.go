package workflows

import (
	"errors"
	"fmt"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func GoPayPaymentWorkflow(ctx workflow.Context, input GoPayPaymentWorkflowInput) (GoPayPaymentWorkflowResult, error) {
	if normalizeGoPayOTPChannel(input.GetOtpChannel()) == "wa" {
		return goPayWAPaymentWorkflow(ctx, input)
	}
	return goPaySMSPaymentWorkflow(ctx, input)
}

func goPaySMSPaymentWorkflow(ctx workflow.Context, input GoPayPaymentWorkflowInput) (GoPayPaymentWorkflowResult, error) {
	progress := newWorkflowProgress(ctx, "GoPayPaymentWorkflow", input.GetJobId())
	result := GoPayPaymentWorkflowResult{JobId: input.GetJobId()}
	defer func() {
		finishWorkflowProgressOnError(ctx, progress, result.GetErrorMessage())
	}()

	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	atomicCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(15*time.Minute))
	gopayCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(30*time.Minute))
	paymentCtx := workflow.WithActivityOptions(ctx, paymentActivityOptions())
	tierCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(2*time.Minute))

	otpChannel := normalizeGoPayOTPChannel(input.GetOtpChannel())
	if otpChannel == "" {
		otpChannel = "sms"
	}
	userID := strings.TrimSpace(input.GetUserId())
	if userID == "" {
		userID = goPayLocalSource
	}
	result.UserId = userID
	addBalance := input.GetAddBalance()
	addBalanceMethod := goPayAddBalanceMethod(addBalance)
	stateJSON := "{}"
	combined := map[string]any{
		"otp_channel":        otpChannel,
		"user_id":            userID,
		"add_balance_method": addBalanceMethod,
	}

	var account AccountRef
	setWorkflowProgress(ctx, progress, "resolve_account")
	if err := workflow.ExecuteActivity(retryCtx, resolveAccountActivityName, ResolveAccountInput{
		AccountId:   input.GetAccountId(),
		SourceJobId: input.GetSourceJobId(),
	}).Get(ctx, &account); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}
	combined["account_id"] = account.GetAccountId()

	setWorkflowProgress(ctx, progress, "create_job")
	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobId:     input.GetJobId(),
		AccountId: account.GetAccountId(),
		Action:    actionGoPayPayment,
		Params: map[string]string{
			"otp_channel":        otpChannel,
			"add_balance_method": addBalanceMethod,
			"user_id":            userID,
		},
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	var probe ProbePlusTrialActivityOutput
	setWorkflowProgress(ctx, progress, stepProbePlusTrial)
	if err := workflow.ExecuteActivity(atomicCtx, probePlusTrialActivityName, ProbePlusTrialActivityInput{
		JobId:     input.GetJobId(),
		AccountId: account.GetAccountId(),
	}).Get(ctx, &probe); err != nil {
		combined["probe_plus_trial"] = protoDataMap(probe.GetData())
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbePlusTrial, statusFailedRetryable, false, true, err, combined), nil
	}
	combined["probe_plus_trial"] = protoDataMap(probe.GetData())
	if !probe.GetChecked() {
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbePlusTrial, statusFailedRetryable, false, true, fmt.Errorf("plus trial eligibility is unknown"), combined), nil
	}
	if !probe.GetPlusTrialEligible() {
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbePlusTrial, statusFailedFinal, false, false, fmt.Errorf("account is not plus trial eligible"), combined), nil
	}

	var otpOpts goPayAppOTPOptions
	var signup GoPayAppStepOutput
	signupAttempts := []any{}

	for attempt := 1; attempt <= goPayAppSignupMaxPhoneAttempts; attempt++ {
		attemptData := map[string]any{"attempt": attempt}

		var phone GoPayAppAcquireSignupPhoneOutput
		setWorkflowProgress(ctx, progress, stepGoPayAppSignupPhone)
		if err := workflow.ExecuteActivity(gopayCtx, goPayAppAcquireSignupPhoneActivityName, GoPayAppAcquireSignupPhoneInput{
			JobId:        input.GetJobId(),
			FailureCount: int32(attempt - 1),
		}).Get(ctx, &phone); err != nil {
			attemptData["signup_phone"] = protoDataMap(phone.GetData())
			attemptData["error_message"] = err.Error()
			signupAttempts = append(signupAttempts, attemptData)
			combined["signup_attempts"] = signupAttempts
			combined["signup_phone"] = protoDataMap(phone.GetData())
			result.ActivationId = phone.GetActivationId()
			result.Phone = phone.GetPhone()
			if attempt < goPayAppSignupMaxPhoneAttempts && isGoPaySignupPhoneRotatableError(err) {
				continue
			}
			return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppSignupPhone, statusFailedRetryable, false, true, err, combined), nil
		}
		attemptData["signup_phone"] = protoDataMap(phone.GetData())
		combined["signup_phone"] = protoDataMap(phone.GetData())
		result.ActivationId = phone.GetActivationId()
		result.Phone = phone.GetPhone()

		otpOpts = goPayAppOTPOptions{
			Phone:           phone.GetPhone(),
			OTPChannel:      otpChannel,
			SMSActivationID: phone.GetActivationId(),
			Source:          userID,
			ResetState:      true,
			StateJSON:       stateJSON,
		}

		setWorkflowProgress(ctx, progress, stepGoPayAppSignup)
		currentSignup, err := runGoPayAppSignup(ctx, gopayCtx, retryCtx, input.GetJobId(), otpOpts)
		signup = currentSignup
		stateJSON = signup.GetStateJson()
		attemptData["signup"] = protoDataMap(signup.GetData())
		if err == nil {
			signupAttempts = append(signupAttempts, attemptData)
			combined["signup_attempts"] = signupAttempts
			combined["signup"] = protoDataMap(signup.GetData())
			break
		}

		attemptData["error_message"] = err.Error()
		if isGoPaySignupOTPNotReceived(err) {
			var discarded GoPayAppSMSActivationOutput
			discardErr := workflow.ExecuteActivity(retryCtx, goPayAppDiscardSignupPhoneActivityName, GoPayAppSMSActivationInput{
				JobId:        input.GetJobId(),
				ActivationId: phone.GetActivationId(),
				FailureCount: int32(attempt),
				Reason:       err.Error(),
			}).Get(ctx, &discarded)
			attemptData["discard_signup_phone"] = protoDataMap(discarded.GetData())
			if discardErr != nil {
				attemptData["discard_error_message"] = discardErr.Error()
			}
			signupAttempts = append(signupAttempts, attemptData)
			combined["signup_attempts"] = signupAttempts
			combined["signup"] = protoDataMap(signup.GetData())
			if attempt < goPayAppSignupMaxPhoneAttempts {
				continue
			}
			return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppSignup, statusFailedRetryable, false, true, err, combined), nil
		}

		signupAttempts = append(signupAttempts, attemptData)
		combined["signup_attempts"] = signupAttempts
		combined["signup"] = protoDataMap(signup.GetData())
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppSignup, statusFailedRetryable, false, true, err, combined), nil
	}
	result.SignupComplete = signup.GetSignupComplete()

	if addBalance == nil {
		setWorkflowProgress(ctx, progress, stepGoPayAppAddBalance)
		selectedAddBalance, err := waitForGoPayAddBalanceSelection(ctx, retryCtx, input.GetJobId(), input.GetAddBalanceConfirmTimeoutSeconds())
		if err != nil {
			combined["add_balance"] = map[string]any{
				"status":  "awaiting_selection",
				"methods": goPayAddBalanceMethodOptions(),
			}
			return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppAddBalance, statusFailedRetryable, false, true, err, combined), nil
		}
		addBalance = selectedAddBalance
		addBalanceMethod = goPayAddBalanceMethod(addBalance)
		combined["add_balance_method"] = addBalanceMethod
		result.AddBalanceMethod = addBalanceMethod
	}

	var balance GoPayAppAddBalanceOutput
	setWorkflowProgress(ctx, progress, stepGoPayAppAddBalance)
	addBalanceCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	if err := workflow.ExecuteActivity(addBalanceCtx, goPayAppAddBalanceActivityName, GoPayAppAddBalanceInput{
		JobId:       input.GetJobId(),
		StateJson:   stateJSON,
		AddBalance:  addBalance,
		TargetPhone: result.GetPhone(),
	}).Get(ctx, &balance); err != nil {
		stateJSON = balance.GetStateJson()
		combined["add_balance"] = protoDataMap(balance.GetData())
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppAddBalance, statusFailedRetryable, false, true, err, combined), nil
	}
	stateJSON = balance.GetStateJson()
	combined["add_balance"] = protoDataMap(balance.GetData())
	result.AddBalanceMethod = balance.GetMethod()
	result.AddBalanceStatus = balance.GetStatus()
	if balance.GetMethod() == "manual_transfer" {
		setWorkflowProgress(ctx, progress, stepGoPayAppAddBalanceConfirm)
		if err := waitForManualAddBalance(ctx, input.GetAddBalanceConfirmTimeoutSeconds()); err != nil {
			combined["add_balance_confirmation"] = map[string]any{
				"confirmed": false,
				"method":    "manual_transfer",
			}
			return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppAddBalanceConfirm, statusFailedRetryable, false, true, err, combined), nil
		}
		combined["add_balance_confirmation"] = map[string]any{
			"confirmed": true,
			"method":    "manual_transfer",
		}
		result.AddBalanceStatus = "confirmed"
	}
	result.AddBalanceComplete = true

	var paymentPrepare GoPayPaymentPrepareOutput
	setWorkflowProgress(ctx, progress, stepGoPayPaymentPrepare)
	paymentPrepare, err := prepareGoPayPayment(ctx, paymentCtx, GoPayActivityInput{
		JobId:             input.GetJobId(),
		AccountId:         account.GetAccountId(),
		UseAccountToken:   false,
		Tokenization:      "true",
		CheckoutUrl:       probe.GetCheckoutUrl(),
		CheckoutSessionId: probe.GetCheckoutSessionId(),
		GopayPhone:        result.GetPhone(),
		StateJson:         stateJSON,
	})
	stateJSON = paymentPrepare.GetStateJson()
	combined["gopay_payment_prepare"] = protoDataMap(paymentPrepare.GetData())
	if err != nil {
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayPaymentPrepare, statusFailedRetryable, false, true, err, combined), nil
	}

	setWorkflowProgress(ctx, progress, stepGoPayAppCreatePin)
	otpOpts.StateJSON = stateJSON
	createPin, err := runGoPayAppCreatePin(ctx, gopayCtx, retryCtx, input.GetJobId(), otpOpts)
	stateJSON = createPin.GetStateJson()
	if err != nil {
		cancelGoPayPayment(ctx, retryCtx, paymentPrepare.GetFlowId())
		combined["create_pin"] = protoDataMap(createPin.GetData())
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppCreatePin, statusFailedRetryable, false, true, err, combined), nil
	}
	combined["create_pin"] = protoDataMap(createPin.GetData())
	result.SignupPinComplete = createPin.GetSignupPinComplete()
	result.AccountTokenReady = createPin.GetAccountTokenReady()
	if !createPin.GetAccountTokenReady() {
		cancelGoPayPayment(ctx, retryCtx, paymentPrepare.GetFlowId())
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppCreatePin, statusFailedRetryable, false, true, fmt.Errorf("gopay account token is not ready after create pin"), combined), nil
	}

	var payment GoPayActivityOutput
	setWorkflowProgress(ctx, progress, stepGoPayPayment)
	payment, err = runGoPayPayment(ctx, paymentCtx, retryCtx, GoPayActivityInput{
		JobId:             input.GetJobId(),
		AccountId:         account.GetAccountId(),
		UseAccountToken:   true,
		Tokenization:      "true",
		CheckoutUrl:       probe.GetCheckoutUrl(),
		CheckoutSessionId: probe.GetCheckoutSessionId(),
		PreparedFlowId:    paymentPrepare.GetFlowId(),
		GopayPhone:        result.GetPhone(),
		OtpChannel:        otpChannel,
		SmsActivationId:   otpOpts.SMSActivationID,
		UserId:            userID,
		StateJson:         stateJSON,
	})
	stateJSON = payment.GetStateJson()
	if err != nil {
		combined["gopay_payment"] = protoDataMap(payment.GetData())
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayPayment, statusFailedRetryable, false, true, err, combined), nil
	}
	combined["gopay_payment"] = protoDataMap(payment.GetData())

	var tier ProbeTierActivityOutput
	setWorkflowProgress(ctx, progress, stepProbeTier)
	if err := workflow.ExecuteActivity(tierCtx, probeTierActivityName, ProbeTierActivityInput{
		JobId:     input.GetJobId(),
		AccountId: account.GetAccountId(),
	}).Get(ctx, &tier); err != nil {
		combined["probe_tier"] = protoDataMap(tier.GetData())
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbeTier, statusFailedRecoverable, true, false, err, combined), nil
	}
	combined["probe_tier"] = protoDataMap(tier.GetData())

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobId:  input.GetJobId(),
		Result: protoData(combined),
	}).Get(ctx, nil)

	result.Success = true
	result.ChargeRef = payment.GetChargeRef()
	result.SnapToken = payment.GetSnapToken()
	setWorkflowProgressSucceeded(ctx, progress)
	return result, nil
}

func goPayWAPaymentWorkflow(ctx workflow.Context, input GoPayPaymentWorkflowInput) (GoPayPaymentWorkflowResult, error) {
	progress := newWorkflowProgress(ctx, "GoPayPaymentWorkflow", input.GetJobId())
	result := GoPayPaymentWorkflowResult{JobId: input.GetJobId()}
	defer func() {
		finishWorkflowProgressOnError(ctx, progress, result.GetErrorMessage())
	}()

	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	atomicCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(15*time.Minute))
	gopayCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(30*time.Minute))
	paymentCtx := workflow.WithActivityOptions(ctx, paymentActivityOptions())
	tierCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(2*time.Minute))

	userID := strings.TrimSpace(input.GetUserId())
	if userID == "" {
		userID = goPayLocalSource
	}
	result.UserId = userID
	addBalance := input.GetAddBalance()
	addBalanceMethod := goPayAddBalanceMethod(addBalance)
	stateJSON := "{}"
	combined := map[string]any{
		"otp_channel":        "wa",
		"user_id":            userID,
		"add_balance_method": addBalanceMethod,
	}

	var account AccountRef
	setWorkflowProgress(ctx, progress, "resolve_account")
	if err := workflow.ExecuteActivity(retryCtx, resolveAccountActivityName, ResolveAccountInput{
		AccountId:   input.GetAccountId(),
		SourceJobId: input.GetSourceJobId(),
	}).Get(ctx, &account); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}
	combined["account_id"] = account.GetAccountId()

	setWorkflowProgress(ctx, progress, "create_job")
	params := map[string]string{
		"otp_channel":        "wa",
		"user_id":            userID,
		"add_balance_method": addBalanceMethod,
	}
	if strings.TrimSpace(input.GetWaPhone()) != "" {
		params["wa_phone"] = strings.TrimSpace(input.GetWaPhone())
	}
	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobId:     input.GetJobId(),
		AccountId: account.GetAccountId(),
		Action:    actionGoPayPayment,
		Params:    params,
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	var probe ProbePlusTrialActivityOutput
	setWorkflowProgress(ctx, progress, stepProbePlusTrial)
	if err := workflow.ExecuteActivity(atomicCtx, probePlusTrialActivityName, ProbePlusTrialActivityInput{
		JobId:     input.GetJobId(),
		AccountId: account.GetAccountId(),
	}).Get(ctx, &probe); err != nil {
		combined["probe_plus_trial"] = protoDataMap(probe.GetData())
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbePlusTrial, statusFailedRetryable, false, true, err, combined), nil
	}
	combined["probe_plus_trial"] = protoDataMap(probe.GetData())
	if !probe.GetChecked() {
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbePlusTrial, statusFailedRetryable, false, true, fmt.Errorf("plus trial eligibility is unknown"), combined), nil
	}
	if !probe.GetPlusTrialEligible() {
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbePlusTrial, statusFailedFinal, false, false, fmt.Errorf("account is not plus trial eligible"), combined), nil
	}

	var waPhone GoPayResolveWAPhoneOutput
	setWorkflowProgress(ctx, progress, stepGoPayAppWAPhoneCheck)
	if err := workflow.ExecuteActivity(gopayCtx, goPayResolveWAPhoneActivityName, GoPayResolveWAPhoneInput{
		JobId:   input.GetJobId(),
		UserId:  userID,
		WaPhone: input.GetWaPhone(),
	}).Get(ctx, &waPhone); err != nil {
		combined["wa_phone"] = protoDataMap(waPhone.GetData())
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppWAPhoneCheck, statusFailedRetryable, false, true, err, combined), nil
	}
	userID = waPhone.GetUserId()
	result.UserId = userID
	result.WaPhone = waPhone.GetWaPhone()
	result.Phone = waPhone.GetWaPhone()
	combined["wa_phone"] = result.GetWaPhone()
	combined["wa_phone_resolution"] = protoDataMap(waPhone.GetData())

	var stored GoPayAppStateActivityOutput
	setWorkflowProgress(ctx, progress, "load_gopay_state")
	if err := workflow.ExecuteActivity(retryCtx, goPayAppLoadStateActivityName, GoPayAppStateActivityInput{
		JobId:  input.GetJobId(),
		UserId: userID,
		Reason: "wa_payment_start",
	}).Get(ctx, &stored); err != nil {
		combined["load_state"] = protoDataMap(stored.GetData())
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), "load_gopay_state", statusFailedRetryable, false, true, err, combined), nil
	}
	stateJSON = strings.TrimSpace(stored.GetStateJson())
	if stateJSON == "" {
		stateJSON = "{}"
	}
	combined["load_state"] = protoDataMap(stored.GetData())

	otpOpts := goPayAppOTPOptions{
		Phone:      result.GetWaPhone(),
		OTPChannel: "wa",
		Source:     userID,
		StateJSON:  stateJSON,
	}

	setWorkflowProgress(ctx, progress, stepGoPayAppLogin)
	token, err := runGoPayAppEnsureTokenAvailable(ctx, gopayCtx, retryCtx, input.GetJobId(), otpOpts)
	stateJSON = token.GetStateJson()
	combined["ensure_token_available"] = protoDataMap(token.GetData())
	if err != nil {
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppLogin, statusFailedRetryable, false, true, err, combined), nil
	}
	result.SignupComplete = token.GetSignupComplete()
	result.AccountTokenReady = token.GetAccountTokenReady()
	if err := workflow.ExecuteActivity(retryCtx, goPayAppSaveStateActivityName, GoPayAppStateActivityInput{
		JobId:     input.GetJobId(),
		UserId:    userID,
		StateJson: stateJSON,
		Reason:    "wa_payment_token_available",
	}).Get(ctx, nil); err != nil {
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppLogin, statusFailedRetryable, false, true, err, combined), nil
	}

	setWorkflowProgress(ctx, progress, stepGoPayAppCreatePin)
	otpOpts.StateJSON = stateJSON
	createPin, err := runGoPayAppEnsurePinSettled(ctx, gopayCtx, retryCtx, input.GetJobId(), otpOpts)
	stateJSON = createPin.GetStateJson()
	combined["ensure_pin_settled"] = protoDataMap(createPin.GetData())
	if err != nil {
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppCreatePin, statusFailedRetryable, false, true, err, combined), nil
	}
	result.SignupPinComplete = createPin.GetSignupPinComplete()
	result.AccountTokenReady = createPin.GetAccountTokenReady()
	if !createPin.GetAccountTokenReady() {
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppCreatePin, statusFailedRetryable, false, true, fmt.Errorf("gopay account token is not ready after create pin"), combined), nil
	}
	if err := workflow.ExecuteActivity(retryCtx, goPayAppSaveStateActivityName, GoPayAppStateActivityInput{
		JobId:     input.GetJobId(),
		UserId:    userID,
		StateJson: stateJSON,
		Reason:    "wa_payment_pin_settled",
	}).Get(ctx, nil); err != nil {
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppCreatePin, statusFailedRetryable, false, true, err, combined), nil
	}

	if addBalance == nil {
		setWorkflowProgress(ctx, progress, stepGoPayAppAddBalance)
		selectedAddBalance, err := waitForGoPayAddBalanceSelection(ctx, retryCtx, input.GetJobId(), input.GetAddBalanceConfirmTimeoutSeconds())
		if err != nil {
			combined["add_balance"] = map[string]any{
				"status":  "awaiting_selection",
				"methods": goPayAddBalanceMethodOptions(),
			}
			return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppAddBalance, statusFailedRetryable, false, true, err, combined), nil
		}
		addBalance = selectedAddBalance
		addBalanceMethod = goPayAddBalanceMethod(addBalance)
		combined["add_balance_method"] = addBalanceMethod
		result.AddBalanceMethod = addBalanceMethod
	}

	var balance GoPayAppAddBalanceOutput
	setWorkflowProgress(ctx, progress, stepGoPayAppAddBalance)
	addBalanceCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	if err := workflow.ExecuteActivity(addBalanceCtx, goPayAppAddBalanceActivityName, GoPayAppAddBalanceInput{
		JobId:       input.GetJobId(),
		StateJson:   stateJSON,
		AddBalance:  addBalance,
		TargetPhone: result.GetWaPhone(),
	}).Get(ctx, &balance); err != nil {
		stateJSON = balance.GetStateJson()
		combined["add_balance"] = protoDataMap(balance.GetData())
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppAddBalance, statusFailedRetryable, false, true, err, combined), nil
	}
	stateJSON = balance.GetStateJson()
	combined["add_balance"] = protoDataMap(balance.GetData())
	result.AddBalanceMethod = balance.GetMethod()
	result.AddBalanceStatus = balance.GetStatus()
	if err := workflow.ExecuteActivity(retryCtx, goPayAppSaveStateActivityName, GoPayAppStateActivityInput{
		JobId:     input.GetJobId(),
		UserId:    userID,
		StateJson: stateJSON,
		Reason:    "wa_payment_add_balance",
	}).Get(ctx, nil); err != nil {
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppAddBalance, statusFailedRetryable, false, true, err, combined), nil
	}
	if balance.GetMethod() == "manual_transfer" {
		setWorkflowProgress(ctx, progress, stepGoPayAppAddBalanceConfirm)
		if err := waitForManualAddBalance(ctx, input.GetAddBalanceConfirmTimeoutSeconds()); err != nil {
			combined["add_balance_confirmation"] = map[string]any{
				"confirmed": false,
				"method":    "manual_transfer",
			}
			return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppAddBalanceConfirm, statusFailedRetryable, false, true, err, combined), nil
		}
		combined["add_balance_confirmation"] = map[string]any{
			"confirmed": true,
			"method":    "manual_transfer",
		}
		result.AddBalanceStatus = "confirmed"
	}
	result.AddBalanceComplete = true

	var paymentPrepare GoPayPaymentPrepareOutput
	setWorkflowProgress(ctx, progress, stepGoPayPaymentPrepare)
	paymentPrepare, err = prepareGoPayPayment(ctx, paymentCtx, GoPayActivityInput{
		JobId:             input.GetJobId(),
		AccountId:         account.GetAccountId(),
		UseAccountToken:   false,
		Tokenization:      "true",
		CheckoutUrl:       probe.GetCheckoutUrl(),
		CheckoutSessionId: probe.GetCheckoutSessionId(),
		GopayPhone:        result.GetWaPhone(),
		UserId:            userID,
		StateJson:         stateJSON,
	})
	stateJSON = paymentPrepare.GetStateJson()
	combined["gopay_payment_prepare"] = protoDataMap(paymentPrepare.GetData())
	if err != nil {
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayPaymentPrepare, statusFailedRetryable, false, true, err, combined), nil
	}

	var payment GoPayActivityOutput
	setWorkflowProgress(ctx, progress, stepGoPayPayment)
	payment, err = runGoPayPayment(ctx, paymentCtx, retryCtx, GoPayActivityInput{
		JobId:             input.GetJobId(),
		AccountId:         account.GetAccountId(),
		UseAccountToken:   true,
		Tokenization:      "true",
		CheckoutUrl:       probe.GetCheckoutUrl(),
		CheckoutSessionId: probe.GetCheckoutSessionId(),
		PreparedFlowId:    paymentPrepare.GetFlowId(),
		GopayPhone:        result.GetWaPhone(),
		OtpChannel:        "wa",
		UserId:            userID,
		StateJson:         stateJSON,
	})
	stateJSON = payment.GetStateJson()
	combined["gopay_payment"] = protoDataMap(payment.GetData())
	if err != nil {
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayPayment, statusFailedRetryable, false, true, err, combined), nil
	}
	result.ChargeRef = payment.GetChargeRef()
	result.SnapToken = payment.GetSnapToken()
	combined["payment_completed"] = true
	combined["charge_ref"] = result.GetChargeRef()
	combined["snap_token"] = result.GetSnapToken()
	_ = workflow.ExecuteActivity(retryCtx, goPayAppSaveStateActivityName, GoPayAppStateActivityInput{
		JobId:     input.GetJobId(),
		UserId:    userID,
		StateJson: stateJSON,
		Reason:    "wa_payment_completed",
	}).Get(ctx, nil)
	combined["rebind_required"] = true

	var tier ProbeTierActivityOutput
	setWorkflowProgress(ctx, progress, stepProbeTier)
	if err := workflow.ExecuteActivity(tierCtx, probeTierActivityName, ProbeTierActivityInput{
		JobId:     input.GetJobId(),
		AccountId: account.GetAccountId(),
	}).Get(ctx, &tier); err != nil {
		combined["probe_tier"] = protoDataMap(tier.GetData())
		return failGoPayPaymentWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbeTier, statusFailedRecoverable, true, false, err, combined), nil
	}
	combined["probe_tier"] = protoDataMap(tier.GetData())

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobId:  input.GetJobId(),
		Result: protoData(combined),
	}).Get(ctx, nil)
	startGoPayPaymentRebindSideEffect(ctx, input.GetJobId(), account.GetAccountId(), userID, combined)
	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobId:  input.GetJobId(),
		Result: protoData(combined),
	}).Get(ctx, nil)

	result.Success = true
	setWorkflowProgressSucceeded(ctx, progress)
	return result, nil
}

func finishGoPayChangePhoneSMS(ctx workflow.Context, activityCtx workflow.Context, jobID, activationID, reason string) error {
	if strings.TrimSpace(activationID) == "" {
		return fmt.Errorf("change phone activation id is missing")
	}
	return workflow.ExecuteActivity(activityCtx, goPayAppSMSFinishActivityName, GoPayAppSMSActivationInput{
		JobId:        jobID,
		ActivationId: activationID,
		Reason:       reason,
	}).Get(ctx, nil)
}

func isGoPaySignupPhoneRotatableError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "signup phone already registered")
}

func startGoPayPaymentRebindSideEffect(ctx workflow.Context, sourceJobID string, accountID string, userID string, combined map[string]any) {
	sourceJobID = strings.TrimSpace(sourceJobID)
	if sourceJobID == "" {
		return
	}
	rebindJobID := sourceJobID + "-rebind"
	workflowID := "gopay-payment-rebind-" + rebindJobID
	data := map[string]any{
		"job_id":        rebindJobID,
		"workflow_id":   workflowID,
		"source_job_id": sourceJobID,
		"account_id":    accountID,
		"user_id":       userID,
	}
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:        workflowID,
		ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
	})
	future := workflow.ExecuteChildWorkflow(childCtx, GoPayPaymentRebindWorkflow, GoPayPaymentRebindWorkflowInput{
		JobId:       rebindJobID,
		SourceJobId: sourceJobID,
		AccountId:   accountID,
		UserId:      userID,
	})
	err := future.GetChildWorkflowExecution().Get(ctx, nil)
	if err != nil {
		var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(err, &alreadyStarted) {
			data["started"] = true
			data["already_started"] = true
		} else {
			data["started"] = false
			data["error_message"] = err.Error()
			workflow.GetLogger(ctx).Warn("failed to start gopay payment rebind side effect", "source_job_id", sourceJobID, "error", err)
		}
	} else {
		data["started"] = true
	}
	combined["rebind"] = data
	combined["rebind_job_id"] = rebindJobID
	combined["rebind_started"] = data["started"]
}
