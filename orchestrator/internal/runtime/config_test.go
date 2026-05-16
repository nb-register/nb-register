package runtime

import (
	"testing"
	"time"
)

func TestLoadOrchestratorConfigDefaults(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "")
	t.Setenv("BROWSER_ADDR", "")
	t.Setenv("PAYMENT_ADDR", "")
	t.Setenv("GOPAY_APP_ADDR", "")
	t.Setenv("CODE_RECEIVER_ADDR", "")
	t.Setenv("ACCOUNT_DB_ADDR", "")
	t.Setenv("EMAIL_ADDR", "")
	t.Setenv("MAILBOX_REGISTER_ADDR", "")
	t.Setenv("OTP_ADDR", "")
	t.Setenv("GOPAY_OTP_SERVICE_ADDR", "")
	t.Setenv("GOPAY_OTP_TIMEOUT_SECONDS", "")
	t.Setenv("REGISTRATION_OTP_TIMEOUT_SECONDS", "")
	t.Setenv("GOPAY_APP_STEP_BODY_LIMIT", "")
	t.Setenv("GOPAY_APP_LINK_PAYMENT_TIMEOUT_SECONDS", "")
	t.Setenv("GOPAY_APP_UNLINK_TIMEOUT_SECONDS", "")
	t.Setenv("OUTLOOK_REGISTER_ENABLE_OAUTH2", "")
	t.Setenv("GOPAY_CHANGE_PHONE_DISABLED", "")
	t.Setenv("GOPAY_CHANGE_PHONE_MAX_FAILURES", "")
	t.Setenv("GOPAY_CHANGE_PHONE_OTP_WAIT_SECONDS", "")
	t.Setenv("GOPAY_CHANGE_PHONE_OTP_RETRY_ATTEMPTS", "")
	t.Setenv("GOPAY_CHANGE_PHONE_GET_NUMBER_RETRY_SECONDS", "")
	t.Setenv("GOPAY_CHANGE_PHONE_SMS_CANCEL_TIMEOUT_SECONDS", "")
	t.Setenv("GOPAY_CHANGE_PHONE_SMS_CANCEL_RETRY_SECONDS", "")
	t.Setenv("TEMPORAL_ADDR", "")
	t.Setenv("TEMPORAL_NAMESPACE", "")
	t.Setenv("TEMPORAL_TASK_QUEUE", "")
	t.Setenv("TEMPORAL_DEV_SERVER", "")
	t.Setenv("TEMPORAL_DEV_SERVER_VERSION", "")
	t.Setenv("TEMPORAL_DEV_SERVER_CACHE_DIR", "")
	t.Setenv("TEMPORAL_DEV_SERVER_DB", "")
	t.Setenv("TEMPORAL_DEV_SERVER_UI", "")
	t.Setenv("TEMPORAL_DEV_SERVER_UI_PORT", "")
	t.Setenv("TEMPORAL_DEV_SERVER_LOG_LEVEL", "")

	cfg := loadOrchestratorConfig()

	if cfg.ListenAddr != ":50051" {
		t.Fatalf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.BrowserAddr != "browser-reg:50051" {
		t.Fatalf("BrowserAddr = %q", cfg.BrowserAddr)
	}
	if cfg.PaymentAddr != "host.docker.internal:50051" {
		t.Fatalf("PaymentAddr = %q", cfg.PaymentAddr)
	}
	if cfg.GoPayAppAddr != "gopay-app:50051" {
		t.Fatalf("GoPayAppAddr = %q", cfg.GoPayAppAddr)
	}
	if cfg.CodeReceiverAddr != "herosms-sms-service:50051" {
		t.Fatalf("CodeReceiverAddr = %q", cfg.CodeReceiverAddr)
	}
	if cfg.GoPayOTPServiceAddr != "whatsapp-otp-relay:50051" {
		t.Fatalf("GoPayOTPServiceAddr = %q", cfg.GoPayOTPServiceAddr)
	}
	if cfg.GoPayOTPTimeout != 60 {
		t.Fatalf("GoPayOTPTimeout = %d", cfg.GoPayOTPTimeout)
	}
	if cfg.RegistrationOTPWait != 180 {
		t.Fatalf("RegistrationOTPWait = %d", cfg.RegistrationOTPWait)
	}
	if cfg.GoPayAppStepBodyLimit != 6000 {
		t.Fatalf("GoPayAppStepBodyLimit = %d", cfg.GoPayAppStepBodyLimit)
	}
	if cfg.GoPayAppLinkPaymentTimeout != 180*time.Second {
		t.Fatalf("GoPayAppLinkPaymentTimeout = %s", cfg.GoPayAppLinkPaymentTimeout)
	}
	if cfg.GoPayAppUnlinkTimeout != 15*time.Second {
		t.Fatalf("GoPayAppUnlinkTimeout = %s", cfg.GoPayAppUnlinkTimeout)
	}
	if !cfg.OutlookRegisterEnableOAuth2 {
		t.Fatalf("OutlookRegisterEnableOAuth2 = false")
	}
	if cfg.ChangePhoneDisabled {
		t.Fatalf("ChangePhoneDisabled = true")
	}
	if cfg.ChangePhoneMaxFailures != defaultChangePhoneMaxFailures {
		t.Fatalf("ChangePhoneMaxFailures = %d", cfg.ChangePhoneMaxFailures)
	}
	if cfg.ChangePhoneOTPWaitSeconds != defaultChangePhoneOTPWaitSeconds {
		t.Fatalf("ChangePhoneOTPWaitSeconds = %d", cfg.ChangePhoneOTPWaitSeconds)
	}
	if cfg.ChangePhoneOTPRetryAttempts != defaultChangePhoneOTPRetryAttempts {
		t.Fatalf("ChangePhoneOTPRetryAttempts = %d", cfg.ChangePhoneOTPRetryAttempts)
	}
	if cfg.ChangePhoneGetNumberRetryDelay != defaultChangePhoneGetNumberRetryDelay {
		t.Fatalf("ChangePhoneGetNumberRetryDelay = %s", cfg.ChangePhoneGetNumberRetryDelay)
	}
	if cfg.ChangePhoneSMSCancelTimeout != defaultChangePhoneSMSCancelTimeout {
		t.Fatalf("ChangePhoneSMSCancelTimeout = %s", cfg.ChangePhoneSMSCancelTimeout)
	}
	if cfg.ChangePhoneSMSCancelRetryInterval != defaultChangePhoneSMSCancelRetryInterval {
		t.Fatalf("ChangePhoneSMSCancelRetryInterval = %s", cfg.ChangePhoneSMSCancelRetryInterval)
	}
	if cfg.TemporalAddr != "host.docker.internal:7233" {
		t.Fatalf("TemporalAddr = %q", cfg.TemporalAddr)
	}
	if cfg.TemporalNamespace != "default" {
		t.Fatalf("TemporalNamespace = %q", cfg.TemporalNamespace)
	}
	if cfg.TemporalTaskQueue != taskQueueDefault {
		t.Fatalf("TemporalTaskQueue = %q", cfg.TemporalTaskQueue)
	}
	if cfg.TemporalDevServer {
		t.Fatalf("TemporalDevServer = true")
	}
	if cfg.TemporalDevServerVersion != "default" {
		t.Fatalf("TemporalDevServerVersion = %q", cfg.TemporalDevServerVersion)
	}
	if cfg.TemporalDevServerLog != "warn" {
		t.Fatalf("TemporalDevServerLog = %q", cfg.TemporalDevServerLog)
	}
}

func TestLoadOrchestratorConfigOverrides(t *testing.T) {
	t.Setenv("LISTEN_ADDR", ":6000")
	t.Setenv("BROWSER_ADDR", "browser:1")
	t.Setenv("PAYMENT_ADDR", "payment:2")
	t.Setenv("GOPAY_APP_ADDR", "gopay:3")
	t.Setenv("CODE_RECEIVER_ADDR", "code:4")
	t.Setenv("ACCOUNT_DB_ADDR", "account:5")
	t.Setenv("EMAIL_ADDR", "email:6")
	t.Setenv("MAILBOX_REGISTER_ADDR", "mailbox:7")
	t.Setenv("GOPAY_OTP_SERVICE_ADDR", "otp:8")
	t.Setenv("GOPAY_OTP_TIMEOUT_SECONDS", "61")
	t.Setenv("REGISTRATION_OTP_TIMEOUT_SECONDS", "181")
	t.Setenv("GOPAY_APP_STEP_BODY_LIMIT", "7000")
	t.Setenv("GOPAY_APP_LINK_PAYMENT_TIMEOUT_SECONDS", "181")
	t.Setenv("GOPAY_APP_UNLINK_TIMEOUT_SECONDS", "16")
	t.Setenv("OUTLOOK_REGISTER_ENABLE_OAUTH2", "false")
	t.Setenv("GOPAY_CHANGE_PHONE_DISABLED", "yes")
	t.Setenv("GOPAY_CHANGE_PHONE_MAX_FAILURES", "4")
	t.Setenv("GOPAY_CHANGE_PHONE_OTP_WAIT_SECONDS", "121")
	t.Setenv("GOPAY_CHANGE_PHONE_OTP_RETRY_ATTEMPTS", "2")
	t.Setenv("GOPAY_CHANGE_PHONE_GET_NUMBER_RETRY_SECONDS", "6")
	t.Setenv("GOPAY_CHANGE_PHONE_SMS_CANCEL_TIMEOUT_SECONDS", "131")
	t.Setenv("GOPAY_CHANGE_PHONE_SMS_CANCEL_RETRY_SECONDS", "11")
	t.Setenv("TEMPORAL_ADDR", "temporal:7233")
	t.Setenv("TEMPORAL_NAMESPACE", "ns")
	t.Setenv("TEMPORAL_TASK_QUEUE", "queue")
	t.Setenv("TEMPORAL_DEV_SERVER", "true")
	t.Setenv("TEMPORAL_DEV_SERVER_VERSION", "1.2.3")
	t.Setenv("TEMPORAL_DEV_SERVER_CACHE_DIR", "/tmp/temporal-cache")
	t.Setenv("TEMPORAL_DEV_SERVER_DB", "/tmp/temporal.db")
	t.Setenv("TEMPORAL_DEV_SERVER_UI", "on")
	t.Setenv("TEMPORAL_DEV_SERVER_UI_PORT", "8233")
	t.Setenv("TEMPORAL_DEV_SERVER_LOG_LEVEL", "debug")

	cfg := loadOrchestratorConfig()

	if cfg.ListenAddr != ":6000" {
		t.Fatalf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.BrowserAddr != "browser:1" ||
		cfg.PaymentAddr != "payment:2" ||
		cfg.GoPayAppAddr != "gopay:3" ||
		cfg.CodeReceiverAddr != "code:4" ||
		cfg.AccountDBAddr != "account:5" ||
		cfg.EmailAddr != "email:6" ||
		cfg.MailboxRegisterAddr != "mailbox:7" {
		t.Fatalf("service addrs not overridden: %+v", cfg)
	}
	if cfg.GoPayOTPServiceAddr != "otp:8" {
		t.Fatalf("GoPayOTPServiceAddr = %q", cfg.GoPayOTPServiceAddr)
	}
	if cfg.GoPayOTPTimeout != 61 || cfg.RegistrationOTPWait != 181 {
		t.Fatalf("otp timeouts = %d/%d", cfg.GoPayOTPTimeout, cfg.RegistrationOTPWait)
	}
	if cfg.GoPayAppStepBodyLimit != 7000 {
		t.Fatalf("GoPayAppStepBodyLimit = %d", cfg.GoPayAppStepBodyLimit)
	}
	if cfg.GoPayAppLinkPaymentTimeout != 181*time.Second {
		t.Fatalf("GoPayAppLinkPaymentTimeout = %s", cfg.GoPayAppLinkPaymentTimeout)
	}
	if cfg.GoPayAppUnlinkTimeout != 16*time.Second {
		t.Fatalf("GoPayAppUnlinkTimeout = %s", cfg.GoPayAppUnlinkTimeout)
	}
	if cfg.OutlookRegisterEnableOAuth2 {
		t.Fatalf("OutlookRegisterEnableOAuth2 = true")
	}
	if !cfg.ChangePhoneDisabled {
		t.Fatalf("ChangePhoneDisabled = false")
	}
	if cfg.ChangePhoneMaxFailures != 4 ||
		cfg.ChangePhoneOTPWaitSeconds != 121 ||
		cfg.ChangePhoneOTPRetryAttempts != 2 {
		t.Fatalf("change phone scalar config not overridden: %+v", cfg)
	}
	if cfg.ChangePhoneGetNumberRetryDelay != 6*time.Second ||
		cfg.ChangePhoneSMSCancelTimeout != 131*time.Second ||
		cfg.ChangePhoneSMSCancelRetryInterval != 11*time.Second {
		t.Fatalf("change phone duration config not overridden: %+v", cfg)
	}
	if cfg.TemporalAddr != "temporal:7233" ||
		cfg.TemporalNamespace != "ns" ||
		cfg.TemporalTaskQueue != "queue" {
		t.Fatalf("temporal addr config not overridden: %+v", cfg)
	}
	if !cfg.TemporalDevServer ||
		cfg.TemporalDevServerVersion != "1.2.3" ||
		cfg.TemporalDevServerCache != "/tmp/temporal-cache" ||
		cfg.TemporalDevServerDB != "/tmp/temporal.db" ||
		!cfg.TemporalDevServerUI ||
		cfg.TemporalDevServerUIPort != "8233" ||
		cfg.TemporalDevServerLog != "debug" {
		t.Fatalf("temporal dev config not overridden: %+v", cfg)
	}
}

func TestLoadOrchestratorConfigOTPServiceFallback(t *testing.T) {
	t.Setenv("OTP_ADDR", "legacy-otp:50051")
	t.Setenv("GOPAY_OTP_SERVICE_ADDR", "")

	cfg := loadOrchestratorConfig()
	if cfg.GoPayOTPServiceAddr != "legacy-otp:50051" {
		t.Fatalf("GoPayOTPServiceAddr = %q", cfg.GoPayOTPServiceAddr)
	}

	t.Setenv("GOPAY_OTP_SERVICE_ADDR", "gopay-otp:50051")
	cfg = loadOrchestratorConfig()
	if cfg.GoPayOTPServiceAddr != "gopay-otp:50051" {
		t.Fatalf("GoPayOTPServiceAddr = %q", cfg.GoPayOTPServiceAddr)
	}
}
