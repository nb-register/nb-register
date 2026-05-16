package app

import (
	"fmt"
	pb "orchestrator/pb"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

func RegisterAccountWorkflow(ctx workflow.Context, input RegisterAccountWorkflowInput) (RegisterAccountWorkflowResult, error) {
	progress := newWorkflowProgress(ctx, "RegisterAccountWorkflow", input.GetJobId())
	result := RegisterAccountWorkflowResult{JobId: input.GetJobId()}
	defer func() {
		finishWorkflowProgressOnError(ctx, progress, result.GetErrorMessage())
	}()
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	browserCtx := workflow.WithActivityOptions(ctx, heartbeatingActivityOptions(5*time.Minute, 30*time.Second))

	setWorkflowProgress(ctx, progress, "create_job")
	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobId:     input.GetJobId(),
		AccountId: input.GetAccount().GetAccountId(),
		Action:    actionRegister,
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	var account AccountRef
	setWorkflowProgress(ctx, progress, "ensure_account")
	if err := workflow.ExecuteActivity(retryCtx, ensureAccountActivityName, EnsureAccountInput{Account: input.Account}).Get(ctx, &account); err != nil {
		return failRegisterWorkflow(ctx, retryCtx, result, input.GetJobId(), "", statusFailedRecoverable, true, false, err, nil), nil
	}

	var start BrowserAuthStartOutput
	setWorkflowProgress(ctx, progress, stepRegisterAccount)
	if err := workflow.ExecuteActivity(browserCtx, browserAuthStartActivityName, BrowserAuthStartInput{
		JobId:     input.GetJobId(),
		AccountId: account.GetAccountId(),
		Mode:      browserAuthModeRegister,
	}).Get(ctx, &start); err != nil {
		status, recoverable, retryable := registerFailurePolicy(err)
		return failRegisterWorkflow(ctx, retryCtx, result, input.GetJobId(), stepRegisterAccount, status, recoverable, retryable, err, protoDataMap(start.GetData())), nil
	}

	register := RegisterActivityOutput{}
	if start.GetResult() != nil {
		register = *start.GetResult()
	}
	if start.GetOtpRequired() {
		setWorkflowProgress(ctx, progress, stepRegisterAccount+"_otp_wait")
		otp, err := waitForOTP(ctx, OTPWaitInput{
			JobId:            input.GetJobId(),
			StepName:         stepRegisterAccount,
			Target:           &pb.OTPWaitInput_Email{Email: &pb.OTPWaitEmailTarget{Email: start.GetEmail()}},
			TimeoutSeconds:   start.GetOtpTimeoutSeconds(),
			IssuedAfterUnix:  start.GetOtpIssuedAfterUnix(),
			OtpParam:         registrationOTPParam,
			SubmittedAtParam: registrationOTPSubmittedAtParam,
		})
		if err != nil {
			_ = workflow.ExecuteActivity(retryCtx, browserAuthCancelActivityName, BrowserAuthCancelInput{FlowId: start.GetFlowId(), Mode: browserAuthModeRegister}).Get(ctx, nil)
			return failRegisterWorkflow(ctx, retryCtx, result, input.GetJobId(), stepRegisterAccount, statusFailedRetryable, false, true, err, protoDataMap(start.GetData())), nil
		}
		if err := workflow.ExecuteActivity(browserCtx, browserAuthCompleteActivityName, BrowserAuthCompleteInput{
			JobId:              input.GetJobId(),
			AccountId:          account.GetAccountId(),
			FlowId:             start.GetFlowId(),
			Mode:               browserAuthModeRegister,
			OtpParam:           registrationOTPParam,
			SubmittedAtParam:   registrationOTPSubmittedAtParam,
			OtpIssuedAfterUnix: start.GetOtpIssuedAfterUnix(),
			OtpSource:          otp.GetSource(),
		}).Get(ctx, &register); err != nil {
			status, recoverable, retryable := registerFailurePolicy(err)
			return failRegisterWorkflow(ctx, retryCtx, result, input.GetJobId(), stepRegisterAccount, status, recoverable, retryable, err, protoDataMap(register.GetData())), nil
		}
	}

	if err := workflow.ExecuteActivity(retryCtx, persistRegisteredActivityName, PersistRegisteredInput{
		AccountId:         account.GetAccountId(),
		SessionToken:      register.GetSessionToken(),
		AccessToken:       register.GetAccessToken(),
		PlusTrialEligible: register.GetPlusTrialEligible(),
		PlusTrialChecked:  register.GetPlusTrialChecked(),
	}).Get(ctx, nil); err != nil {
		return failRegisterWorkflow(ctx, retryCtx, result, input.GetJobId(), "", statusFailedRecoverable, true, false, err, protoDataMap(register.GetData())), nil
	}

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobId:  input.GetJobId(),
		Result: register.GetData(),
	}).Get(ctx, nil)
	startRegisteredAccountProbeSideEffects(ctx, input.GetJobId(), account.GetAccountId())
	setWorkflowProgressSucceeded(ctx, progress)

	result.SessionToken = register.GetSessionToken()
	result.AccessToken = register.GetAccessToken()
	result.PlusTrialEligible = register.GetPlusTrialEligible()
	result.CheckoutUrl = register.GetCheckoutUrl()
	return result, nil
}
func LoginSessionWorkflow(ctx workflow.Context, input LoginSessionWorkflowInput) (LoginSessionWorkflowResult, error) {
	progress := newWorkflowProgress(ctx, "LoginSessionWorkflow", input.GetJobId())
	result := LoginSessionWorkflowResult{JobId: input.GetJobId()}
	defer func() {
		finishWorkflowProgressOnError(ctx, progress, result.GetErrorMessage())
	}()
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	browserCtx := workflow.WithActivityOptions(ctx, heartbeatingActivityOptions(5*time.Minute, 30*time.Second))

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
		Action:    actionLoginSession,
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	var start BrowserAuthStartOutput
	setWorkflowProgress(ctx, progress, stepLoginSession)
	if err := workflow.ExecuteActivity(browserCtx, browserAuthStartActivityName, BrowserAuthStartInput{
		JobId:     input.GetJobId(),
		AccountId: account.GetAccountId(),
		Mode:      browserAuthModeLogin,
	}).Get(ctx, &start); err != nil {
		return failLoginSessionWorkflow(ctx, retryCtx, result, input.GetJobId(), stepLoginSession, statusFailedRetryable, false, true, err, protoDataMap(start.GetData())), nil
	}

	startResult := start.GetResult()
	login := LoginSessionActivityOutput{
		SessionToken: startResult.GetSessionToken(),
		AccessToken:  startResult.GetAccessToken(),
		DeviceId:     startResult.GetDeviceId(),
		Data:         startResult.GetData(),
	}
	if start.GetOtpRequired() {
		setWorkflowProgress(ctx, progress, stepLoginSession+"_otp_wait")
		otp, err := waitForOTP(ctx, OTPWaitInput{
			JobId:            input.GetJobId(),
			StepName:         stepLoginSession,
			Target:           &pb.OTPWaitInput_Email{Email: &pb.OTPWaitEmailTarget{Email: start.GetEmail()}},
			TimeoutSeconds:   start.GetOtpTimeoutSeconds(),
			IssuedAfterUnix:  start.GetOtpIssuedAfterUnix(),
			OtpParam:         registrationOTPParam,
			SubmittedAtParam: registrationOTPSubmittedAtParam,
		})
		if err != nil {
			_ = workflow.ExecuteActivity(retryCtx, browserAuthCancelActivityName, BrowserAuthCancelInput{FlowId: start.GetFlowId(), Mode: browserAuthModeLogin}).Get(ctx, nil)
			return failLoginSessionWorkflow(ctx, retryCtx, result, input.GetJobId(), stepLoginSession, statusFailedRetryable, false, true, err, protoDataMap(start.GetData())), nil
		}
		var completed RegisterActivityOutput
		if err := workflow.ExecuteActivity(browserCtx, browserAuthCompleteActivityName, BrowserAuthCompleteInput{
			JobId:              input.GetJobId(),
			AccountId:          account.GetAccountId(),
			FlowId:             start.GetFlowId(),
			Mode:               browserAuthModeLogin,
			OtpParam:           registrationOTPParam,
			SubmittedAtParam:   registrationOTPSubmittedAtParam,
			OtpIssuedAfterUnix: start.GetOtpIssuedAfterUnix(),
			OtpSource:          otp.GetSource(),
		}).Get(ctx, &completed); err != nil {
			return failLoginSessionWorkflow(ctx, retryCtx, result, input.GetJobId(), stepLoginSession, statusFailedRetryable, false, true, err, protoDataMap(completed.GetData())), nil
		}
		login = LoginSessionActivityOutput{
			SessionToken: completed.GetSessionToken(),
			AccessToken:  completed.GetAccessToken(),
			DeviceId:     completed.GetDeviceId(),
			Data:         completed.GetData(),
		}
	}

	if err := workflow.ExecuteActivity(retryCtx, persistRegisteredActivityName, PersistRegisteredInput{
		AccountId:    account.GetAccountId(),
		SessionToken: login.GetSessionToken(),
		AccessToken:  login.GetAccessToken(),
	}).Get(ctx, nil); err != nil {
		return failLoginSessionWorkflow(ctx, retryCtx, result, input.GetJobId(), "", statusFailedRecoverable, true, false, err, protoDataMap(login.GetData())), nil
	}

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobId:  input.GetJobId(),
		Result: login.GetData(),
	}).Get(ctx, nil)
	setWorkflowProgressSucceeded(ctx, progress)

	result.SessionToken = login.GetSessionToken()
	result.AccessToken = login.GetAccessToken()
	return result, nil
}
func RegisterAndActivateWorkflow(ctx workflow.Context, input RegisterAndActivateWorkflowInput) (RegisterAndActivateWorkflowResult, error) {
	progress := newWorkflowProgress(ctx, "RegisterAndActivateWorkflow", input.GetJobId())
	result := RegisterAndActivateWorkflowResult{JobId: input.GetJobId()}
	defer func() {
		finishWorkflowProgressOnError(ctx, progress, result.GetErrorMessage())
	}()
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	atomicCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(15*time.Minute))
	browserCtx := workflow.WithActivityOptions(ctx, heartbeatingActivityOptions(5*time.Minute, 30*time.Second))
	paymentCtx := workflow.WithActivityOptions(ctx, paymentActivityOptions())
	ensureLogonCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(30*time.Minute))

	setWorkflowProgress(ctx, progress, "create_job")
	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobId:     input.GetJobId(),
		AccountId: input.GetAccount().GetAccountId(),
		Action:    actionRegisterAndActivate,
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	var account AccountRef
	setWorkflowProgress(ctx, progress, "ensure_account")
	if err := workflow.ExecuteActivity(retryCtx, ensureAccountActivityName, EnsureAccountInput{Account: input.Account}).Get(ctx, &account); err != nil {
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), "", statusFailedRecoverable, true, false, err, nil), nil
	}

	var start BrowserAuthStartOutput
	setWorkflowProgress(ctx, progress, stepRegisterAccount)
	if err := workflow.ExecuteActivity(browserCtx, browserAuthStartActivityName, BrowserAuthStartInput{
		JobId:     input.GetJobId(),
		AccountId: account.GetAccountId(),
		Mode:      browserAuthModeRegister,
	}).Get(ctx, &start); err != nil {
		status, recoverable, retryable := registerFailurePolicy(err)
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), stepRegisterAccount, status, recoverable, retryable, err, protoDataMap(start.GetData())), nil
	}

	register := RegisterActivityOutput{}
	if start.GetResult() != nil {
		register = *start.GetResult()
	}
	registerData := func() map[string]any {
		return protoDataMap(register.GetData())
	}
	if start.GetOtpRequired() {
		setWorkflowProgress(ctx, progress, stepRegisterAccount+"_otp_wait")
		otp, err := waitForOTP(ctx, OTPWaitInput{
			JobId:            input.GetJobId(),
			StepName:         stepRegisterAccount,
			Target:           &pb.OTPWaitInput_Email{Email: &pb.OTPWaitEmailTarget{Email: start.GetEmail()}},
			TimeoutSeconds:   start.GetOtpTimeoutSeconds(),
			IssuedAfterUnix:  start.GetOtpIssuedAfterUnix(),
			OtpParam:         registrationOTPParam,
			SubmittedAtParam: registrationOTPSubmittedAtParam,
		})
		if err != nil {
			_ = workflow.ExecuteActivity(retryCtx, browserAuthCancelActivityName, BrowserAuthCancelInput{FlowId: start.GetFlowId(), Mode: browserAuthModeRegister}).Get(ctx, nil)
			return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), stepRegisterAccount, statusFailedRetryable, false, true, err, protoDataMap(start.GetData())), nil
		}
		if err := workflow.ExecuteActivity(browserCtx, browserAuthCompleteActivityName, BrowserAuthCompleteInput{
			JobId:              input.GetJobId(),
			AccountId:          account.GetAccountId(),
			FlowId:             start.GetFlowId(),
			Mode:               browserAuthModeRegister,
			OtpParam:           registrationOTPParam,
			SubmittedAtParam:   registrationOTPSubmittedAtParam,
			OtpIssuedAfterUnix: start.GetOtpIssuedAfterUnix(),
			OtpSource:          otp.GetSource(),
		}).Get(ctx, &register); err != nil {
			status, recoverable, retryable := registerFailurePolicy(err)
			return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), stepRegisterAccount, status, recoverable, retryable, err, registerData()), nil
		}
	}

	if err := workflow.ExecuteActivity(retryCtx, persistRegisteredActivityName, PersistRegisteredInput{
		AccountId:         account.GetAccountId(),
		SessionToken:      register.GetSessionToken(),
		AccessToken:       register.GetAccessToken(),
		PlusTrialEligible: register.GetPlusTrialEligible(),
		PlusTrialChecked:  register.GetPlusTrialChecked(),
	}).Get(ctx, nil); err != nil {
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), "", statusFailedRecoverable, true, false, err, registerData()), nil
	}

	var probe ProbePlusTrialActivityOutput
	setWorkflowProgress(ctx, progress, stepProbePlusTrial)
	if err := workflow.ExecuteActivity(atomicCtx, probePlusTrialActivityName, ProbePlusTrialActivityInput{
		JobId:     input.GetJobId(),
		AccountId: account.GetAccountId(),
	}).Get(ctx, &probe); err != nil {
		combined := map[string]any{"register_account": registerData(), "probe_plus_trial": protoDataMap(probe.GetData())}
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbePlusTrial, statusFailedRetryable, false, true, err, combined), nil
	}
	if !probe.GetChecked() {
		combined := map[string]any{"register_account": registerData(), "probe_plus_trial": protoDataMap(probe.GetData())}
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbePlusTrial, statusFailedRetryable, false, true, fmt.Errorf("plus trial eligibility is unknown"), combined), nil
	}
	if !probe.GetPlusTrialEligible() && !probe.GetPlusActive() {
		combined := map[string]any{"register_account": registerData(), "probe_plus_trial": protoDataMap(probe.GetData())}
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), stepProbePlusTrial, statusFailedFinal, false, false, fmt.Errorf("account is not plus trial eligible"), combined), nil
	}

	setWorkflowProgress(ctx, progress, stepGoPayAppLogin)
	logon, err := runGoPayAppAuth(ctx, atomicCtx, retryCtx, input.GetJobId())
	if err != nil {
		combined := map[string]any{"register_account": registerData(), "probe_plus_trial": protoDataMap(probe.GetData()), "gopay_login": protoDataMap(logon.GetData())}
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppLogin, statusFailedRetryable, false, true, err, combined), nil
	}

	var ensureLogon pb.EnsureLogonResponse
	setWorkflowProgress(ctx, progress, stepEnsureLogon)
	if err := workflow.ExecuteActivity(ensureLogonCtx, ensureLogonActivityName, &pb.EnsureLogonRequest{
		JobId:     input.GetJobId(),
		AccountId: account.GetAccountId(),
	}).Get(ctx, &ensureLogon); err != nil {
		combined := map[string]any{"register_account": registerData(), "probe_plus_trial": protoDataMap(probe.GetData()), "gopay_login": protoDataMap(logon.GetData())}
		if logonData := ensureLogonData(&ensureLogon); logonData != nil {
			combined["ensure_logon"] = logonData
		}
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), stepEnsureLogon, statusFailedRetryable, false, true, err, combined), nil
	}

	var payment GoPayActivityOutput
	setWorkflowProgress(ctx, progress, stepGoPayPayment)
	payment, err = runGoPayPayment(ctx, paymentCtx, retryCtx, GoPayActivityInput{
		JobId:             input.GetJobId(),
		AccountId:         account.GetAccountId(),
		SessionToken:      register.GetSessionToken(),
		AccessToken:       register.GetAccessToken(),
		UseAccountToken:   ensureLogon.GetAccountTokenReady(),
		CheckoutUrl:       probe.GetCheckoutUrl(),
		CheckoutSessionId: probe.GetCheckoutSessionId(),
	})
	if err != nil {
		combined := map[string]any{"register_account": registerData(), "probe_plus_trial": protoDataMap(probe.GetData()), "gopay_payment": protoDataMap(payment.GetData())}
		if logonData := ensureLogonData(&ensureLogon); logonData != nil {
			combined["ensure_logon"] = logonData
		}
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayPayment, statusFailedRetryable, false, true, err, combined), nil
	}

	combined := map[string]any{"register_account": registerData(), "probe_plus_trial": protoDataMap(probe.GetData()), "gopay_login": protoDataMap(logon.GetData()), "gopay_payment": protoDataMap(payment.GetData())}
	if logonData := ensureLogonData(&ensureLogon); logonData != nil {
		combined["ensure_logon"] = logonData
	}
	if err := workflow.ExecuteActivity(retryCtx, persistActivatedActivityName, PersistActivatedInput{
		AccountId:         account.GetAccountId(),
		SessionToken:      register.GetSessionToken(),
		AccessToken:       register.GetAccessToken(),
		ChargeRef:         payment.GetChargeRef(),
		PlusTrialEligible: payment.GetPlusTrialEligible(),
		PlusTrialChecked:  payment.GetPlusTrialChecked(),
		PlusActive:        payment.GetPlusActive(),
	}).Get(ctx, nil); err != nil {
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.GetJobId(), "", statusFailedRecoverable, true, false, err, combined), nil
	}

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobId:  input.GetJobId(),
		Result: protoData(combined),
	}).Get(ctx, nil)
	startRegisteredAccountProbeSideEffects(ctx, input.GetJobId(), account.GetAccountId())
	setWorkflowProgressSucceeded(ctx, progress)

	result.SessionToken = register.GetSessionToken()
	result.AccessToken = register.GetAccessToken()
	result.PlusTrialEligible = payment.GetPlusTrialEligible() || probe.GetPlusTrialEligible() || register.GetPlusTrialEligible()
	result.CheckoutUrl = register.GetCheckoutUrl()
	result.ActivationSuccess = true
	result.ChargeRef = payment.GetChargeRef()
	result.SnapToken = payment.GetSnapToken()
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
		JobId:     jobID,
		AccountId: accountID,
	})
	if err := future.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
		logger.Warn("failed to start account probe side effect", "account_id", accountID, "error", err)
	}
}
