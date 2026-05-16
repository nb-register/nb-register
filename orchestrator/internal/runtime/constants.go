package runtime

import (
	"orchestrator/internal/contracts"
	"orchestrator/internal/jobstatus"
)

const (
	actionRegister            = contracts.ActionRegister
	actionActivate            = contracts.ActionActivate
	actionAutopay             = contracts.ActionAutopay
	actionGoPayApp            = contracts.ActionGoPayApp
	actionProbeAccount        = contracts.ActionProbeAccount
	actionLoginSession        = contracts.ActionLoginSession
	actionRegisterAndActivate = contracts.ActionRegisterAndActivate
	actionRegisterMailbox     = contracts.ActionRegisterMailbox
	actionMailboxOAuth        = contracts.ActionMailboxOAuth

	statusCreated           = jobstatus.Created
	statusRunning           = jobstatus.Running
	statusSucceeded         = jobstatus.Succeeded
	statusFailedRecoverable = jobstatus.FailedRecoverable
	statusFailedRetryable   = jobstatus.FailedRetryable
	statusFailedFinal       = jobstatus.FailedFinal

	accountStatusRegistered        = "REGISTERED"
	accountStatusActivated         = "ACTIVATED"
	accountStatusUserAlreadyExists = "USER_ALREADY_EXISTS"

	emailStatusAvailable         = "AVAILABLE"
	emailStatusRegistered        = "REGISTERED"
	emailStatusOAuthPending      = "OAUTH_PENDING"
	emailStatusUserAlreadyExists = "USER_ALREADY_EXISTS"
	emailStatusRegistrationFail  = "REGISTRATION_FAILED"
	emailStatusAuthFailed        = "AUTH_FAILED"
	emailStatusNeedsManualVerify = "NEEDS_MANUAL_VERIFICATION"

	emailAuthStatusAuthorized        = "AUTHORIZED"
	emailAuthStatusOAuthPending      = "OAUTH_PENDING"
	emailAuthStatusAuthFailed        = "AUTH_FAILED"
	emailAuthStatusNeedsManualVerify = "NEEDS_MANUAL_VERIFICATION"

	stepRegisterAccount             = "register_account"
	stepEnsureLogon                 = "ensure_logon"
	stepGoPayAppLogin               = "gopay_app_login"
	stepGoPayAppChangePhone         = "gopay_app_change_phone"
	stepGoPayAppChangePhoneStart    = "gopay_app_change_phone_start"
	stepGoPayAppChangePhoneSMSWait  = "gopay_app_change_phone_sms_wait"
	stepGoPayAppChangePhoneRetry    = "gopay_app_change_phone_retry"
	stepGoPayAppChangePhoneCancel   = "gopay_app_change_phone_cancel"
	stepGoPayAppChangePhoneComplete = "gopay_app_change_phone_complete"
	stepGoPayAppDeactivate          = "gopay_app_deactivate"
	stepGoPayAppDeactivateStart     = "gopay_app_deactivate_start"
	stepGoPayAppDeactivateSMSWait   = "gopay_app_deactivate_sms_wait"
	stepGoPayAppDeactivateSMSFinish = "gopay_app_deactivate_sms_finish"
	stepGoPayAppDeactivateComplete  = "gopay_app_deactivate_complete"
	stepGoPayAppSignup              = "gopay_app_signup"
	stepGoPayAppCreatePin           = "gopay_app_create_pin"
	stepGoPayPayment                = "gopay_payment"
	stepProbePlusTrial              = "probe_plus_trial"
	stepProbeTier                   = "probe_tier"
	stepLoginSession                = "login_session"
	stepRegisterMailbox             = "register_mailbox"
	stepMailboxOAuth                = "mailbox_oauth"

	registrationOTPParam            = "registration_otp"
	registrationOTPSubmittedAtParam = "registration_otp_submitted_at_unix"
	paymentOTPParam                 = "payment_otp"
	paymentOTPSubmittedAtParam      = "payment_otp_submitted_at_unix"
	goPayLocalSource                = "local"
	goPayAppStateKey                = goPayLocalSource
)
