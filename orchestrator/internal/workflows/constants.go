package workflows

import (
	"orchestrator/internal/contracts"
	"orchestrator/internal/jobstatus"
)

const (
	actionRegister            = contracts.ActionRegister
	actionActivate            = contracts.ActionActivate
	actionAutopay             = contracts.ActionAutopay
	actionGoPayApp            = contracts.ActionGoPayApp
	actionGoPayPayment        = contracts.ActionGoPayPayment
	actionGoPayPaymentRebind  = contracts.ActionGoPayPaymentRebind
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
	stepRegisterAccountStart        = "register_account_start"
	stepRegisterAccountBrowser      = "register_account_browser"
	stepRegisterAccountOTPRequest   = "register_account_otp_request"
	stepRegisterAccountOTPWait      = "register_account_otp_wait"
	stepRegisterAccountComplete     = "register_account_complete"
	stepEnsureLogon                 = "ensure_logon"
	stepGoPayAppLogin               = "gopay_app_ensure_token_available"
	stepGoPayAppChangePhone         = "gopay_app_change_phone"
	stepGoPayAppChangePhoneStart    = "gopay_app_change_phone_start"
	stepGoPayAppChangePhoneSMSWait  = "gopay_app_change_phone_sms_wait"
	stepGoPayAppChangePhoneRetry    = "gopay_app_change_phone_retry"
	stepGoPayAppChangePhoneCancel   = "gopay_app_change_phone_cancel"
	stepGoPayAppChangePhoneComplete = "gopay_app_change_phone_complete"
	stepGoPayAppSignupPhone         = "gopay_app_signup_phone"
	stepGoPayAppWAPhoneCheck        = "gopay_app_wa_phone_check"
	stepGoPayAppDeactivate          = "gopay_app_deactivate"
	stepGoPayAppDeactivateStart     = "gopay_app_deactivate_start"
	stepGoPayAppDeactivateSMSWait   = "gopay_app_deactivate_sms_wait"
	stepGoPayAppDeactivateSMSFinish = "gopay_app_deactivate_sms_finish"
	stepGoPayAppDeactivateComplete  = "gopay_app_deactivate_complete"
	stepGoPayAppSignup              = "gopay_app_signup"
	stepGoPayAppSignupRetry         = "gopay_app_signup_retry"
	stepGoPayAppSignupPhoneCancel   = "gopay_app_signup_phone_cancel"
	stepGoPayAppStatus              = "gopay_app_status"
	stepGoPayAppCreatePin           = "gopay_app_ensure_pin_settled"
	stepGoPayAppAddBalance          = "gopay_app_add_balance"
	stepGoPayAppAddBalanceConfirm   = "gopay_app_add_balance_confirm"
	stepGoPayAppSMSFinish           = "gopay_app_sms_finish"
	stepGoPayAppSMSRequestMore      = "gopay_app_sms_request_more"
	stepGoPayPaymentPrepare         = "gopay_payment_prepare"
	stepGoPayPayment                = "gopay_payment"
	stepProbePlusTrial              = "probe_plus_trial"
	stepProbeTier                   = "probe_tier"
	stepLoginSession                = "login_session"
	stepLoginSessionStart           = "login_session_start"
	stepLoginSessionBrowser         = "login_session_browser"
	stepLoginSessionOTPRequest      = "login_session_otp_request"
	stepLoginSessionOTPWait         = "login_session_otp_wait"
	stepLoginSessionComplete        = "login_session_complete"
	stepRegisterMailbox             = "register_mailbox"
	stepMailboxOAuth                = "mailbox_oauth"

	registrationOTPParam            = "registration_otp"
	registrationOTPSubmittedAtParam = "registration_otp_submitted_at_unix"
	paymentOTPParam                 = "payment_otp"
	paymentOTPSubmittedAtParam      = "payment_otp_submitted_at_unix"
	goPayLocalSource                = "local"
	goPayAppStateKey                = goPayLocalSource

	goPayAppOTPOperationAuth   = "auth"
	goPayAppOTPOperationSignup = "signup"

	goPayAppSignupMaxPhoneAttempts = 3
)
