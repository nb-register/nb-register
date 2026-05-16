package app

import (
	"fmt"
	pb "orchestrator/pb"
	"time"

	"go.temporal.io/sdk/workflow"
)

func ActivateAccountWorkflow(ctx workflow.Context, input ActivateAccountWorkflowInput) (ActivateAccountWorkflowResult, error) {
	progress := newWorkflowProgress(ctx, "ActivateAccountWorkflow", input.GetJobId())
	result := ActivateAccountWorkflowResult{JobId: input.GetJobId()}
	defer func() {
		finishWorkflowProgressOnError(ctx, progress, result.GetErrorMessage())
	}()
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	atomicCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(15*time.Minute))
	gopayCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(5*time.Minute))
	paymentCtx := workflow.WithActivityOptions(ctx, paymentActivityOptions())
	ensureLogonCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(30*time.Minute))
	action := input.Action
	if action == "" {
		action = actionActivate
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

	setWorkflowProgress(ctx, progress, "create_job")
	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobId:     input.GetJobId(),
		AccountId: account.GetAccountId(),
		Action:    action,
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
		return failActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbePlusTrial, statusFailedRetryable, false, true, err, map[string]any{"probe_plus_trial": protoDataMap(probe.GetData())}), nil
	}
	if !probe.GetChecked() {
		return failActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbePlusTrial, statusFailedRetryable, false, true, fmt.Errorf("plus trial eligibility is unknown"), map[string]any{"probe_plus_trial": protoDataMap(probe.GetData())}), nil
	}
	if !probe.GetPlusTrialEligible() && !probe.GetPlusActive() {
		return failActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbePlusTrial, statusFailedFinal, false, false, fmt.Errorf("account is not plus trial eligible"), map[string]any{"probe_plus_trial": protoDataMap(probe.GetData())}), nil
	}

	setWorkflowProgress(ctx, progress, stepGoPayAppLogin)
	logon, err := runGoPayAppAuth(ctx, gopayCtx, retryCtx, input.GetJobId())
	if err != nil {
		return failActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppLogin, statusFailedRetryable, false, true, err, map[string]any{"probe_plus_trial": protoDataMap(probe.GetData()), "gopay_login": protoDataMap(logon.GetData())}), nil
	}

	var ensureLogon pb.EnsureLogonResponse
	setWorkflowProgress(ctx, progress, stepEnsureLogon)
	if err := workflow.ExecuteActivity(ensureLogonCtx, ensureLogonActivityName, &pb.EnsureLogonRequest{
		JobId:     input.GetJobId(),
		AccountId: account.GetAccountId(),
	}).Get(ctx, &ensureLogon); err != nil {
		return failActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), stepEnsureLogon, statusFailedRetryable, false, true, err, map[string]any{"probe_plus_trial": protoDataMap(probe.GetData()), "gopay_login": protoDataMap(logon.GetData()), "ensure_logon": ensureLogonData(&ensureLogon)}), nil
	}

	var payment GoPayActivityOutput
	setWorkflowProgress(ctx, progress, stepGoPayPayment)
	payment, err = runGoPayPayment(ctx, paymentCtx, retryCtx, GoPayActivityInput{
		JobId:             input.GetJobId(),
		AccountId:         account.GetAccountId(),
		UseAccountToken:   ensureLogon.GetAccountTokenReady(),
		CheckoutUrl:       probe.GetCheckoutUrl(),
		CheckoutSessionId: probe.GetCheckoutSessionId(),
	})
	if err != nil {
		return failActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayPayment, statusFailedRetryable, false, true, err, activationPaymentData(ensureLogonData(&ensureLogon), map[string]any{"probe_plus_trial": protoDataMap(probe.GetData()), "gopay_payment": protoDataMap(payment.GetData())})), nil
	}

	if err := workflow.ExecuteActivity(retryCtx, persistActivatedActivityName, PersistActivatedInput{
		AccountId:         account.GetAccountId(),
		ChargeRef:         payment.GetChargeRef(),
		PlusTrialEligible: payment.GetPlusTrialEligible(),
		PlusTrialChecked:  payment.GetPlusTrialChecked(),
		PlusActive:        payment.GetPlusActive(),
	}).Get(ctx, nil); err != nil {
		return failActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), "", statusFailedRecoverable, true, false, err, protoDataMap(payment.GetData())), nil
	}

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobId:  input.GetJobId(),
		Result: protoData(activationPaymentData(ensureLogonData(&ensureLogon), map[string]any{"probe_plus_trial": protoDataMap(probe.GetData()), "gopay_login": protoDataMap(logon.GetData()), "gopay_payment": protoDataMap(payment.GetData())})),
	}).Get(ctx, nil)

	result.Success = true
	result.ChargeRef = payment.GetChargeRef()
	result.SnapToken = payment.GetSnapToken()
	setWorkflowProgressSucceeded(ctx, progress)
	return result, nil
}
func AutoPayWorkflow(ctx workflow.Context, input AutoPayWorkflowInput) (AutoPayWorkflowResult, error) {
	progress := newWorkflowProgress(ctx, "AutoPayWorkflow", input.GetJobId())
	result := AutoPayWorkflowResult{JobId: input.GetJobId()}
	defer func() {
		finishWorkflowProgressOnError(ctx, progress, result.GetErrorMessage())
	}()
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	atomicCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(15*time.Minute))
	gopayCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(5*time.Minute))
	paymentCtx := workflow.WithActivityOptions(ctx, paymentActivityOptions())
	ensureLogonCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(30*time.Minute))
	tierCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(2*time.Minute))

	var account AccountRef
	setWorkflowProgress(ctx, progress, "resolve_account")
	if err := workflow.ExecuteActivity(retryCtx, resolveAccountActivityName, ResolveAccountInput{
		AccountId:   input.GetAccountId(),
		SourceJobId: input.GetSourceJobId(),
	}).Get(ctx, &account); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	setWorkflowProgress(ctx, progress, "create_job")
	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobId:     input.GetJobId(),
		AccountId: account.GetAccountId(),
		Action:    actionAutopay,
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
		return failAutoPayWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbePlusTrial, statusFailedRetryable, false, true, err, map[string]any{"probe_plus_trial": protoDataMap(probe.GetData())}), nil
	}
	if !probe.GetChecked() {
		return failAutoPayWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbePlusTrial, statusFailedRetryable, false, true, fmt.Errorf("plus trial eligibility is unknown"), map[string]any{"probe_plus_trial": protoDataMap(probe.GetData())}), nil
	}
	if !probe.GetPlusTrialEligible() {
		return failAutoPayWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbePlusTrial, statusFailedFinal, false, false, fmt.Errorf("account is not plus trial eligible"), map[string]any{"probe_plus_trial": protoDataMap(probe.GetData())}), nil
	}

	setWorkflowProgress(ctx, progress, stepGoPayAppLogin)
	logon, err := runGoPayAppAuth(ctx, gopayCtx, retryCtx, input.GetJobId())
	if err != nil {
		return failAutoPayWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppLogin, statusFailedRetryable, false, true, err, map[string]any{"probe_plus_trial": protoDataMap(probe.GetData()), "gopay_login": protoDataMap(logon.GetData())}), nil
	}

	var ensureLogon pb.EnsureLogonResponse
	setWorkflowProgress(ctx, progress, stepEnsureLogon)
	if err := workflow.ExecuteActivity(ensureLogonCtx, ensureLogonActivityName, &pb.EnsureLogonRequest{
		JobId:     input.GetJobId(),
		AccountId: account.GetAccountId(),
	}).Get(ctx, &ensureLogon); err != nil {
		combined := map[string]any{"probe_plus_trial": protoDataMap(probe.GetData()), "gopay_login": protoDataMap(logon.GetData())}
		if logonData := ensureLogonData(&ensureLogon); logonData != nil {
			combined["ensure_logon"] = logonData
		}
		return failAutoPayWorkflow(ctx, retryCtx, result, input.GetJobId(), stepEnsureLogon, statusFailedRetryable, false, true, err, combined), nil
	}

	var payment GoPayActivityOutput
	setWorkflowProgress(ctx, progress, stepGoPayPayment)
	payment, err = runGoPayPayment(ctx, paymentCtx, retryCtx, GoPayActivityInput{
		JobId:             input.GetJobId(),
		AccountId:         account.GetAccountId(),
		UseAccountToken:   ensureLogon.GetAccountTokenReady(),
		Tokenization:      "true",
		CheckoutUrl:       probe.GetCheckoutUrl(),
		CheckoutSessionId: probe.GetCheckoutSessionId(),
	})
	if err != nil {
		combined := map[string]any{"probe_plus_trial": protoDataMap(probe.GetData()), "gopay_payment": protoDataMap(payment.GetData())}
		if logonData := ensureLogonData(&ensureLogon); logonData != nil {
			combined["ensure_logon"] = logonData
		}
		return failAutoPayWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayPayment, statusFailedRetryable, false, true, err, combined), nil
	}

	combined := map[string]any{"probe_plus_trial": protoDataMap(probe.GetData()), "gopay_login": protoDataMap(logon.GetData()), "gopay_payment": protoDataMap(payment.GetData())}
	if logonData := ensureLogonData(&ensureLogon); logonData != nil {
		combined["ensure_logon"] = logonData
	}

	var tier ProbeTierActivityOutput
	setWorkflowProgress(ctx, progress, stepProbeTier)
	if err := workflow.ExecuteActivity(tierCtx, probeTierActivityName, ProbeTierActivityInput{
		JobId:     input.GetJobId(),
		AccountId: account.GetAccountId(),
	}).Get(ctx, &tier); err != nil {
		combined["probe_tier"] = protoDataMap(tier.GetData())
		return failAutoPayWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbeTier, statusFailedRecoverable, true, false, err, combined), nil
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
func ProbeAccountWorkflow(ctx workflow.Context, input ProbeAccountWorkflowInput) (ProbeAccountWorkflowResult, error) {
	progress := newWorkflowProgress(ctx, "ProbeAccountWorkflow", input.GetJobId())
	result := ProbeAccountWorkflowResult{JobId: input.GetJobId()}
	defer func() {
		finishWorkflowProgressOnError(ctx, progress, result.GetErrorMessage())
	}()
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	plusTrialCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(5*time.Minute))
	tierCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(2*time.Minute))

	var account AccountRef
	setWorkflowProgress(ctx, progress, "resolve_account")
	if err := workflow.ExecuteActivity(retryCtx, resolveAccountActivityName, ResolveAccountInput{
		AccountId: input.GetAccountId(),
	}).Get(ctx, &account); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	setWorkflowProgress(ctx, progress, "create_job")
	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobId:     input.GetJobId(),
		AccountId: account.GetAccountId(),
		Action:    actionProbeAccount,
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	combined := map[string]any{
		"account_id":               account.GetAccountId(),
		"plus_trial_already_known": account.GetPlusTrialKnown(),
		"tier":                     account.GetTier(),
		"plus_active":              account.GetPlusActive(),
	}
	plusTrialSuccess := true
	plusTrialSkipped := shouldSkipPlusTrialProbe(account)
	var plusTrial ProbePlusTrialActivityOutput
	if plusTrialSkipped {
		combined["probe_plus_trial"] = skippedPlusTrialProbeData(account)
		result.PlusActive = account.GetPlusActive()
		result.PlanType = account.GetTier()
	} else {
		setWorkflowProgress(ctx, progress, stepProbePlusTrial)
		if err := workflow.ExecuteActivity(plusTrialCtx, probePlusTrialActivityName, ProbePlusTrialActivityInput{
			JobId:     input.GetJobId(),
			AccountId: account.GetAccountId(),
		}).Get(ctx, &plusTrial); err != nil {
			combined["probe_plus_trial"] = protoDataMap(plusTrial.GetData())
			return failProbeAccountWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbePlusTrial, statusFailedRetryable, false, true, err, combined), nil
		}
		plusTrialSuccess = plusTrial.GetSuccess()
		result.PlusTrialChecked = plusTrial.GetChecked()
		result.PlusTrialEligible = plusTrial.GetPlusTrialEligible()
		result.PlusActive = plusTrial.GetPlusActive()
		result.Amount = plusTrial.GetAmount()
		result.Currency = plusTrial.GetCurrency()
		result.Source = plusTrial.GetSource()
		result.PlanType = plusTrial.GetPlanType()
		result.CheckoutUrl = plusTrial.GetCheckoutUrl()
		combined["probe_plus_trial"] = protoDataMap(plusTrial.GetData())
	}

	var tier ProbeTierActivityOutput
	setWorkflowProgress(ctx, progress, stepProbeTier)
	if err := workflow.ExecuteActivity(tierCtx, probeTierActivityName, ProbeTierActivityInput{
		JobId:     input.GetJobId(),
		AccountId: account.GetAccountId(),
	}).Get(ctx, &tier); err != nil {
		combined["probe_tier"] = protoDataMap(tier.GetData())
		return failProbeAccountWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbeTier, statusFailedRetryable, false, true, err, combined), nil
	}
	combined["probe_tier"] = protoDataMap(tier.GetData())

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobId:  input.GetJobId(),
		Result: protoData(combined),
	}).Get(ctx, nil)

	result.Success = tier.GetSuccess() && plusTrialSuccess
	result.TierChecked = tier.GetChecked()
	result.Tier = tier.GetTier()
	if plusTrialSkipped && tier.GetChecked() {
		result.PlusActive = tier.GetPlusActive()
	} else if tier.GetPlusActive() {
		result.PlusActive = true
	}
	if tier.GetSource() != "" {
		result.Source = tier.GetSource()
	}
	result.ErrorMessage = tier.GetErrorMessage()
	if result.GetErrorMessage() == "" {
		setWorkflowProgressSucceeded(ctx, progress)
	}
	return result, nil
}
func shouldSkipPlusTrialProbe(account AccountRef) bool {
	return account.GetPlusActive() || normalizeTier(account.GetTier()) == "plus"
}
func skippedPlusTrialProbeData(account AccountRef) map[string]any {
	reason := "plus_active"
	if normalizeTier(account.GetTier()) == "plus" {
		reason = "tier_plus"
	}
	return map[string]any{
		"skipped":     true,
		"reason":      reason,
		"tier":        account.GetTier(),
		"plus_active": account.GetPlusActive(),
	}
}
func activationPaymentData(logonData map[string]any, paymentData map[string]any) map[string]any {
	if logonData == nil {
		return paymentData
	}
	return map[string]any{
		"ensure_logon":  logonData,
		"gopay_payment": paymentData,
	}
}
func runGoPayPayment(ctx workflow.Context, paymentCtx workflow.Context, cancelCtx workflow.Context, input GoPayActivityInput) (GoPayActivityOutput, error) {
	var start GoPayPaymentStartOutput
	if err := workflow.ExecuteActivity(paymentCtx, goPayPaymentStartActivityName, input).Get(ctx, &start); err != nil {
		return GoPayActivityOutput{Data: start.GetData()}, err
	}

	otp, err := waitForOTP(ctx, OTPWaitInput{
		JobId:            input.GetJobId(),
		StepName:         stepGoPayPayment,
		Target:           &pb.OTPWaitInput_Payment{Payment: &pb.OTPWaitPaymentTarget{Source: goPayLocalSource}},
		TimeoutSeconds:   start.GetOtpTimeoutSeconds(),
		IssuedAfterUnix:  start.GetIssuedAfterUnix(),
		OtpParam:         paymentOTPParam,
		SubmittedAtParam: paymentOTPSubmittedAtParam,
	})
	if err != nil {
		_ = workflow.ExecuteActivity(cancelCtx, goPayPaymentCancelActivityName, GoPayPaymentCancelInput{FlowId: start.GetFlowId()}).Get(ctx, nil)
		return GoPayActivityOutput{Data: start.GetData()}, err
	}

	var payment GoPayActivityOutput
	err = workflow.ExecuteActivity(paymentCtx, goPayPaymentCompleteActivityName, GoPayPaymentCompleteInput{
		JobId:              input.GetJobId(),
		AccountId:          input.GetAccountId(),
		FlowId:             start.GetFlowId(),
		OtpParam:           paymentOTPParam,
		SubmittedAtParam:   paymentOTPSubmittedAtParam,
		OtpIssuedAfterUnix: start.GetIssuedAfterUnix(),
		OtpSource:          otp.GetSource(),
		UseAccountToken:    start.GetUseAccountToken(),
		Data:               start.GetData(),
	}).Get(ctx, &payment)
	return payment, err
}
