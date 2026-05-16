package app

import (
	"time"

	temporalclient "go.temporal.io/sdk/client"
	"gorm.io/gorm"
	"orchestrator/internal/jobprojection"
	"orchestrator/pb"
)

type orchestratorServer struct {
	pb.UnimplementedOrchestratorServiceServer
	db                                *gorm.DB
	jobStore                          *jobprojection.Store
	accountClient                     pb.AccountDatabaseServiceClient
	browserClient                     pb.BrowserRegistrationClient
	paymentClient                     pb.PaymentServiceClient
	gopayClient                       pb.GopayAppServiceClient
	codeReceiverClient                pb.CodeReceiverServiceClient
	emailClient                       pb.EmailServiceClient
	mailboxRegisterClient             pb.MailboxRegistrationServiceClient
	otpAddr                           string
	otpTimeout                        int32
	regOTPTimeout                     int32
	gopayAppStepBodyLimit             int32
	gopayAppLinkPaymentTimeout        time.Duration
	gopayAppUnlinkTimeout             time.Duration
	outlookRegisterEnableOAuth2       bool
	changePhoneMaxFailures            int
	changePhoneDisabled               bool
	changePhoneOTPWaitSeconds         int32
	changePhoneOTPRetryAttempts       int
	changePhoneGetNumberRetryDelay    time.Duration
	changePhoneSMSCancelTimeout       time.Duration
	changePhoneSMSCancelRetryInterval time.Duration
	temporal                          temporalclient.Client
	taskQueue                         string
}
