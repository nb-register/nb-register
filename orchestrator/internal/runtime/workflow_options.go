package runtime

import (
	"fmt"
	"strconv"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func waitForOTP(ctx workflow.Context, input OTPWaitInput) (OTPWaitOutput, error) {
	channel := otpWaitInputChannel(input)
	if channel == "" {
		return OTPWaitOutput{}, fmt.Errorf("otp wait target missing")
	}
	timeoutSeconds := input.GetTimeoutSeconds()
	if timeoutSeconds <= 0 {
		if channel == otpWaitChannelSMS {
			timeoutSeconds = defaultChangePhoneOTPWaitSeconds
		} else {
			timeoutSeconds = 120
		}
	}
	input.TimeoutSeconds = timeoutSeconds
	timeout := time.Duration(timeoutSeconds) * time.Second
	waitCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: timeout + 10*time.Second,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	})
	if channel == otpWaitChannelSMS {
		var out OTPWaitOutput
		err := workflow.ExecuteActivity(waitCtx, waitOTPActivityName, input).Get(ctx, &out)
		return out, err
	}

	manualCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 3))
	otpCtx, cancelOTP := workflow.WithCancel(waitCtx)
	defer cancelOTP()

	otpFuture := workflow.ExecuteActivity(otpCtx, waitOTPActivityName, input)
	timer := workflow.NewTimer(ctx, timeout)
	signalCh := workflow.GetSignalChannel(ctx, manualOTPSignalName)

	otpDone := false
	lastErr := ""
	for {
		var (
			found        bool
			timedOut     bool
			manualSignal bool
			otp          OTPWaitOutput
		)
		selector := workflow.NewSelector(ctx)
		if !otpDone {
			selector.AddFuture(otpFuture, func(f workflow.Future) {
				var out OTPWaitOutput
				if err := f.Get(ctx, &out); err != nil {
					lastErr = err.Error()
				} else if out.GetFound() {
					otp = out
					found = true
				}
				otpDone = true
			})
		}
		selector.AddReceive(signalCh, func(c workflow.ReceiveChannel, more bool) {
			var signal ManualOTPSignal
			c.Receive(ctx, &signal)
			manualSignal = true
		})
		selector.AddFuture(timer, func(f workflow.Future) {
			timedOut = true
		})
		selector.Select(ctx)

		if found {
			cancelOTP()
			return otp, nil
		}
		if manualSignal {
			var manual OTPWaitOutput
			err := workflow.ExecuteActivity(manualCtx, fetchManualOTPActivityName, input).Get(ctx, &manual)
			if err != nil {
				lastErr = err.Error()
				continue
			}
			if manual.GetFound() {
				cancelOTP()
				return manual, nil
			}
		}
		if timedOut {
			cancelOTP()
			if channel == otpWaitChannelPayment {
				if lastErr != "" {
					return OTPWaitOutput{}, fmt.Errorf("payment otp not received after %ds: %s", timeoutSeconds, lastErr)
				}
				return OTPWaitOutput{}, fmt.Errorf("payment otp not received after %ds", timeoutSeconds)
			}
			if lastErr != "" {
				return OTPWaitOutput{}, fmt.Errorf("otp not received after %ds: %s", timeoutSeconds, lastErr)
			}
			return OTPWaitOutput{}, fmt.Errorf("otp not received after %ds", timeoutSeconds)
		}
	}
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
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	}
}
func heartbeatingActivityOptions(timeout time.Duration, heartbeatTimeout time.Duration) workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: timeout,
		HeartbeatTimeout:    heartbeatTimeout,
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
