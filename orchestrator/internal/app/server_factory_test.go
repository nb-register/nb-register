package app

import (
	"testing"
	"time"
)

func TestNewOrchestratorServerInjectsConfigAndDependencies(t *testing.T) {
	cfg := orchestratorConfig{
		GoPayOTPServiceAddr:               "otp:50051",
		GoPayOTPTimeout:                   70,
		RegistrationOTPWait:               190,
		GoPayAppStepBodyLimit:             7000,
		GoPayAppLinkPaymentTimeout:        181 * time.Second,
		GoPayAppUnlinkTimeout:             16 * time.Second,
		OutlookRegisterEnableOAuth2:       true,
		ChangePhoneMaxFailures:            5,
		ChangePhoneDisabled:               true,
		ChangePhoneOTPWaitSeconds:         130,
		ChangePhoneOTPRetryAttempts:       3,
		ChangePhoneGetNumberRetryDelay:    7 * time.Second,
		ChangePhoneSMSCancelTimeout:       140 * time.Second,
		ChangePhoneSMSCancelRetryInterval: 12 * time.Second,
		TemporalTaskQueue:                 "custom-task-queue",
	}
	deps := &orchestratorDependencies{}

	server := newOrchestratorServer(cfg, deps)

	if server.otpAddr != cfg.GoPayOTPServiceAddr {
		t.Fatalf("otpAddr = %q", server.otpAddr)
	}
	if server.otpTimeout != cfg.GoPayOTPTimeout || server.regOTPTimeout != cfg.RegistrationOTPWait {
		t.Fatalf("otp timeouts = %d/%d", server.otpTimeout, server.regOTPTimeout)
	}
	if server.gopayAppStepBodyLimit != cfg.GoPayAppStepBodyLimit ||
		server.gopayAppLinkPaymentTimeout != cfg.GoPayAppLinkPaymentTimeout ||
		server.gopayAppUnlinkTimeout != cfg.GoPayAppUnlinkTimeout ||
		server.outlookRegisterEnableOAuth2 != cfg.OutlookRegisterEnableOAuth2 {
		t.Fatalf("runtime config mismatch: %+v", server)
	}
	if server.changePhoneMaxFailures != cfg.ChangePhoneMaxFailures ||
		server.changePhoneDisabled != cfg.ChangePhoneDisabled ||
		server.changePhoneOTPWaitSeconds != cfg.ChangePhoneOTPWaitSeconds ||
		server.changePhoneOTPRetryAttempts != cfg.ChangePhoneOTPRetryAttempts ||
		server.changePhoneGetNumberRetryDelay != cfg.ChangePhoneGetNumberRetryDelay ||
		server.changePhoneSMSCancelTimeout != cfg.ChangePhoneSMSCancelTimeout ||
		server.changePhoneSMSCancelRetryInterval != cfg.ChangePhoneSMSCancelRetryInterval {
		t.Fatalf("change phone config mismatch: %+v", server)
	}
	if server.taskQueue != cfg.TemporalTaskQueue {
		t.Fatalf("taskQueue = %q", server.taskQueue)
	}
}
