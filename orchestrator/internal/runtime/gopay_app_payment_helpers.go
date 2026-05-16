package runtime

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	pb "orchestrator/pb"
)

func (s *orchestratorServer) gopayAppStepResponseBodyLimit() int32 {
	if s.gopayAppStepBodyLimit <= 0 {
		return 6000
	}
	return s.gopayAppStepBodyLimit
}

func (s *orchestratorServer) gopayAppLinkPaymentWaitTimeout() time.Duration {
	if s.gopayAppLinkPaymentTimeout <= 0 {
		return 180 * time.Second
	}
	return s.gopayAppLinkPaymentTimeout
}

func (s *orchestratorServer) gopayAppUnlinkWaitTimeout() time.Duration {
	if s.gopayAppUnlinkTimeout <= 0 {
		return 15 * time.Second
	}
	return s.gopayAppUnlinkTimeout
}

func (s *orchestratorServer) readyGoPayAccountToken(ctx context.Context) (string, string, error) {
	if s.gopayClient == nil {
		return "", "", fmt.Errorf("gopay-app client not configured")
	}
	stateJSON, err := s.loadGoPayAppState(ctx)
	if err != nil {
		return "", "", err
	}
	resp, err := s.gopayClient.GetReadyAccountToken(ctx, &pb.GetReadyAccountTokenRequest{StateJson: stateJSON})
	if err == nil {
		err = s.saveGoPayAppState(ctx, resp.GetStateJson())
	}
	if err != nil {
		return "", "", fmt.Errorf("GetReadyAccountToken: %w", err)
	}
	if resp == nil {
		return "", "", fmt.Errorf("GetReadyAccountToken returned empty response")
	}
	if !resp.GetSuccess() {
		return "", "", fmt.Errorf("GetReadyAccountToken: %s", resp.GetErrorMessage())
	}
	token := strings.TrimSpace(resp.GetAccountToken())
	if token == "" {
		return "", "", fmt.Errorf("GetReadyAccountToken returned empty account token")
	}
	return token, strings.TrimSpace(resp.GetPhone()), nil
}

func paymentLinkFromGoPayResponse(resp *pb.GoPayResponse) (string, error) {
	if resp == nil {
		return "", fmt.Errorf("payment response is empty")
	}
	for _, value := range []string{resp.GetDeeplinkUrl(), resp.GetChargeRef()} {
		if link := strings.TrimSpace(value); link != "" {
			return link, nil
		}
	}
	return "", fmt.Errorf("midtrans payment link is missing")
}

func (s *orchestratorServer) replayGoPayPaymentLink(ctx context.Context, paymentResp *pb.GoPayResponse) (*pb.ReplayLinkPaymentResponse, error) {
	if s.gopayClient == nil {
		return nil, fmt.Errorf("gopay-app client not configured")
	}
	paymentLink, err := paymentLinkFromGoPayResponse(paymentResp)
	if err != nil {
		return nil, err
	}
	pin := configuredGoPayPIN()
	if pin == "" {
		return nil, fmt.Errorf("GOPAY_PIN is required")
	}
	stateJSON, err := s.loadGoPayAppState(ctx)
	if err != nil {
		return nil, err
	}
	timeout := s.gopayAppLinkPaymentWaitTimeout()
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := s.gopayClient.ReplayLinkPayment(reqCtx, &pb.ReplayLinkPaymentRequest{
		StateJson:   stateJSON,
		PaymentLink: paymentLink,
		Pin:         pin,
		BodyLimit:   s.gopayAppStepResponseBodyLimit(),
	})
	if err == nil {
		err = s.saveGoPayAppState(ctx, resp.GetStateJson())
	}
	if err != nil {
		return resp, fmt.Errorf("ReplayLinkPayment: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("ReplayLinkPayment returned empty response")
	}
	if !resp.GetSuccess() {
		for _, step := range resp.GetSteps() {
			log.Printf("[gopay-app] ReplayLinkPayment step label=%s status=%d error=%s response=%s",
				step.GetLabel(),
				step.GetStatusCode(),
				step.GetErrorMessage(),
				strings.TrimSpace(step.GetResponseText()),
			)
		}
		return resp, fmt.Errorf("ReplayLinkPayment: %s", resp.GetErrorMessage())
	}
	return resp, nil
}

func (s *orchestratorServer) unlinkGoPayAccountToken(ctx context.Context) error {
	if s.gopayClient == nil {
		return fmt.Errorf("gopay-app client not configured")
	}
	stateJSON, err := s.loadGoPayAppState(ctx)
	if err != nil {
		return err
	}
	timeout := s.gopayAppUnlinkWaitTimeout()
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := s.gopayClient.Unlink(reqCtx, &pb.UnlinkRequest{StateJson: stateJSON})
	if err == nil {
		err = s.saveGoPayAppState(ctx, resp.GetStateJson())
	}
	if err != nil {
		return fmt.Errorf("Unlink: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("Unlink returned empty response")
	}
	if !resp.GetSuccess() {
		return fmt.Errorf("Unlink: %s", resp.GetErrorMessage())
	}
	log.Printf("[gopay-app] Unlinked token-linked apps count=%d", resp.GetUnlinkedCount())
	return nil
}

func goPayStatusTokenReady(resp *pb.StatusResponse) bool {
	return resp != nil && resp.GetStage() == "ready" && resp.GetTokenPresent()
}

func (s *orchestratorServer) validateGoPayAccountToken(ctx context.Context) (*pb.CheckTokenValidResponse, error) {
	if s.gopayClient == nil {
		return nil, fmt.Errorf("gopay app client not configured")
	}
	stateJSON, err := s.loadGoPayAppState(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := s.gopayClient.CheckTokenValid(ctx, &pb.CheckTokenValidRequest{StateJson: stateJSON})
	if err == nil {
		err = s.saveGoPayAppState(ctx, resp.GetStateJson())
	}
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("empty response")
	}
	return resp, nil
}

func (s *orchestratorServer) waitForGoPayMinBalance(ctx context.Context, step activityStep) error {
	if s.gopayClient == nil {
		return fmt.Errorf("gopay app client not configured")
	}
	deadline := time.NewTimer(gopayBalanceWaitTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(gopayBalancePollInterval)
	defer ticker.Stop()

	var lastAmount int64
	lastCurrency := "IDR"
	var lastErr string
	for {
		step.progress("checking gopay balance", map[string]any{
			"last_amount":   lastAmount,
			"last_currency": lastCurrency,
			"last_error":    lastErr,
		})
		var resp *pb.CheckTokenValidResponse
		var err error
		stateJSON, stateErr := s.loadGoPayAppState(ctx)
		if stateErr != nil {
			lastErr = stateErr.Error()
			resp = nil
			err = stateErr
		} else {
			resp, err = s.gopayClient.CheckTokenValid(ctx, &pb.CheckTokenValidRequest{StateJson: stateJSON})
			if err == nil {
				err = s.saveGoPayAppState(ctx, resp.GetStateJson())
			}
		}
		if err != nil {
			lastErr = err.Error()
		} else if resp == nil {
			lastErr = "empty response"
		} else {
			lastAmount = resp.GetBalanceAmount()
			if strings.TrimSpace(resp.GetBalanceCurrency()) != "" {
				lastCurrency = resp.GetBalanceCurrency()
			}
			if !resp.GetTokenValid() {
				message := strings.TrimSpace(resp.GetErrorMessage())
				if message == "" {
					message = "token invalid"
				}
				return fmt.Errorf("%s", message)
			}
			if resp.GetSuccess() && resp.GetHasMinBalance() {
				return nil
			}
			lastErr = strings.TrimSpace(resp.GetErrorMessage())
		}

		select {
		case <-ticker.C:
			continue
		case <-deadline.C:
			if lastErr != "" {
				return fmt.Errorf("gopay balance not ready after %s: %d %s; last_error=%s", gopayBalanceWaitTimeout, lastAmount, lastCurrency, lastErr)
			}
			return fmt.Errorf("gopay balance not ready after %s: %d %s", gopayBalanceWaitTimeout, lastAmount, lastCurrency)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
