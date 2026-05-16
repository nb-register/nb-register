package app

import "time"

type orchestratorConfig struct {
	ListenAddr string

	BrowserAddr         string
	PaymentAddr         string
	GoPayAppAddr        string
	CodeReceiverAddr    string
	AccountDBAddr       string
	EmailAddr           string
	MailboxRegisterAddr string

	GoPayOTPServiceAddr         string
	GoPayOTPTimeout             int32
	RegistrationOTPWait         int32
	GoPayAppStepBodyLimit       int32
	GoPayAppLinkPaymentTimeout  time.Duration
	GoPayAppUnlinkTimeout       time.Duration
	OutlookRegisterEnableOAuth2 bool

	ChangePhoneMaxFailures            int
	ChangePhoneDisabled               bool
	ChangePhoneOTPWaitSeconds         int32
	ChangePhoneOTPRetryAttempts       int
	ChangePhoneGetNumberRetryDelay    time.Duration
	ChangePhoneSMSCancelTimeout       time.Duration
	ChangePhoneSMSCancelRetryInterval time.Duration

	TemporalAddr             string
	TemporalNamespace        string
	TemporalTaskQueue        string
	TemporalDevServer        bool
	TemporalDevServerVersion string
	TemporalDevServerCache   string
	TemporalDevServerDB      string
	TemporalDevServerUI      bool
	TemporalDevServerUIPort  string
	TemporalDevServerLog     string
}

func loadOrchestratorConfig() orchestratorConfig {
	otpServiceAddr := envDefault("GOPAY_OTP_SERVICE_ADDR", envDefault("OTP_ADDR", "whatsapp-otp-relay:50051"))

	return orchestratorConfig{
		ListenAddr: envDefault("LISTEN_ADDR", ":50051"),

		BrowserAddr:         envDefault("BROWSER_ADDR", "browser-reg:50051"),
		PaymentAddr:         envDefault("PAYMENT_ADDR", "host.docker.internal:50051"),
		GoPayAppAddr:        envDefault("GOPAY_APP_ADDR", "gopay-app:50051"),
		CodeReceiverAddr:    envDefault("CODE_RECEIVER_ADDR", "herosms-sms-service:50051"),
		AccountDBAddr:       envDefault("ACCOUNT_DB_ADDR", "account-db:50051"),
		EmailAddr:           envDefault("EMAIL_ADDR", "outlook-imap-service:50051"),
		MailboxRegisterAddr: envDefault("MAILBOX_REGISTER_ADDR", "outlook-register-service:50051"),

		GoPayOTPServiceAddr:         otpServiceAddr,
		GoPayOTPTimeout:             envInt32("GOPAY_OTP_TIMEOUT_SECONDS", 60),
		RegistrationOTPWait:         envInt32("REGISTRATION_OTP_TIMEOUT_SECONDS", 180),
		GoPayAppStepBodyLimit:       int32(envInt("GOPAY_APP_STEP_BODY_LIMIT", 6000)),
		GoPayAppLinkPaymentTimeout:  envPositiveDurationSeconds("GOPAY_APP_LINK_PAYMENT_TIMEOUT_SECONDS", 180*time.Second),
		GoPayAppUnlinkTimeout:       envPositiveDurationSeconds("GOPAY_APP_UNLINK_TIMEOUT_SECONDS", 15*time.Second),
		OutlookRegisterEnableOAuth2: envBool("OUTLOOK_REGISTER_ENABLE_OAUTH2", true),

		ChangePhoneMaxFailures:            envInt("GOPAY_CHANGE_PHONE_MAX_FAILURES", defaultChangePhoneMaxFailures),
		ChangePhoneDisabled:               envBool("GOPAY_CHANGE_PHONE_DISABLED", false),
		ChangePhoneOTPWaitSeconds:         envInt32("GOPAY_CHANGE_PHONE_OTP_WAIT_SECONDS", defaultChangePhoneOTPWaitSeconds),
		ChangePhoneOTPRetryAttempts:       envIntNonNegative("GOPAY_CHANGE_PHONE_OTP_RETRY_ATTEMPTS", defaultChangePhoneOTPRetryAttempts),
		ChangePhoneGetNumberRetryDelay:    envNonNegativeDurationSeconds("GOPAY_CHANGE_PHONE_GET_NUMBER_RETRY_SECONDS", defaultChangePhoneGetNumberRetryDelay),
		ChangePhoneSMSCancelTimeout:       envPositiveDurationSeconds("GOPAY_CHANGE_PHONE_SMS_CANCEL_TIMEOUT_SECONDS", defaultChangePhoneSMSCancelTimeout),
		ChangePhoneSMSCancelRetryInterval: envPositiveDurationSeconds("GOPAY_CHANGE_PHONE_SMS_CANCEL_RETRY_SECONDS", defaultChangePhoneSMSCancelRetryInterval),

		TemporalAddr:             envDefault("TEMPORAL_ADDR", "host.docker.internal:7233"),
		TemporalNamespace:        envDefault("TEMPORAL_NAMESPACE", "default"),
		TemporalTaskQueue:        envDefault("TEMPORAL_TASK_QUEUE", taskQueueDefault),
		TemporalDevServer:        envBool("TEMPORAL_DEV_SERVER", false),
		TemporalDevServerVersion: envDefault("TEMPORAL_DEV_SERVER_VERSION", "default"),
		TemporalDevServerCache:   envDefault("TEMPORAL_DEV_SERVER_CACHE_DIR", ""),
		TemporalDevServerDB:      envDefault("TEMPORAL_DEV_SERVER_DB", ""),
		TemporalDevServerUI:      envBool("TEMPORAL_DEV_SERVER_UI", false),
		TemporalDevServerUIPort:  envDefault("TEMPORAL_DEV_SERVER_UI_PORT", ""),
		TemporalDevServerLog:     envDefault("TEMPORAL_DEV_SERVER_LOG_LEVEL", "warn"),
	}
}
