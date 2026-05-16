package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	pb "orchestrator/pb"
)

const (
	defaultChangePhoneMaxFailures            = 3
	defaultChangePhoneOTPWaitSeconds         = int32(120)
	defaultChangePhoneOTPRetryAttempts       = 1
	defaultChangePhoneGetNumberRetryDelay    = 5 * time.Second
	defaultChangePhoneSMSCancelTimeout       = 130 * time.Second
	defaultChangePhoneSMSCancelRetryInterval = 10 * time.Second
	gopayBalanceWaitTimeout                  = 2 * time.Minute
	gopayBalancePollInterval                 = 5 * time.Second
)

func (s *orchestratorServer) EnsureLogonActivity(ctx context.Context, input *pb.EnsureLogonRequest) (*pb.EnsureLogonResponse, error) {
	output := &pb.EnsureLogonResponse{}
	if input == nil {
		err := fmt.Errorf("ensure logon input is required")
		output.ErrorMessage = err.Error()
		return output, err
	}
	step := s.activityStep(ctx, input.GetJobId(), stepEnsureLogon, false, true)
	step.progress("starting ensure logon", map[string]any{
		"account_id": input.GetAccountId(),
	})

	account, err := s.getAccount(ctx, input.GetAccountId())
	if err != nil {
		output.ErrorMessage = err.Error()
		return output, err
	}
	if err := accountEligibleForActivation(account); err != nil {
		output.ErrorMessage = err.Error()
		return output, err
	}

	_, err = step.run(func() (any, error) {
		statusResp, statusErr := s.goPayStatus(ctx)
		output.StatusBefore = goPayStatusSnapshot(statusResp, statusErr)
		if statusErr != nil {
			output.ErrorMessage = statusErr.Error()
			return ensureLogonData(output), statusErr
		}

		tokenResp, err := s.validateGoPayAccountToken(ctx)
		if err != nil {
			err = fmt.Errorf("validate gopay account token: %w", err)
			output.ErrorMessage = err.Error()
			return ensureLogonData(output), err
		}
		if !tokenResp.GetTokenValid() {
			message := strings.TrimSpace(tokenResp.GetErrorMessage())
			if message == "" {
				message = "token invalid"
			}
			err := fmt.Errorf("gopay account token is not ready: %s", message)
			output.ErrorMessage = err.Error()
			return ensureLogonData(output), err
		}

		output.AlreadyReady = true
		output.AccountTokenReady = true
		return s.finishEnsureLogon(ctx, output)
	})
	if err != nil {
		if output.ErrorMessage == "" {
			output.ErrorMessage = err.Error()
		}
		return output, err
	}
	return output, nil
}
