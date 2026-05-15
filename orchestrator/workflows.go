package main

import (
	"fmt"
	pb "orchestrator/pb"
	"strconv"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	taskQueueDefault = "nb-register-orchestrator"

	createJobActivityName             = "CreateJobActivity"
	ensureAccountActivityName         = "EnsureAccountActivity"
	resolveAccountActivityName        = "ResolveAccountFromJobActivity"
	registerAccountActivityName       = "RegisterAccountAtomicActivity"
	ensureLogonActivityName           = "EnsureLogonActivity"
	goPayPaymentActivityName          = "GoPayPaymentAtomicActivity"
	cycleAndPayActivityName           = "CycleAndPayActivity"
	goPayCycleLoginActivityName       = "GoPayCycleLoginActivity"
	goPayCycleChangePhoneActivityName = "GoPayCycleChangePhoneActivity"
	goPayCycleDeactivateActivityName  = "GoPayCycleDeactivateActivity"
	goPayCycleSignupActivityName      = "GoPayCycleSignupActivity"
	goPayCycleCreatePinActivityName   = "GoPayCycleCreatePinActivity"
	probePlusTrialActivityName        = "ProbePlusTrialAtomicActivity"
	probeTierActivityName             = "ProbeTierAtomicActivity"
	loginSessionActivityName          = "LoginSessionAtomicActivity"
	registerMailboxActivityName       = "RegisterMailboxAtomicActivity"
	mailboxOAuthActivityName          = "MailboxOAuthAtomicActivity"
	persistRegisteredActivityName     = "PersistRegisteredActivity"
	persistActivatedActivityName      = "PersistActivatedActivity"
	markJobFailedActivityName         = "MarkJobFailedActivity"
	markJobSucceededActivityName      = "MarkJobSucceededActivity"
)

func RegisterAccountWorkflow(ctx workflow.Context, input RegisterAccountWorkflowInput) (RegisterAccountWorkflowResult, error) {
	result := RegisterAccountWorkflowResult{JobID: input.JobID}
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	atomicCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(15*time.Minute))

	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobID:     input.JobID,
		AccountID: input.Account.AccountID,
		Action:    actionRegister,
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	var account AccountRef
	if err := workflow.ExecuteActivity(retryCtx, ensureAccountActivityName, EnsureAccountInput{Account: input.Account}).Get(ctx, &account); err != nil {
		return failRegisterWorkflow(ctx, retryCtx, result, input.JobID, "", statusFailedRecoverable, true, false, err, nil), nil
	}

	var register RegisterActivityOutput
	if err := workflow.ExecuteActivity(atomicCtx, registerAccountActivityName, RegisterActivityInput{
		JobID:     input.JobID,
		AccountID: account.AccountID,
	}).Get(ctx, &register); err != nil {
		status, recoverable, retryable := registerFailurePolicy(err)
		return failRegisterWorkflow(ctx, retryCtx, result, input.JobID, stepRegisterAccount, status, recoverable, retryable, err, register.Data), nil
	}

	if err := workflow.ExecuteActivity(retryCtx, persistRegisteredActivityName, PersistRegisteredInput{
		AccountID:         account.AccountID,
		SessionToken:      register.SessionToken,
		AccessToken:       register.AccessToken,
		PlusTrialEligible: register.PlusTrialEligible,
		PlusTrialChecked:  register.PlusTrialChecked,
	}).Get(ctx, nil); err != nil {
		return failRegisterWorkflow(ctx, retryCtx, result, input.JobID, "", statusFailedRecoverable, true, false, err, register.Data), nil
	}

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobID:  input.JobID,
		Result: register.Data,
	}).Get(ctx, nil)
	startRegisteredAccountProbeSideEffects(ctx, input.JobID, account.AccountID)

	result.SessionToken = register.SessionToken
	result.AccessToken = register.AccessToken
	result.PlusTrialEligible = register.PlusTrialEligible
	result.CheckoutURL = register.CheckoutURL
	return result, nil
}

func ActivateAccountWorkflow(ctx workflow.Context, input ActivateAccountWorkflowInput) (ActivateAccountWorkflowResult, error) {
	result := ActivateAccountWorkflowResult{JobID: input.JobID}
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	atomicCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(15*time.Minute))
	paymentCtx := workflow.WithActivityOptions(ctx, paymentActivityOptions())
	ensureLogonCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(30*time.Minute))
	action := input.Action
	if action == "" {
		action = actionActivate
	}

	var account AccountRef
	if err := workflow.ExecuteActivity(retryCtx, resolveAccountActivityName, ResolveAccountInput{
		AccountID:   input.AccountID,
		SourceJobID: input.SourceJobID,
	}).Get(ctx, &account); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobID:     input.JobID,
		AccountID: account.AccountID,
		Action:    action,
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	var probe ProbePlusTrialActivityOutput
	if err := workflow.ExecuteActivity(atomicCtx, probePlusTrialActivityName, ProbePlusTrialActivityInput{
		JobID:     input.JobID,
		AccountID: account.AccountID,
	}).Get(ctx, &probe); err != nil {
		return failActivateWorkflow(ctx, retryCtx, result, input.JobID, stepProbePlusTrial, statusFailedRetryable, false, true, err, map[string]any{"probe_plus_trial": probe.Data}), nil
	}
	if !probe.Checked {
		return failActivateWorkflow(ctx, retryCtx, result, input.JobID, stepProbePlusTrial, statusFailedRetryable, false, true, fmt.Errorf("plus trial eligibility is unknown"), map[string]any{"probe_plus_trial": probe.Data}), nil
	}
	if !probe.PlusTrialEligible && !probe.PlusActive {
		return failActivateWorkflow(ctx, retryCtx, result, input.JobID, stepProbePlusTrial, statusFailedFinal, false, false, fmt.Errorf("account is not plus trial eligible"), map[string]any{"probe_plus_trial": probe.Data}), nil
	}

	var ensureLogon pb.EnsureLogonResponse
	if err := workflow.ExecuteActivity(ensureLogonCtx, ensureLogonActivityName, &pb.EnsureLogonRequest{
		JobId:     input.JobID,
		AccountId: account.AccountID,
	}).Get(ctx, &ensureLogon); err != nil {
		return failActivateWorkflow(ctx, retryCtx, result, input.JobID, stepEnsureLogon, statusFailedRetryable, false, true, err, activationPaymentData(ensureLogonData(&ensureLogon), probe.Data)), nil
	}

	var payment GoPayActivityOutput
	if err := workflow.ExecuteActivity(paymentCtx, goPayPaymentActivityName, GoPayActivityInput{
		JobID:         input.JobID,
		AccountID:     account.AccountID,
		UseCycleToken: ensureLogon.GetCycleTokenReady(),
	}).Get(ctx, &payment); err != nil {
		return failActivateWorkflow(ctx, retryCtx, result, input.JobID, stepGoPayPayment, statusFailedRetryable, false, true, err, activationPaymentData(ensureLogonData(&ensureLogon), map[string]any{"probe_plus_trial": probe.Data, "gopay_payment": payment.Data})), nil
	}

	if err := workflow.ExecuteActivity(retryCtx, persistActivatedActivityName, PersistActivatedInput{
		AccountID:         account.AccountID,
		ChargeRef:         payment.ChargeRef,
		PlusTrialEligible: payment.PlusTrialEligible,
		PlusTrialChecked:  payment.PlusTrialChecked,
		PlusActive:        payment.PlusActive,
	}).Get(ctx, nil); err != nil {
		return failActivateWorkflow(ctx, retryCtx, result, input.JobID, "", statusFailedRecoverable, true, false, err, payment.Data), nil
	}

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobID:  input.JobID,
		Result: activationPaymentData(ensureLogonData(&ensureLogon), map[string]any{"probe_plus_trial": probe.Data, "gopay_payment": payment.Data}),
	}).Get(ctx, nil)

	result.Success = true
	result.ChargeRef = payment.ChargeRef
	result.SnapToken = payment.SnapToken
	return result, nil
}

func AutoPayWorkflow(ctx workflow.Context, input AutoPayWorkflowInput) (AutoPayWorkflowResult, error) {
	result := AutoPayWorkflowResult{JobID: input.JobID}
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	atomicCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(15*time.Minute))
	paymentCtx := workflow.WithActivityOptions(ctx, paymentActivityOptions())

	var account AccountRef
	if err := workflow.ExecuteActivity(retryCtx, resolveAccountActivityName, ResolveAccountInput{
		AccountID:   input.AccountID,
		SourceJobID: input.SourceJobID,
	}).Get(ctx, &account); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobID:     input.JobID,
		AccountID: account.AccountID,
		Action:    actionAutopay,
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	var probe ProbePlusTrialActivityOutput
	if err := workflow.ExecuteActivity(atomicCtx, probePlusTrialActivityName, ProbePlusTrialActivityInput{
		JobID:     input.JobID,
		AccountID: account.AccountID,
	}).Get(ctx, &probe); err != nil {
		return failAutoPayWorkflow(ctx, retryCtx, result, input.JobID, stepProbePlusTrial, statusFailedRetryable, false, true, err, map[string]any{"probe_plus_trial": probe.Data}), nil
	}
	if !probe.Checked {
		return failAutoPayWorkflow(ctx, retryCtx, result, input.JobID, stepProbePlusTrial, statusFailedRetryable, false, true, fmt.Errorf("plus trial eligibility is unknown"), map[string]any{"probe_plus_trial": probe.Data}), nil
	}
	if !probe.PlusTrialEligible {
		return failAutoPayWorkflow(ctx, retryCtx, result, input.JobID, stepProbePlusTrial, statusFailedFinal, false, false, fmt.Errorf("account is not plus trial eligible"), map[string]any{"probe_plus_trial": probe.Data}), nil
	}

	var payment GoPayActivityOutput
	if err := workflow.ExecuteActivity(paymentCtx, goPayPaymentActivityName, GoPayActivityInput{
		JobID:        input.JobID,
		AccountID:    account.AccountID,
		Tokenization: "false",
	}).Get(ctx, &payment); err != nil {
		return failAutoPayWorkflow(ctx, retryCtx, result, input.JobID, stepGoPayPayment, statusFailedRetryable, false, true, err, map[string]any{"probe_plus_trial": probe.Data, "gopay_payment": payment.Data}), nil
	}

	combined := map[string]any{"probe_plus_trial": probe.Data, "gopay_payment": payment.Data}
	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobID:  input.JobID,
		Result: combined,
	}).Get(ctx, nil)

	result.Success = true
	result.ChargeRef = payment.ChargeRef
	result.SnapToken = payment.SnapToken
	return result, nil
}

func GoPayCycleWorkflow(ctx workflow.Context, input GoPayCycleWorkflowInput) (GoPayCycleWorkflowResult, error) {
	result := GoPayCycleWorkflowResult{JobID: input.JobID}
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	cycleCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(30*time.Minute))

	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobID:  input.JobID,
		Action: actionGoPayCycle,
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	combined := map[string]any{}
	var login GoPayCycleStepOutput
	if err := workflow.ExecuteActivity(cycleCtx, goPayCycleLoginActivityName, GoPayCycleStepInput{
		JobID: input.JobID,
	}).Get(ctx, &login); err != nil {
		combined["login"] = login.Data
		return failGoPayCycleWorkflow(ctx, retryCtx, result, input.JobID, stepGoPayCycleLogin, statusFailedRetryable, false, true, err, combined), nil
	}
	combined["login"] = login.Data

	var changePhone GoPayCycleStepOutput
	if err := workflow.ExecuteActivity(cycleCtx, goPayCycleChangePhoneActivityName, GoPayCycleStepInput{
		JobID: input.JobID,
	}).Get(ctx, &changePhone); err != nil {
		combined["change_phone"] = changePhone.Data
		return failGoPayCycleWorkflow(ctx, retryCtx, result, input.JobID, stepGoPayCycleChangePhone, statusFailedRetryable, false, true, err, combined), nil
	}
	combined["change_phone"] = changePhone.Data
	result.ActivationID = changePhone.ActivationID
	result.ChangePhoneComplete = changePhone.ChangePhoneComplete

	// Later stages intentionally disabled for now. Re-enable these in order
	// when the change-phone-only workflow is stable:
	//   1. gopay_cycle_deactivate
	//   2. gopay_cycle_signup
	//   3. gopay_cycle_create_pin
	result.CycleTokenReady = false

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobID:  input.JobID,
		Result: combined,
	}).Get(ctx, nil)

	result.Success = true
	return result, nil
}

func ProbeAccountWorkflow(ctx workflow.Context, input ProbeAccountWorkflowInput) (ProbeAccountWorkflowResult, error) {
	result := ProbeAccountWorkflowResult{JobID: input.JobID}
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	plusTrialCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(5*time.Minute))
	tierCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(2*time.Minute))

	var account AccountRef
	if err := workflow.ExecuteActivity(retryCtx, resolveAccountActivityName, ResolveAccountInput{
		AccountID: input.AccountID,
	}).Get(ctx, &account); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobID:     input.JobID,
		AccountID: account.AccountID,
		Action:    actionProbeAccount,
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	combined := map[string]any{
		"account_id":               account.AccountID,
		"plus_trial_already_known": account.PlusTrialKnown,
	}
	var plusTrial ProbePlusTrialActivityOutput
	if err := workflow.ExecuteActivity(plusTrialCtx, probePlusTrialActivityName, ProbePlusTrialActivityInput{
		JobID:     input.JobID,
		AccountID: account.AccountID,
	}).Get(ctx, &plusTrial); err != nil {
		combined["probe_plus_trial"] = plusTrial.Data
		return failProbeAccountWorkflow(ctx, retryCtx, result, input.JobID, stepProbePlusTrial, statusFailedRetryable, false, true, err, combined), nil
	}
	result.PlusTrialChecked = plusTrial.Checked
	result.PlusTrialEligible = plusTrial.PlusTrialEligible
	result.PlusActive = plusTrial.PlusActive
	result.Amount = plusTrial.Amount
	result.Currency = plusTrial.Currency
	result.Source = plusTrial.Source
	result.PlanType = plusTrial.PlanType
	result.CheckoutURL = plusTrial.CheckoutURL
	combined["probe_plus_trial"] = plusTrial.Data

	var tier ProbeTierActivityOutput
	if err := workflow.ExecuteActivity(tierCtx, probeTierActivityName, ProbeTierActivityInput{
		JobID:     input.JobID,
		AccountID: account.AccountID,
	}).Get(ctx, &tier); err != nil {
		combined["probe_tier"] = tier.Data
		return failProbeAccountWorkflow(ctx, retryCtx, result, input.JobID, stepProbeTier, statusFailedRetryable, false, true, err, combined), nil
	}
	combined["probe_tier"] = tier.Data

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobID:  input.JobID,
		Result: combined,
	}).Get(ctx, nil)

	result.Success = tier.Success && plusTrial.Success
	result.TierChecked = tier.Checked
	result.Tier = tier.Tier
	if tier.PlusActive {
		result.PlusActive = true
	}
	if tier.Source != "" {
		result.Source = tier.Source
	}
	result.ErrorMessage = tier.ErrorMessage
	return result, nil
}

func LoginSessionWorkflow(ctx workflow.Context, input LoginSessionWorkflowInput) (LoginSessionWorkflowResult, error) {
	result := LoginSessionWorkflowResult{JobID: input.JobID}
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	atomicCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(15*time.Minute))

	var account AccountRef
	if err := workflow.ExecuteActivity(retryCtx, resolveAccountActivityName, ResolveAccountInput{
		AccountID: input.AccountID,
	}).Get(ctx, &account); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobID:     input.JobID,
		AccountID: account.AccountID,
		Action:    actionLoginSession,
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	var login LoginSessionActivityOutput
	if err := workflow.ExecuteActivity(atomicCtx, loginSessionActivityName, LoginSessionActivityInput{
		JobID:     input.JobID,
		AccountID: account.AccountID,
	}).Get(ctx, &login); err != nil {
		return failLoginSessionWorkflow(ctx, retryCtx, result, input.JobID, stepLoginSession, statusFailedRetryable, false, true, err, login.Data), nil
	}

	if err := workflow.ExecuteActivity(retryCtx, persistRegisteredActivityName, PersistRegisteredInput{
		AccountID:    account.AccountID,
		SessionToken: login.SessionToken,
		AccessToken:  login.AccessToken,
	}).Get(ctx, nil); err != nil {
		return failLoginSessionWorkflow(ctx, retryCtx, result, input.JobID, "", statusFailedRecoverable, true, false, err, login.Data), nil
	}

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobID:  input.JobID,
		Result: login.Data,
	}).Get(ctx, nil)

	result.SessionToken = login.SessionToken
	result.AccessToken = login.AccessToken
	return result, nil
}

func RegisterAndActivateWorkflow(ctx workflow.Context, input RegisterAndActivateWorkflowInput) (RegisterAndActivateWorkflowResult, error) {
	result := RegisterAndActivateWorkflowResult{JobID: input.JobID}
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	atomicCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(15*time.Minute))
	paymentCtx := workflow.WithActivityOptions(ctx, paymentActivityOptions())
	ensureLogonCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(30*time.Minute))

	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobID:     input.JobID,
		AccountID: input.Account.AccountID,
		Action:    actionRegisterAndActivate,
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	var account AccountRef
	if err := workflow.ExecuteActivity(retryCtx, ensureAccountActivityName, EnsureAccountInput{Account: input.Account}).Get(ctx, &account); err != nil {
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.JobID, "", statusFailedRecoverable, true, false, err, nil), nil
	}

	var register RegisterActivityOutput
	if err := workflow.ExecuteActivity(atomicCtx, registerAccountActivityName, RegisterActivityInput{
		JobID:     input.JobID,
		AccountID: account.AccountID,
	}).Get(ctx, &register); err != nil {
		status, recoverable, retryable := registerFailurePolicy(err)
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.JobID, stepRegisterAccount, status, recoverable, retryable, err, register.Data), nil
	}

	if err := workflow.ExecuteActivity(retryCtx, persistRegisteredActivityName, PersistRegisteredInput{
		AccountID:         account.AccountID,
		SessionToken:      register.SessionToken,
		AccessToken:       register.AccessToken,
		PlusTrialEligible: register.PlusTrialEligible,
		PlusTrialChecked:  register.PlusTrialChecked,
	}).Get(ctx, nil); err != nil {
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.JobID, "", statusFailedRecoverable, true, false, err, register.Data), nil
	}

	var probe ProbePlusTrialActivityOutput
	if err := workflow.ExecuteActivity(atomicCtx, probePlusTrialActivityName, ProbePlusTrialActivityInput{
		JobID:     input.JobID,
		AccountID: account.AccountID,
	}).Get(ctx, &probe); err != nil {
		combined := map[string]any{"register_account": register.Data, "probe_plus_trial": probe.Data}
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.JobID, stepProbePlusTrial, statusFailedRetryable, false, true, err, combined), nil
	}
	if !probe.Checked {
		combined := map[string]any{"register_account": register.Data, "probe_plus_trial": probe.Data}
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.JobID, stepProbePlusTrial, statusFailedRetryable, false, true, fmt.Errorf("plus trial eligibility is unknown"), combined), nil
	}
	if !probe.PlusTrialEligible && !probe.PlusActive {
		combined := map[string]any{"register_account": register.Data, "probe_plus_trial": probe.Data}
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.JobID, stepProbePlusTrial, statusFailedFinal, false, false, fmt.Errorf("account is not plus trial eligible"), combined), nil
	}

	var ensureLogon pb.EnsureLogonResponse
	if err := workflow.ExecuteActivity(ensureLogonCtx, ensureLogonActivityName, &pb.EnsureLogonRequest{
		JobId:     input.JobID,
		AccountId: account.AccountID,
	}).Get(ctx, &ensureLogon); err != nil {
		combined := map[string]any{"register_account": register.Data, "probe_plus_trial": probe.Data}
		if logonData := ensureLogonData(&ensureLogon); logonData != nil {
			combined["ensure_logon"] = logonData
		}
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.JobID, stepEnsureLogon, statusFailedRetryable, false, true, err, combined), nil
	}

	var payment GoPayActivityOutput
	if err := workflow.ExecuteActivity(paymentCtx, goPayPaymentActivityName, GoPayActivityInput{
		JobID:         input.JobID,
		AccountID:     account.AccountID,
		SessionToken:  register.SessionToken,
		AccessToken:   register.AccessToken,
		UseCycleToken: ensureLogon.GetCycleTokenReady(),
	}).Get(ctx, &payment); err != nil {
		combined := map[string]any{"register_account": register.Data, "probe_plus_trial": probe.Data, "gopay_payment": payment.Data}
		if logonData := ensureLogonData(&ensureLogon); logonData != nil {
			combined["ensure_logon"] = logonData
		}
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.JobID, stepGoPayPayment, statusFailedRetryable, false, true, err, combined), nil
	}

	combined := map[string]any{"register_account": register.Data, "probe_plus_trial": probe.Data, "gopay_payment": payment.Data}
	if logonData := ensureLogonData(&ensureLogon); logonData != nil {
		combined["ensure_logon"] = logonData
	}
	if err := workflow.ExecuteActivity(retryCtx, persistActivatedActivityName, PersistActivatedInput{
		AccountID:         account.AccountID,
		SessionToken:      register.SessionToken,
		AccessToken:       register.AccessToken,
		ChargeRef:         payment.ChargeRef,
		PlusTrialEligible: payment.PlusTrialEligible,
		PlusTrialChecked:  payment.PlusTrialChecked,
		PlusActive:        payment.PlusActive,
	}).Get(ctx, nil); err != nil {
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.JobID, "", statusFailedRecoverable, true, false, err, combined), nil
	}

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobID:  input.JobID,
		Result: combined,
	}).Get(ctx, nil)
	startRegisteredAccountProbeSideEffects(ctx, input.JobID, account.AccountID)

	result.SessionToken = register.SessionToken
	result.AccessToken = register.AccessToken
	result.PlusTrialEligible = payment.PlusTrialEligible || probe.PlusTrialEligible || register.PlusTrialEligible
	result.CheckoutURL = register.CheckoutURL
	result.ActivationSuccess = true
	result.ChargeRef = payment.ChargeRef
	result.SnapToken = payment.SnapToken
	return result, nil
}

func startRegisteredAccountProbeSideEffects(ctx workflow.Context, sourceJobID string, accountID string) {
	if accountID == "" {
		return
	}
	logger := workflow.GetLogger(ctx)
	startProbeAccountSideEffect(ctx, logger, sourceJobID, accountID)
}

func startProbeAccountSideEffect(ctx workflow.Context, logger log.Logger, sourceJobID string, accountID string) {
	jobID := sourceJobID + "-probe"
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:        "probe-" + jobID,
		ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
	})
	future := workflow.ExecuteChildWorkflow(childCtx, ProbeAccountWorkflow, ProbeAccountWorkflowInput{
		JobID:     jobID,
		AccountID: accountID,
	})
	if err := future.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
		logger.Warn("failed to start account probe side effect", "account_id", accountID, "error", err)
	}
}

func RegisterMailboxWorkflow(ctx workflow.Context, input RegisterMailboxWorkflowInput) (RegisterMailboxWorkflowResult, error) {
	result := RegisterMailboxWorkflowResult{JobID: input.JobID}
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	atomicCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(30*time.Minute))

	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobID:  input.JobID,
		Action: actionRegisterMailbox,
		Params: map[string]string{
			"import_only": boolString(input.ImportOnly),
		},
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	var registration MailboxRegistrationActivityOutput
	if err := workflow.ExecuteActivity(atomicCtx, registerMailboxActivityName, MailboxRegistrationActivityInput{
		JobID:      input.JobID,
		Enabled:    !input.ImportOnly,
		ImportOnly: input.ImportOnly,
	}).Get(ctx, &registration); err != nil {
		return failRegisterMailboxWorkflow(ctx, retryCtx, result, input.JobID, stepRegisterMailbox, statusFailedRetryable, false, true, err, registration.Data), nil
	}
	if !registration.Success {
		err := temporal.NewApplicationError(registration.ErrorMessage, "MailboxRegistrationFailed")
		return failRegisterMailboxWorkflow(ctx, retryCtx, result, input.JobID, stepRegisterMailbox, statusFailedRetryable, false, true, err, registration.Data), nil
	}

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobID:  input.JobID,
		Result: registration.Data,
	}).Get(ctx, nil)
	if input.AutoOAuth {
		startMailboxOAuthSideEffects(ctx, input.JobID, registration.Mailboxes)
	}

	result.Success = registration.Success
	result.ExitCode = registration.ExitCode
	result.Mailboxes = registration.Mailboxes
	return result, nil
}

func startMailboxOAuthSideEffects(ctx workflow.Context, sourceJobID string, mailboxes []RegisteredMailboxResult) {
	logger := workflow.GetLogger(ctx)
	for index, mailbox := range mailboxes {
		if mailbox.EmailAddress == "" {
			continue
		}
		jobID := sourceJobID + "-oauth-" + strconv.Itoa(index+1)
		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID:        "mailbox-oauth-" + jobID,
			ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
		})
		future := workflow.ExecuteChildWorkflow(childCtx, MailboxOAuthWorkflow, MailboxOAuthWorkflowInput{
			JobID:        jobID,
			EmailAddress: mailbox.EmailAddress,
			OnlyMissing:  true,
			Limit:        1,
		})
		if err := future.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
			logger.Warn("failed to start mailbox OAuth side effect", "email", mailbox.EmailAddress, "error", err)
		}
	}
}

func MailboxOAuthWorkflow(ctx workflow.Context, input MailboxOAuthWorkflowInput) (MailboxOAuthWorkflowResult, error) {
	result := MailboxOAuthWorkflowResult{JobID: input.JobID}
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	atomicCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(30*time.Minute))

	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobID:  input.JobID,
		Action: actionMailboxOAuth,
		Params: map[string]string{
			"email_address": input.EmailAddress,
			"only_missing":  boolString(input.OnlyMissing),
			"limit":         int32String(input.Limit),
		},
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	var oauth MailboxOAuthActivityOutput
	if err := workflow.ExecuteActivity(atomicCtx, mailboxOAuthActivityName, MailboxOAuthActivityInput{
		JobID:        input.JobID,
		EmailAddress: input.EmailAddress,
		OnlyMissing:  input.OnlyMissing,
		Limit:        input.Limit,
	}).Get(ctx, &oauth); err != nil {
		return failMailboxOAuthWorkflow(ctx, retryCtx, result, input.JobID, stepMailboxOAuth, statusFailedRetryable, false, true, err, oauth.Data), nil
	}
	if !oauth.Success {
		msg := oauth.ErrorMessage
		if msg == "" {
			msg = "mailbox OAuth failed"
		}
		err := temporal.NewApplicationError(msg, "MailboxOAuthFailed")
		return failMailboxOAuthWorkflow(ctx, retryCtx, result, input.JobID, stepMailboxOAuth, statusFailedRetryable, false, true, err, oauth.Data), nil
	}

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobID:  input.JobID,
		Result: oauth.Data,
	}).Get(ctx, nil)

	result.Success = oauth.Success
	result.Processed = oauth.Processed
	result.Succeeded = oauth.Succeeded
	result.Failed = oauth.Failed
	return result, nil
}

func failRegisterWorkflow(ctx workflow.Context, activityCtx workflow.Context, result RegisterAccountWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) RegisterAccountWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}

func failActivateWorkflow(ctx workflow.Context, activityCtx workflow.Context, result ActivateAccountWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) ActivateAccountWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}

func failAutoPayWorkflow(ctx workflow.Context, activityCtx workflow.Context, result AutoPayWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) AutoPayWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}

func failGoPayCycleWorkflow(ctx workflow.Context, activityCtx workflow.Context, result GoPayCycleWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) GoPayCycleWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}

func failProbeAccountWorkflow(ctx workflow.Context, activityCtx workflow.Context, result ProbeAccountWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) ProbeAccountWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}

func failLoginSessionWorkflow(ctx workflow.Context, activityCtx workflow.Context, result LoginSessionWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) LoginSessionWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}

func failRegisterAndActivateWorkflow(ctx workflow.Context, activityCtx workflow.Context, result RegisterAndActivateWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) RegisterAndActivateWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}

func failRegisterMailboxWorkflow(ctx workflow.Context, activityCtx workflow.Context, result RegisterMailboxWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) RegisterMailboxWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}

func failMailboxOAuthWorkflow(ctx workflow.Context, activityCtx workflow.Context, result MailboxOAuthWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) MailboxOAuthWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
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

func markWorkflowFailure(ctx workflow.Context, activityCtx workflow.Context, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) {
	_ = workflow.ExecuteActivity(activityCtx, markJobFailedActivityName, JobFailureInput{
		JobID:        jobID,
		StepName:     stepName,
		Status:       status,
		Recoverable:  recoverable,
		Retryable:    retryable,
		ErrorMessage: err.Error(),
		Result:       data,
	}).Get(ctx, nil)
}

func atomicActivityOptions(timeout time.Duration) workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: timeout,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	}
}

func paymentActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 15 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	}
}

func retryableActivityOptions(timeout time.Duration, attempts int32) workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: timeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    attempts,
		},
	}
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func int32String(value int32) string {
	if value == 0 {
		return ""
	}
	return strconv.FormatInt(int64(value), 10)
}
