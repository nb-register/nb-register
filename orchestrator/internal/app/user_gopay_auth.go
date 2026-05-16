package app

import (
	"context"
	"fmt"
	"orchestrator/pb"
	"strings"
)

func (s *orchestratorServer) GoPayUserAuthStart(ctx context.Context, req *pb.GoPayUserAuthStartRequest) (*pb.GoPayUserAuthStartResponse, error) {
	stateKey, err := normalizeGoPayUserStateKey(req.GetStateKey())
	if err != nil {
		return &pb.GoPayUserAuthStartResponse{ErrorMessage: err.Error()}, nil
	}
	phone := strings.TrimSpace(req.GetPhone())
	if phone == "" {
		return &pb.GoPayUserAuthStartResponse{ErrorMessage: "phone is required"}, nil
	}
	if strings.TrimSpace(req.GetPin()) == "" {
		return &pb.GoPayUserAuthStartResponse{ErrorMessage: "pin is required"}, nil
	}
	stateJSON, err := s.loadGoPayAppStateForKey(ctx, stateKey)
	if err != nil {
		return &pb.GoPayUserAuthStartResponse{ErrorMessage: err.Error()}, nil
	}
	resp, err := s.gopayClient.LoginStart(ctx, &pb.LoginStartRequest{
		Phone:       phone,
		CountryCode: req.GetCountryCode(),
		Pin:         req.GetPin(),
		StateJson:   stateJSON,
	})
	if err == nil {
		err = s.saveGoPayAppStateForKey(ctx, stateKey, resp.GetStateJson())
	}
	if err != nil {
		return &pb.GoPayUserAuthStartResponse{ErrorMessage: fmt.Sprintf("LoginStart: %v", err)}, nil
	}
	stage := ""
	ready := false
	if resp.GetOtpSent() {
		stage = "login_otp_pending"
	} else if resp.GetSuccess() {
		status, statusErr := s.userGoPayStatus(ctx, stateKey)
		if statusErr == nil && status != nil {
			stage = status.GetStage()
			ready = goPayStatusTokenReady(status)
		} else {
			ready = true
		}
	}
	return &pb.GoPayUserAuthStartResponse{
		Success:        resp.GetSuccess(),
		ErrorMessage:   resp.GetErrorMessage(),
		Mode:           "login",
		Stage:          stage,
		OtpSent:        resp.GetOtpSent(),
		VerificationId: resp.GetVerificationId(),
		Ready:          ready,
	}, nil
}

func (s *orchestratorServer) GoPayUserAuthComplete(ctx context.Context, req *pb.GoPayUserAuthCompleteRequest) (*pb.GoPayUserAuthCompleteResponse, error) {
	stateKey, err := normalizeGoPayUserStateKey(req.GetStateKey())
	if err != nil {
		return &pb.GoPayUserAuthCompleteResponse{ErrorMessage: err.Error()}, nil
	}
	if strings.TrimSpace(req.GetOtp()) == "" {
		return &pb.GoPayUserAuthCompleteResponse{ErrorMessage: "otp is required"}, nil
	}
	if strings.TrimSpace(req.GetPin()) == "" {
		return &pb.GoPayUserAuthCompleteResponse{ErrorMessage: "pin is required"}, nil
	}
	stateJSON, err := s.loadGoPayAppStateForKey(ctx, stateKey)
	if err != nil {
		return &pb.GoPayUserAuthCompleteResponse{ErrorMessage: err.Error()}, nil
	}
	resp, err := s.gopayClient.LoginComplete(ctx, &pb.LoginCompleteRequest{Otp: req.GetOtp(), StateJson: stateJSON})
	if err == nil {
		err = s.saveGoPayAppStateForKey(ctx, stateKey, resp.GetStateJson())
	}
	if err != nil {
		return &pb.GoPayUserAuthCompleteResponse{ErrorMessage: fmt.Sprintf("LoginComplete: %v", err)}, nil
	}
	stage := ""
	ready := false
	if resp.GetSuccess() {
		status, statusErr := s.userGoPayStatus(ctx, stateKey)
		if statusErr == nil && status != nil {
			stage = status.GetStage()
			ready = goPayStatusTokenReady(status)
		} else {
			stage = "ready"
			ready = true
		}
	}
	return &pb.GoPayUserAuthCompleteResponse{
		Success:      resp.GetSuccess(),
		ErrorMessage: resp.GetErrorMessage(),
		Mode:         "login",
		Stage:        stage,
		Phone:        resp.GetPhone(),
		Ready:        ready,
	}, nil
}
