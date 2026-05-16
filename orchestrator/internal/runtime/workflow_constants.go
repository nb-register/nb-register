package runtime

import "orchestrator/internal/contracts"

const (
	taskQueueDefault = contracts.TaskQueueDefault

	createJobActivityName                       = contracts.CreateJobActivityName
	ensureAccountActivityName                   = contracts.EnsureAccountActivityName
	resolveAccountActivityName                  = contracts.ResolveAccountActivityName
	browserAuthStartActivityName                = contracts.BrowserAuthStartActivityName
	browserAuthCompleteActivityName             = contracts.BrowserAuthCompleteActivityName
	browserAuthCancelActivityName               = contracts.BrowserAuthCancelActivityName
	waitOTPActivityName                         = contracts.WaitOTPActivityName
	fetchManualOTPActivityName                  = contracts.FetchManualOTPActivityName
	ensureLogonActivityName                     = contracts.EnsureLogonActivityName
	goPayPaymentStartActivityName               = contracts.GoPayPaymentStartActivityName
	goPayPaymentCompleteActivityName            = contracts.GoPayPaymentCompleteActivityName
	goPayPaymentCancelActivityName              = contracts.GoPayPaymentCancelActivityName
	goPayAppOTPStartActivityName                = contracts.GoPayAppOTPStartActivityName
	goPayAppOTPCompleteActivityName             = contracts.GoPayAppOTPCompleteActivityName
	goPayAppChangePhoneStartActivityName        = contracts.GoPayAppChangePhoneStartActivityName
	goPayAppChangePhoneRetryActivityName        = contracts.GoPayAppChangePhoneRetryActivityName
	goPayAppSMSCancelBeforeRotationActivityName = contracts.GoPayAppSMSCancelBeforeRotationActivityName
	goPayAppSMSFinishActivityName               = contracts.GoPayAppSMSFinishActivityName
	goPayAppChangePhoneCompleteActivityName     = contracts.GoPayAppChangePhoneCompleteActivityName
	goPayAppDeactivateStartActivityName         = contracts.GoPayAppDeactivateStartActivityName
	goPayAppDeactivateCompleteActivityName      = contracts.GoPayAppDeactivateCompleteActivityName
	probePlusTrialActivityName                  = contracts.ProbePlusTrialActivityName
	probeTierActivityName                       = contracts.ProbeTierActivityName
	registerMailboxActivityName                 = contracts.RegisterMailboxActivityName
	mailboxOAuthActivityName                    = contracts.MailboxOAuthActivityName
	persistRegisteredActivityName               = contracts.PersistRegisteredActivityName
	persistActivatedActivityName                = contracts.PersistActivatedActivityName
	markJobFailedActivityName                   = contracts.MarkJobFailedActivityName
	markJobSucceededActivityName                = contracts.MarkJobSucceededActivityName

	manualOTPSignalName       = contracts.ManualOTPSignalName
	workflowProgressQueryName = contracts.WorkflowProgressQueryName
)
