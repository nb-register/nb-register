package app

import (
	"errors"
	"fmt"
	"log"

	temporalclient "go.temporal.io/sdk/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/gorm"

	"orchestrator/db"
	"orchestrator/internal/jobprojection"
	"orchestrator/pb"
)

type orchestratorDependencies struct {
	db       *gorm.DB
	jobStore *jobprojection.Store
	temporal temporalclient.Client

	accountClient         pb.AccountDatabaseServiceClient
	browserClient         pb.BrowserRegistrationClient
	paymentClient         pb.PaymentServiceClient
	gopayClient           pb.GopayAppServiceClient
	codeReceiverClient    pb.CodeReceiverServiceClient
	emailClient           pb.EmailServiceClient
	mailboxRegisterClient pb.MailboxRegistrationServiceClient

	closers []func() error
}

func newOrchestratorDependencies(cfg orchestratorConfig) (*orchestratorDependencies, error) {
	deps := &orchestratorDependencies{}

	browserConn, err := newGRPCClientConn("browser service", cfg.BrowserAddr)
	if err != nil {
		return nil, err
	}
	deps.addCloser(browserConn.Close)

	paymentConn, err := newGRPCClientConn("payment service", cfg.PaymentAddr)
	if err != nil {
		deps.Close()
		return nil, err
	}
	deps.addCloser(paymentConn.Close)

	gopayConn, err := newGRPCClientConn("gopay-app service", cfg.GoPayAppAddr)
	if err != nil {
		deps.Close()
		return nil, err
	}
	deps.addCloser(gopayConn.Close)

	codeReceiverConn, err := newGRPCClientConn("code receiver service", cfg.CodeReceiverAddr)
	if err != nil {
		deps.Close()
		return nil, err
	}
	deps.addCloser(codeReceiverConn.Close)

	accountDBConn, err := newGRPCClientConn("account-db service", cfg.AccountDBAddr)
	if err != nil {
		deps.Close()
		return nil, err
	}
	deps.addCloser(accountDBConn.Close)

	emailConn, err := newGRPCClientConn("email service", cfg.EmailAddr)
	if err != nil {
		deps.Close()
		return nil, err
	}
	deps.addCloser(emailConn.Close)

	mailboxRegisterConn, err := newGRPCClientConn("mailbox registration service", cfg.MailboxRegisterAddr)
	if err != nil {
		deps.Close()
		return nil, err
	}
	deps.addCloser(mailboxRegisterConn.Close)

	temporalClient, closeTemporal, err := newTemporalClient(cfg)
	if err != nil {
		deps.Close()
		return nil, fmt.Errorf("connect to Temporal: %w", err)
	}
	deps.temporal = temporalClient
	deps.addCloser(func() error {
		closeTemporal()
		return nil
	})

	database := db.InitDB()
	deps.db = database
	deps.jobStore = jobprojection.NewStore(database)
	deps.accountClient = pb.NewAccountDatabaseServiceClient(accountDBConn)
	deps.browserClient = pb.NewBrowserRegistrationClient(browserConn)
	deps.paymentClient = pb.NewPaymentServiceClient(paymentConn)
	deps.gopayClient = pb.NewGopayAppServiceClient(gopayConn)
	deps.codeReceiverClient = pb.NewCodeReceiverServiceClient(codeReceiverConn)
	deps.emailClient = pb.NewEmailServiceClient(emailConn)
	deps.mailboxRegisterClient = pb.NewMailboxRegistrationServiceClient(mailboxRegisterConn)

	return deps, nil
}

func newGRPCClientConn(name string, addr string) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", name, err)
	}
	return conn, nil
}

func (d *orchestratorDependencies) addCloser(closeFn func() error) {
	d.closers = append(d.closers, closeFn)
}

func (d *orchestratorDependencies) Close() {
	var closeErr error
	for i := len(d.closers) - 1; i >= 0; i-- {
		if err := d.closers[i](); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}
	if closeErr != nil {
		log.Printf("Orchestrator dependency close failed: %v", closeErr)
	}
}
