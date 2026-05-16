package app

func newOrchestratorServer(cfg orchestratorConfig, deps *orchestratorDependencies) *orchestratorServer {
	return &orchestratorServer{
		db:                                deps.db,
		jobStore:                          deps.jobStore,
		accountClient:                     deps.accountClient,
		browserClient:                     deps.browserClient,
		paymentClient:                     deps.paymentClient,
		gopayClient:                       deps.gopayClient,
		codeReceiverClient:                deps.codeReceiverClient,
		emailClient:                       deps.emailClient,
		mailboxRegisterClient:             deps.mailboxRegisterClient,
		otpAddr:                           cfg.GoPayOTPServiceAddr,
		otpTimeout:                        cfg.GoPayOTPTimeout,
		regOTPTimeout:                     cfg.RegistrationOTPWait,
		gopayAppStepBodyLimit:             cfg.GoPayAppStepBodyLimit,
		gopayAppLinkPaymentTimeout:        cfg.GoPayAppLinkPaymentTimeout,
		gopayAppUnlinkTimeout:             cfg.GoPayAppUnlinkTimeout,
		outlookRegisterEnableOAuth2:       cfg.OutlookRegisterEnableOAuth2,
		changePhoneMaxFailures:            cfg.ChangePhoneMaxFailures,
		changePhoneDisabled:               cfg.ChangePhoneDisabled,
		changePhoneOTPWaitSeconds:         cfg.ChangePhoneOTPWaitSeconds,
		changePhoneOTPRetryAttempts:       cfg.ChangePhoneOTPRetryAttempts,
		changePhoneGetNumberRetryDelay:    cfg.ChangePhoneGetNumberRetryDelay,
		changePhoneSMSCancelTimeout:       cfg.ChangePhoneSMSCancelTimeout,
		changePhoneSMSCancelRetryInterval: cfg.ChangePhoneSMSCancelRetryInterval,
		temporal:                          deps.temporal,
		taskQueue:                         cfg.TemporalTaskQueue,
	}
}
