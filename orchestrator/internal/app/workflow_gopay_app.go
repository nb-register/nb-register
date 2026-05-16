package app

import (
	"fmt"
	pb "orchestrator/pb"
	"time"

	"go.temporal.io/sdk/workflow"
)

func GoPayAppWorkflow(ctx workflow.Context, input GoPayAppWorkflowInput) (GoPayAppWorkflowResult, error) {
	progress := newWorkflowProgress(ctx, "GoPayAppWorkflow", input.GetJobId())
	result := GoPayAppWorkflowResult{JobId: input.GetJobId()}
	defer func() {
		finishWorkflowProgressOnError(ctx, progress, result.GetErrorMessage())
	}()
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	gopayCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(30*time.Minute))

	setWorkflowProgress(ctx, progress, "create_job")
	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobId:  input.GetJobId(),
		Action: actionGoPayApp,
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	combined := map[string]any{}
	setWorkflowProgress(ctx, progress, stepGoPayAppLogin)
	login, err := runGoPayAppAuth(ctx, gopayCtx, retryCtx, input.GetJobId())
	if err != nil {
		combined["login"] = protoDataMap(login.GetData())
		return failGoPayAppWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppLogin, statusFailedRetryable, false, true, err, combined), nil
	}
	combined["login"] = protoDataMap(login.GetData())

	setWorkflowProgress(ctx, progress, stepGoPayAppChangePhone)
	changePhone, err := runGoPayAppChangePhone(ctx, gopayCtx, input.GetJobId())
	if err != nil {
		combined["change_phone"] = protoDataMap(changePhone.GetData())
		return failGoPayAppWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppChangePhone, statusFailedRetryable, false, true, err, combined), nil
	}
	combined["change_phone"] = protoDataMap(changePhone.GetData())
	result.ActivationId = changePhone.GetActivationId()
	result.ChangePhoneComplete = changePhone.GetChangePhoneComplete()

	setWorkflowProgress(ctx, progress, stepGoPayAppDeactivate)
	deactivate, err := runGoPayAppDeactivate(ctx, gopayCtx, input.GetJobId(), changePhone.GetActivationId())
	if err != nil {
		combined["deactivate"] = protoDataMap(deactivate.GetData())
		return failGoPayAppWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppDeactivate, statusFailedRetryable, false, true, err, combined), nil
	}
	combined["deactivate"] = protoDataMap(deactivate.GetData())
	result.DeactivateComplete = deactivate.GetDeactivateComplete()

	setWorkflowProgress(ctx, progress, stepGoPayAppSignup)
	signup, err := runGoPayAppSignup(ctx, gopayCtx, retryCtx, input.GetJobId())
	if err != nil {
		combined["signup"] = protoDataMap(signup.GetData())
		return failGoPayAppWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppSignup, statusFailedRetryable, false, true, err, combined), nil
	}
	combined["signup"] = protoDataMap(signup.GetData())
	result.SignupComplete = signup.GetSignupComplete()

	setWorkflowProgress(ctx, progress, stepGoPayAppCreatePin)
	createPin, err := runGoPayAppCreatePin(ctx, gopayCtx, retryCtx, input.GetJobId())
	if err != nil {
		combined["create_pin"] = protoDataMap(createPin.GetData())
		return failGoPayAppWorkflow(ctx, retryCtx, result, input.GetJobId(), stepGoPayAppCreatePin, statusFailedRetryable, false, true, err, combined), nil
	}
	combined["create_pin"] = protoDataMap(createPin.GetData())
	result.SignupPinComplete = createPin.GetSignupPinComplete()
	result.AccountTokenReady = createPin.GetAccountTokenReady()

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobId:  input.GetJobId(),
		Result: protoData(combined),
	}).Get(ctx, nil)

	result.Success = true
	setWorkflowProgressSucceeded(ctx, progress)
	return result, nil
}
func runGoPayAppChangePhone(ctx workflow.Context, activityCtx workflow.Context, jobID string) (GoPayAppStepOutput, error) {
	var failureCount int32
	var last GoPayAppStepOutput
	for {
		var start GoPayAppChangePhoneStartOutput
		if err := workflow.ExecuteActivity(activityCtx, goPayAppChangePhoneStartActivityName, GoPayAppChangePhoneStartInput{
			JobId:        jobID,
			FailureCount: failureCount,
		}).Get(ctx, &start); err != nil {
			return GoPayAppStepOutput{
				ActivationId: start.GetActivationId(),
				Data:         start.GetData(),
			}, err
		}
		failureCount = start.GetFailureCount()
		last = GoPayAppStepOutput{
			ActivationId: start.GetActivationId(),
			Data:         start.GetData(),
		}

		for otpAttempt := int32(0); otpAttempt <= start.GetOtpRetryAttempts(); otpAttempt++ {
			wait, err := waitForOTP(ctx, OTPWaitInput{
				JobId:          jobID,
				StepName:       stepGoPayAppChangePhoneSMSWait,
				Target:         &pb.OTPWaitInput_Sms{Sms: &pb.OTPWaitSMSTarget{ActivationId: start.GetActivationId()}},
				TimeoutSeconds: start.GetOtpTimeoutSeconds(),
			})
			if err != nil {
				_ = workflow.ExecuteActivity(activityCtx, goPayAppSMSCancelBeforeRotationActivityName, GoPayAppSMSActivationInput{
					JobId:        jobID,
					ActivationId: start.GetActivationId(),
					FailureCount: failureCount,
					Reason:       err.Error(),
				}).Get(ctx, nil)
				return last, err
			}
			if wait.GetFound() {
				var complete GoPayAppChangePhoneCompleteOutput
				err = workflow.ExecuteActivity(activityCtx, goPayAppChangePhoneCompleteActivityName, GoPayAppChangePhoneCompleteInput{
					JobId:        jobID,
					ActivationId: start.GetActivationId(),
					Code:         wait.GetCode(),
					FailureCount: failureCount,
				}).Get(ctx, &complete)
				last = goPayAppStepFromChangePhoneComplete(complete)
				if err != nil {
					return last, err
				}
				failureCount = complete.GetFailureCount()
				if complete.GetChangePhoneComplete() {
					return last, nil
				}
				if complete.GetRetryableFailure() {
					break
				}
				return last, fmt.Errorf("gopay change phone did not complete")
			}

			if otpAttempt < start.GetOtpRetryAttempts() {
				var retry GoPayAppChangePhoneRetryOutput
				if err := workflow.ExecuteActivity(activityCtx, goPayAppChangePhoneRetryActivityName, GoPayAppChangePhoneRetryInput{
					JobId:        jobID,
					ActivationId: start.GetActivationId(),
					OtpAttempt:   otpAttempt + 1,
				}).Get(ctx, &retry); err != nil {
					_ = workflow.ExecuteActivity(activityCtx, goPayAppSMSCancelBeforeRotationActivityName, GoPayAppSMSActivationInput{
						JobId:        jobID,
						ActivationId: start.GetActivationId(),
						FailureCount: failureCount,
						Reason:       err.Error(),
					}).Get(ctx, nil)
					return last, err
				}
				if retry.GetOtpSent() {
					continue
				}
				if retry.GetErrorMessage() != "" {
					wait.ErrorMessage = "ChangePhoneRetry: " + retry.GetErrorMessage()
				}
			}

			reason := wait.GetErrorMessage()
			if reason == "" {
				reason = "WaitCode: otp not found"
			} else {
				reason = "WaitCode: " + reason
			}
			var canceled GoPayAppSMSActivationOutput
			err = workflow.ExecuteActivity(activityCtx, goPayAppSMSCancelBeforeRotationActivityName, GoPayAppSMSActivationInput{
				JobId:        jobID,
				ActivationId: start.GetActivationId(),
				FailureCount: failureCount,
				Reason:       reason,
			}).Get(ctx, &canceled)
			failureCount = canceled.GetFailureCount()
			last.ActivationId = canceled.GetActivationId()
			last.Data = canceled.GetData()
			if err != nil {
				return last, err
			}
			break
		}
	}
}
func goPayAppStepFromChangePhoneComplete(output GoPayAppChangePhoneCompleteOutput) GoPayAppStepOutput {
	return GoPayAppStepOutput{
		ActivationId:        output.GetActivationId(),
		Stage:               output.GetStage(),
		Phone:               output.GetPhone(),
		ChangePhoneComplete: output.GetChangePhoneComplete(),
		Data:                output.GetData(),
	}
}
func runGoPayAppDeactivate(ctx workflow.Context, activityCtx workflow.Context, jobID, activationID string) (GoPayAppStepOutput, error) {
	var start GoPayAppDeactivateStartOutput
	if err := workflow.ExecuteActivity(activityCtx, goPayAppDeactivateStartActivityName, GoPayAppDeactivateStartInput{
		JobId:        jobID,
		ActivationId: activationID,
	}).Get(ctx, &start); err != nil {
		return GoPayAppStepOutput{ActivationId: activationID, Data: start.GetData()}, err
	}
	if !start.GetOtpRequired() {
		return GoPayAppStepOutput{ActivationId: activationID, Data: start.GetData()}, fmt.Errorf("gopay deactivate did not request OTP")
	}

	wait, err := waitForOTP(ctx, OTPWaitInput{
		JobId:          jobID,
		StepName:       stepGoPayAppDeactivateSMSWait,
		Target:         &pb.OTPWaitInput_Sms{Sms: &pb.OTPWaitSMSTarget{ActivationId: activationID}},
		TimeoutSeconds: start.GetTimeoutSeconds(),
	})
	if err != nil {
		_ = workflow.ExecuteActivity(activityCtx, goPayAppSMSFinishActivityName, GoPayAppSMSActivationInput{
			JobId:        jobID,
			ActivationId: activationID,
			Reason:       err.Error(),
		}).Get(ctx, nil)
		return GoPayAppStepOutput{ActivationId: activationID, Data: wait.GetData()}, err
	}
	if !wait.GetFound() {
		reason := wait.GetErrorMessage()
		if reason == "" {
			reason = "otp not found"
		}
		var finished GoPayAppSMSActivationOutput
		_ = workflow.ExecuteActivity(activityCtx, goPayAppSMSFinishActivityName, GoPayAppSMSActivationInput{
			JobId:        jobID,
			ActivationId: activationID,
			Reason:       "WaitCode deactivate: " + reason,
		}).Get(ctx, &finished)
		return GoPayAppStepOutput{ActivationId: activationID, Data: finished.GetData()}, fmt.Errorf("WaitCode deactivate: %s", reason)
	}

	var complete GoPayAppDeactivateCompleteOutput
	err = workflow.ExecuteActivity(activityCtx, goPayAppDeactivateCompleteActivityName, GoPayAppDeactivateCompleteInput{
		JobId:        jobID,
		ActivationId: activationID,
		Code:         wait.GetCode(),
	}).Get(ctx, &complete)
	return goPayAppStepFromDeactivateComplete(complete), err
}
func goPayAppStepFromDeactivateComplete(output GoPayAppDeactivateCompleteOutput) GoPayAppStepOutput {
	return GoPayAppStepOutput{
		ActivationId:       output.GetActivationId(),
		DeactivateComplete: output.GetDeactivateComplete(),
		Data:               output.GetData(),
	}
}
func runGoPayAppSignup(ctx workflow.Context, activityCtx workflow.Context, cancelCtx workflow.Context, jobID string) (GoPayAppStepOutput, error) {
	var start GoPayAppOTPOutput
	if err := workflow.ExecuteActivity(activityCtx, goPayAppOTPStartActivityName, GoPayAppOTPStartInput{
		JobId:     jobID,
		Operation: goPayAppOTPOperationSignup,
		StepName:  stepGoPayAppSignup,
	}).Get(ctx, &start); err != nil {
		return goPayAppStepFromOTP(start), err
	}
	if start.GetReady() || start.GetAccountTokenReady() || start.GetSignupComplete() {
		return goPayAppStepFromOTP(start), nil
	}
	if !start.GetOtpRequired() {
		return goPayAppStepFromOTP(start), fmt.Errorf("gopay signup did not request OTP and did not complete")
	}

	otp, err := waitForOTP(ctx, OTPWaitInput{
		JobId:            jobID,
		StepName:         stepGoPayAppSignup,
		Target:           &pb.OTPWaitInput_Payment{Payment: &pb.OTPWaitPaymentTarget{Source: goPayLocalSource}},
		TimeoutSeconds:   start.GetTimeoutSeconds(),
		IssuedAfterUnix:  start.GetIssuedAfterUnix(),
		OtpParam:         paymentOTPParam,
		SubmittedAtParam: paymentOTPSubmittedAtParam,
	})
	if err != nil {
		return goPayAppStepFromOTP(start), err
	}

	var completed GoPayAppOTPOutput
	if err := workflow.ExecuteActivity(activityCtx, goPayAppOTPCompleteActivityName, GoPayAppOTPCompleteInput{
		JobId:            jobID,
		Operation:        goPayAppOTPOperationSignup,
		OtpParam:         paymentOTPParam,
		SubmittedAtParam: paymentOTPSubmittedAtParam,
		IssuedAfterUnix:  start.GetIssuedAfterUnix(),
		OtpSource:        otp.GetSource(),
		Data:             start.GetData(),
	}).Get(ctx, &completed); err != nil {
		return goPayAppStepFromOTP(completed), err
	}
	if completed.GetSignupComplete() || completed.GetReady() || completed.GetAccountTokenReady() {
		return goPayAppStepFromOTP(completed), nil
	}
	return goPayAppStepFromOTP(completed), fmt.Errorf("gopay signup did not complete")
}
func runGoPayAppAuth(ctx workflow.Context, activityCtx workflow.Context, cancelCtx workflow.Context, jobID string) (GoPayAppStepOutput, error) {
	var last GoPayAppOTPOutput
	for attempt := 0; attempt < 4; attempt++ {
		if err := workflow.ExecuteActivity(activityCtx, goPayAppOTPStartActivityName, GoPayAppOTPStartInput{
			JobId:     jobID,
			Operation: goPayAppOTPOperationAuth,
			StepName:  stepGoPayAppLogin,
		}).Get(ctx, &last); err != nil {
			return goPayAppStepFromOTP(last), err
		}
		if last.GetReady() || last.GetAccountTokenReady() {
			return goPayAppStepFromOTP(last), nil
		}
		if last.GetPinSetupRequired() {
			pinResult, err := runGoPayAppCreatePin(ctx, activityCtx, cancelCtx, jobID)
			if err != nil {
				return pinResult, err
			}
			continue
		}
		if !last.GetOtpRequired() {
			continue
		}

		otp, err := waitForOTP(ctx, OTPWaitInput{
			JobId:            jobID,
			StepName:         stepGoPayAppLogin,
			Target:           &pb.OTPWaitInput_Payment{Payment: &pb.OTPWaitPaymentTarget{Source: goPayLocalSource}},
			TimeoutSeconds:   last.GetTimeoutSeconds(),
			IssuedAfterUnix:  last.GetIssuedAfterUnix(),
			OtpParam:         paymentOTPParam,
			SubmittedAtParam: paymentOTPSubmittedAtParam,
		})
		if err != nil {
			return goPayAppStepFromOTP(last), err
		}

		if err := workflow.ExecuteActivity(activityCtx, goPayAppOTPCompleteActivityName, GoPayAppOTPCompleteInput{
			JobId:            jobID,
			Operation:        goPayAppOTPOperationAuth,
			OtpParam:         paymentOTPParam,
			SubmittedAtParam: paymentOTPSubmittedAtParam,
			IssuedAfterUnix:  last.GetIssuedAfterUnix(),
			OtpSource:        otp.GetSource(),
			Data:             last.GetData(),
		}).Get(ctx, &last); err != nil {
			return goPayAppStepFromOTP(last), err
		}
		if last.GetReady() || last.GetAccountTokenReady() {
			return goPayAppStepFromOTP(last), nil
		}
		if last.GetPinSetupRequired() {
			pinResult, err := runGoPayAppCreatePin(ctx, activityCtx, cancelCtx, jobID)
			if err != nil {
				return pinResult, err
			}
		}
	}
	return goPayAppStepFromOTP(last), fmt.Errorf("gopay auth did not reach token-valid state")
}
func runGoPayAppCreatePin(ctx workflow.Context, activityCtx workflow.Context, cancelCtx workflow.Context, jobID string) (GoPayAppStepOutput, error) {
	var start GoPayAppOTPOutput
	if err := workflow.ExecuteActivity(activityCtx, goPayAppOTPStartActivityName, GoPayAppOTPStartInput{
		JobId:     jobID,
		Operation: goPayAppOTPOperationCreatePin,
		StepName:  stepGoPayAppCreatePin,
	}).Get(ctx, &start); err != nil {
		return goPayAppStepFromOTP(start), err
	}
	if start.GetReady() || start.GetAccountTokenReady() || start.GetSignupPinComplete() {
		return goPayAppStepFromOTP(start), nil
	}
	if !start.GetOtpRequired() {
		return goPayAppStepFromOTP(start), fmt.Errorf("gopay create pin did not request OTP and did not become ready")
	}

	otp, err := waitForOTP(ctx, OTPWaitInput{
		JobId:            jobID,
		StepName:         stepGoPayAppCreatePin,
		Target:           &pb.OTPWaitInput_Payment{Payment: &pb.OTPWaitPaymentTarget{Source: goPayLocalSource}},
		TimeoutSeconds:   start.GetTimeoutSeconds(),
		IssuedAfterUnix:  start.GetIssuedAfterUnix(),
		OtpParam:         paymentOTPParam,
		SubmittedAtParam: paymentOTPSubmittedAtParam,
	})
	if err != nil {
		return goPayAppStepFromOTP(start), err
	}

	var completed GoPayAppOTPOutput
	if err := workflow.ExecuteActivity(activityCtx, goPayAppOTPCompleteActivityName, GoPayAppOTPCompleteInput{
		JobId:            jobID,
		Operation:        goPayAppOTPOperationCreatePin,
		OtpParam:         paymentOTPParam,
		SubmittedAtParam: paymentOTPSubmittedAtParam,
		IssuedAfterUnix:  start.GetIssuedAfterUnix(),
		OtpSource:        otp.GetSource(),
		Data:             start.GetData(),
	}).Get(ctx, &completed); err != nil {
		return goPayAppStepFromOTP(completed), err
	}
	return goPayAppStepFromOTP(completed), nil
}
func goPayAppStepFromOTP(output GoPayAppOTPOutput) GoPayAppStepOutput {
	return GoPayAppStepOutput{
		Ready:             output.GetReady(),
		Stage:             output.GetStage(),
		Phone:             output.GetPhone(),
		AccountTokenReady: output.GetAccountTokenReady(),
		SignupComplete:    output.GetSignupComplete(),
		SignupPinComplete: output.GetSignupPinComplete(),
		Data:              output.GetData(),
	}
}
