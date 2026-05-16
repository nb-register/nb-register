package app

import (
	"context"
	"fmt"
	"orchestrator/pb"
	"strings"
)

func (s *orchestratorServer) GoPayUserCreatePinStart(ctx context.Context, req *pb.GoPayUserCreatePinStartRequest) (*pb.GoPayUserCreatePinStartResponse, error) {
	stateKey, err := normalizeGoPayUserStateKey(req.GetStateKey())
	if err != nil {
		return &pb.GoPayUserCreatePinStartResponse{ErrorMessage: err.Error()}, nil
	}
	if strings.TrimSpace(req.GetPin()) == "" {
		return &pb.GoPayUserCreatePinStartResponse{ErrorMessage: "pin is required"}, nil
	}
	stateJSON, err := s.loadGoPayAppStateForKey(ctx, stateKey)
	if err != nil {
		return &pb.GoPayUserCreatePinStartResponse{ErrorMessage: err.Error()}, nil
	}
	resp, err := s.gopayClient.CreatePinStart(ctx, &pb.CreatePinStartRequest{Pin: req.GetPin(), StateJson: stateJSON})
	if err == nil {
		err = s.saveGoPayAppStateForKey(ctx, stateKey, resp.GetStateJson())
	}
	if err != nil {
		return &pb.GoPayUserCreatePinStartResponse{ErrorMessage: fmt.Sprintf("CreatePinStart: %v", err)}, nil
	}
	return &pb.GoPayUserCreatePinStartResponse{
		Success:            resp.GetSuccess(),
		ErrorMessage:       resp.GetErrorMessage(),
		OtpSent:            resp.GetOtpSent(),
		VerificationId:     resp.GetVerificationId(),
		VerificationMethod: resp.GetVerificationMethod(),
	}, nil
}

func (s *orchestratorServer) GoPayUserCreatePinComplete(ctx context.Context, req *pb.GoPayUserCreatePinCompleteRequest) (*pb.GoPayUserCreatePinCompleteResponse, error) {
	stateKey, err := normalizeGoPayUserStateKey(req.GetStateKey())
	if err != nil {
		return &pb.GoPayUserCreatePinCompleteResponse{ErrorMessage: err.Error()}, nil
	}
	if strings.TrimSpace(req.GetOtp()) == "" {
		return &pb.GoPayUserCreatePinCompleteResponse{ErrorMessage: "otp is required"}, nil
	}
	if strings.TrimSpace(req.GetPin()) == "" {
		return &pb.GoPayUserCreatePinCompleteResponse{ErrorMessage: "pin is required"}, nil
	}
	stateJSON, err := s.loadGoPayAppStateForKey(ctx, stateKey)
	if err != nil {
		return &pb.GoPayUserCreatePinCompleteResponse{ErrorMessage: err.Error()}, nil
	}
	resp, err := s.gopayClient.CreatePinComplete(ctx, &pb.CreatePinCompleteRequest{
		Otp:       req.GetOtp(),
		Pin:       req.GetPin(),
		StateJson: stateJSON,
	})
	if err == nil {
		err = s.saveGoPayAppStateForKey(ctx, stateKey, resp.GetStateJson())
	}
	if err != nil {
		return &pb.GoPayUserCreatePinCompleteResponse{ErrorMessage: fmt.Sprintf("CreatePinComplete: %v", err)}, nil
	}
	return &pb.GoPayUserCreatePinCompleteResponse{
		Success:          resp.GetSuccess(),
		ErrorMessage:     resp.GetErrorMessage(),
		Phone:            resp.GetPhone(),
		PinSetupComplete: resp.GetPinSetupComplete(),
	}, nil
}
