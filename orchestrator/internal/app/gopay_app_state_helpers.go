package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	pb "orchestrator/pb"
)

func (s *orchestratorServer) goPayStatus(ctx context.Context) (*pb.StatusResponse, error) {
	if s.gopayClient == nil {
		return nil, fmt.Errorf("gopay app client not configured")
	}
	stateJSON, err := s.loadGoPayAppState(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := s.gopayClient.Status(ctx, &pb.StatusRequest{StateJson: stateJSON})
	if err == nil {
		err = s.saveGoPayAppState(ctx, resp.GetStateJson())
	}
	if err != nil {
		return resp, fmt.Errorf("Status: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("Status returned empty response")
	}
	return resp, nil
}

func (s *orchestratorServer) loadGoPayAppState(ctx context.Context) (string, error) {
	return s.loadGoPayAppStateForKey(ctx, goPayAppStateKey)
}

func (s *orchestratorServer) loadGoPayAppStateForKey(ctx context.Context, stateKey string) (string, error) {
	if s.gopayClient == nil {
		return "{}", fmt.Errorf("gopay-app client not configured")
	}
	resp, err := s.gopayClient.GetGoPayState(ctx, &pb.GetGoPayStateRequest{StateKey: stateKey})
	if err != nil {
		return "", fmt.Errorf("GetGoPayState: %w", err)
	}
	if resp == nil {
		return "", fmt.Errorf("GetGoPayState returned empty response")
	}
	if !resp.GetSuccess() {
		return "", fmt.Errorf("GetGoPayState: %s", resp.GetErrorMessage())
	}
	stateJSON := strings.TrimSpace(resp.GetStateJson())
	if stateJSON == "" {
		stateJSON = "{}"
	}
	return stateJSON, nil
}

func (s *orchestratorServer) saveGoPayAppState(ctx context.Context, stateJSON string) error {
	return s.saveGoPayAppStateForKey(ctx, goPayAppStateKey, stateJSON)
}

func (s *orchestratorServer) saveGoPayAppStateForKey(ctx context.Context, stateKey string, stateJSON string) error {
	stateJSON = strings.TrimSpace(stateJSON)
	if stateJSON == "" {
		return nil
	}
	if s.gopayClient == nil {
		return fmt.Errorf("gopay-app client not configured")
	}
	resp, err := s.gopayClient.UpsertGoPayState(ctx, &pb.UpsertGoPayStateRequest{
		StateKey:  stateKey,
		StateJson: stateJSON,
	})
	if err != nil {
		return fmt.Errorf("UpsertGoPayState: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("UpsertGoPayState returned empty response")
	}
	if !resp.GetSuccess() {
		return fmt.Errorf("UpsertGoPayState: %s", resp.GetErrorMessage())
	}
	return nil
}

func (s *orchestratorServer) deleteGoPayAppStateForKey(ctx context.Context, stateKey string) error {
	if s.gopayClient == nil {
		return fmt.Errorf("gopay-app client not configured")
	}
	resp, err := s.gopayClient.DeleteGoPayState(ctx, &pb.DeleteGoPayStateRequest{StateKey: stateKey})
	if err != nil {
		return fmt.Errorf("DeleteGoPayState: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("DeleteGoPayState returned empty response")
	}
	if !resp.GetSuccess() {
		return fmt.Errorf("DeleteGoPayState: %s", resp.GetErrorMessage())
	}
	return nil
}

func configuredGoPayPhone() string {
	return normalizeIndonesiaPhone(os.Getenv("GOPAY_PHONE_NUMBER"))
}

func configuredGoPayPIN() string {
	return strings.TrimSpace(os.Getenv("GOPAY_PIN"))
}

func configuredGoPayCountryCode() string {
	value := strings.TrimSpace(os.Getenv("GOPAY_COUNTRY_CODE"))
	if value == "" {
		value = "62"
	}
	if !strings.HasPrefix(value, "+") {
		value = "+" + value
	}
	return value
}
