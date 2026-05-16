package runtime

import pb "orchestrator/pb"

type AccountSpec = pb.AccountSpec
type CreateJobInput = pb.CreateJobInput
type EnsureAccountInput = pb.EnsureAccountInput
type AccountRef = pb.AccountRef
type ResolveAccountInput = pb.ResolveAccountInput

type RegisterActivityOutput = pb.RegisterActivityOutput
type BrowserAuthStartInput = pb.BrowserAuthStartInput
type BrowserAuthStartOutput = pb.BrowserAuthStartOutput
type BrowserAuthCompleteInput = pb.BrowserAuthCompleteInput
type BrowserAuthCancelInput = pb.BrowserAuthCancelInput
type OTPWaitInput = pb.OTPWaitInput
type OTPWaitOutput = pb.OTPWaitOutput
type ManualOTPSignal = pb.ManualOTPSignal
type GoPayActivityInput = pb.GoPayActivityInput
type GoPayActivityOutput = pb.GoPayActivityOutput
type GoPayPaymentStartOutput = pb.GoPayPaymentStartOutput
type GoPayPaymentCompleteInput = pb.GoPayPaymentCompleteInput
type GoPayPaymentCancelInput = pb.GoPayPaymentCancelInput

type GoPayAppStepInput = pb.GoPayAppStepInput
type GoPayAppStepOutput = pb.GoPayAppStepOutput
type GoPayAppChangePhoneStartInput = pb.GoPayAppChangePhoneStartInput
type GoPayAppChangePhoneStartOutput = pb.GoPayAppChangePhoneStartOutput
type GoPayAppChangePhoneRetryInput = pb.GoPayAppChangePhoneRetryInput
type GoPayAppChangePhoneRetryOutput = pb.GoPayAppChangePhoneRetryOutput
type GoPayAppSMSActivationInput = pb.GoPayAppSMSActivationInput
type GoPayAppSMSActivationOutput = pb.GoPayAppSMSActivationOutput
type GoPayAppChangePhoneCompleteInput = pb.GoPayAppChangePhoneCompleteInput
type GoPayAppChangePhoneCompleteOutput = pb.GoPayAppChangePhoneCompleteOutput
type GoPayAppDeactivateStartInput = pb.GoPayAppDeactivateStartInput
type GoPayAppDeactivateStartOutput = pb.GoPayAppDeactivateStartOutput
type GoPayAppDeactivateCompleteInput = pb.GoPayAppDeactivateCompleteInput
type GoPayAppDeactivateCompleteOutput = pb.GoPayAppDeactivateCompleteOutput
type GoPayAppOTPStartInput = pb.GoPayAppOTPStartInput
type GoPayAppOTPOutput = pb.GoPayAppOTPOutput
type GoPayAppOTPCompleteInput = pb.GoPayAppOTPCompleteInput

type ProbePlusTrialActivityInput = pb.ProbePlusTrialActivityInput
type ProbeTierActivityInput = pb.ProbeTierActivityInput
type ProbePlusTrialActivityOutput = pb.ProbePlusTrialActivityOutput
type ProbeTierActivityOutput = pb.ProbeTierActivityOutput
type LoginSessionActivityOutput = pb.LoginSessionActivityOutput

type PersistRegisteredInput = pb.PersistRegisteredInput
type PersistActivatedInput = pb.PersistActivatedInput
type JobFailureInput = pb.JobFailureInput
type JobSuccessInput = pb.JobSuccessInput
type WorkflowProgress = pb.WorkflowProgress

type MailboxRegistrationActivityInput = pb.MailboxRegistrationActivityInput
type MailboxRegistrationActivityOutput = pb.MailboxRegistrationActivityOutput
type RegisteredMailboxResult = pb.RegisteredMailboxResult
type MailboxOAuthActivityInput = pb.MailboxOAuthActivityInput
type MailboxOAuthActivityOutput = pb.MailboxOAuthActivityOutput

type RegisterAccountWorkflowInput = pb.RegisterAccountWorkflowInput
type RegisterAccountWorkflowResult = pb.RegisterAccountWorkflowResult
type ActivateAccountWorkflowInput = pb.ActivateAccountWorkflowInput
type ActivateAccountWorkflowResult = pb.ActivateAccountWorkflowResult
type AutoPayWorkflowInput = pb.AutoPayWorkflowInput
type AutoPayWorkflowResult = pb.AutoPayWorkflowResult
type GoPayAppWorkflowInput = pb.GoPayAppWorkflowInput
type GoPayAppWorkflowResult = pb.GoPayAppWorkflowResult
type ProbeAccountWorkflowInput = pb.ProbeAccountWorkflowInput
type LoginSessionWorkflowInput = pb.LoginSessionWorkflowInput
type LoginSessionWorkflowResult = pb.LoginSessionWorkflowResult
type ProbeAccountWorkflowResult = pb.ProbeAccountWorkflowResult
type RegisterAndActivateWorkflowInput = pb.RegisterAndActivateWorkflowInput
type RegisterAndActivateWorkflowResult = pb.RegisterAndActivateWorkflowResult
type RegisterMailboxWorkflowInput = pb.RegisterMailboxWorkflowInput
type RegisterMailboxWorkflowResult = pb.RegisterMailboxWorkflowResult
type MailboxOAuthWorkflowInput = pb.MailboxOAuthWorkflowInput
type MailboxOAuthWorkflowResult = pb.MailboxOAuthWorkflowResult
