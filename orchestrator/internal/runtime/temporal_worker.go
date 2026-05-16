package runtime

import (
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
)

func registerTemporalWorker(w worker.Worker, s *orchestratorServer) {
	w.RegisterWorkflow(RegisterAccountWorkflow)
	w.RegisterWorkflow(ActivateAccountWorkflow)
	w.RegisterWorkflow(AutoPayWorkflow)
	w.RegisterWorkflow(GoPayAppWorkflow)
	w.RegisterWorkflow(ProbeAccountWorkflow)
	w.RegisterWorkflow(LoginSessionWorkflow)
	w.RegisterWorkflow(RegisterAndActivateWorkflow)
	w.RegisterWorkflow(RegisterMailboxWorkflow)
	w.RegisterWorkflow(MailboxOAuthWorkflow)

	w.RegisterActivityWithOptions(s.CreateJobActivity, activity.RegisterOptions{Name: createJobActivityName})
	w.RegisterActivityWithOptions(s.EnsureAccountActivity, activity.RegisterOptions{Name: ensureAccountActivityName})
	w.RegisterActivityWithOptions(s.ResolveAccountFromJobActivity, activity.RegisterOptions{Name: resolveAccountActivityName})
	w.RegisterActivityWithOptions(s.BrowserAuthStartActivity, activity.RegisterOptions{Name: browserAuthStartActivityName})
	w.RegisterActivityWithOptions(s.BrowserAuthCompleteActivity, activity.RegisterOptions{Name: browserAuthCompleteActivityName})
	w.RegisterActivityWithOptions(s.BrowserAuthCancelActivity, activity.RegisterOptions{Name: browserAuthCancelActivityName})
	w.RegisterActivityWithOptions(s.OTPWaitActivity, activity.RegisterOptions{Name: waitOTPActivityName})
	w.RegisterActivityWithOptions(s.FetchManualOTPActivity, activity.RegisterOptions{Name: fetchManualOTPActivityName})
	w.RegisterActivityWithOptions(s.EnsureLogonActivity, activity.RegisterOptions{Name: ensureLogonActivityName})
	w.RegisterActivityWithOptions(s.GoPayPaymentStartActivity, activity.RegisterOptions{Name: goPayPaymentStartActivityName})
	w.RegisterActivityWithOptions(s.GoPayPaymentCompleteActivity, activity.RegisterOptions{Name: goPayPaymentCompleteActivityName})
	w.RegisterActivityWithOptions(s.GoPayPaymentCancelActivity, activity.RegisterOptions{Name: goPayPaymentCancelActivityName})
	w.RegisterActivityWithOptions(s.GoPayAppOTPStartActivity, activity.RegisterOptions{Name: goPayAppOTPStartActivityName})
	w.RegisterActivityWithOptions(s.GoPayAppOTPCompleteActivity, activity.RegisterOptions{Name: goPayAppOTPCompleteActivityName})
	w.RegisterActivityWithOptions(s.GoPayAppChangePhoneStartActivity, activity.RegisterOptions{Name: goPayAppChangePhoneStartActivityName})
	w.RegisterActivityWithOptions(s.GoPayAppChangePhoneRetryActivity, activity.RegisterOptions{Name: goPayAppChangePhoneRetryActivityName})
	w.RegisterActivityWithOptions(s.GoPayAppSMSCancelBeforeRotationActivity, activity.RegisterOptions{Name: goPayAppSMSCancelBeforeRotationActivityName})
	w.RegisterActivityWithOptions(s.GoPayAppSMSFinishActivity, activity.RegisterOptions{Name: goPayAppSMSFinishActivityName})
	w.RegisterActivityWithOptions(s.GoPayAppChangePhoneCompleteActivity, activity.RegisterOptions{Name: goPayAppChangePhoneCompleteActivityName})
	w.RegisterActivityWithOptions(s.GoPayAppDeactivateStartActivity, activity.RegisterOptions{Name: goPayAppDeactivateStartActivityName})
	w.RegisterActivityWithOptions(s.GoPayAppDeactivateCompleteActivity, activity.RegisterOptions{Name: goPayAppDeactivateCompleteActivityName})
	w.RegisterActivityWithOptions(s.ProbePlusTrialAtomicActivity, activity.RegisterOptions{Name: probePlusTrialActivityName})
	w.RegisterActivityWithOptions(s.ProbeTierAtomicActivity, activity.RegisterOptions{Name: probeTierActivityName})
	w.RegisterActivityWithOptions(s.RegisterMailboxAtomicActivity, activity.RegisterOptions{Name: registerMailboxActivityName})
	w.RegisterActivityWithOptions(s.MailboxOAuthAtomicActivity, activity.RegisterOptions{Name: mailboxOAuthActivityName})
	w.RegisterActivityWithOptions(s.PersistRegisteredActivity, activity.RegisterOptions{Name: persistRegisteredActivityName})
	w.RegisterActivityWithOptions(s.PersistActivatedActivity, activity.RegisterOptions{Name: persistActivatedActivityName})
	w.RegisterActivityWithOptions(s.MarkJobFailedActivity, activity.RegisterOptions{Name: markJobFailedActivityName})
	w.RegisterActivityWithOptions(s.MarkJobSucceededActivity, activity.RegisterOptions{Name: markJobSucceededActivityName})
}
