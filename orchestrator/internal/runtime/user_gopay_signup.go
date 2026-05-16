package runtime

import (
	"context"
	"fmt"
	"orchestrator/pb"
	"strings"
)

func (s *orchestratorServer) GoPayUserSignupStart(ctx context.Context, req *pb.GoPayUserSignupStartRequest) (*pb.GoPayUserSignupStartResponse, error) {
	stateKey, err := normalizeGoPayUserStateKey(req.GetStateKey())
	if err != nil {
		return &pb.GoPayUserSignupStartResponse{ErrorMessage: err.Error()}, nil
	}
	if strings.TrimSpace(req.GetPhone()) == "" {
		return &pb.GoPayUserSignupStartResponse{ErrorMessage: "phone is required"}, nil
	}
	stateJSON, err := s.loadGoPayAppStateForKey(ctx, stateKey)
	if err != nil {
		return &pb.GoPayUserSignupStartResponse{ErrorMessage: err.Error()}, nil
	}
	resp, err := s.gopayClient.SignupStart(ctx, &pb.SignupStartRequest{
		Phone:       req.GetPhone(),
		Name:        req.GetName(),
		Email:       req.GetEmail(),
		CountryCode: req.GetCountryCode(),
		StateJson:   stateJSON,
	})
	if err == nil {
		err = s.saveGoPayAppStateForKey(ctx, stateKey, resp.GetStateJson())
	}
	if err != nil {
		return &pb.GoPayUserSignupStartResponse{ErrorMessage: fmt.Sprintf("SignupStart: %v", err)}, nil
	}
	return &pb.GoPayUserSignupStartResponse{
		Success:            resp.GetSuccess(),
		ErrorMessage:       resp.GetErrorMessage(),
		OtpSent:            resp.GetOtpSent(),
		VerificationId:     resp.GetVerificationId(),
		VerificationMethod: resp.GetVerificationMethod(),
	}, nil
}

func (s *orchestratorServer) GoPayUserSignupComplete(ctx context.Context, req *pb.GoPayUserSignupCompleteRequest) (*pb.GoPayUserSignupCompleteResponse, error) {
	stateKey, err := normalizeGoPayUserStateKey(req.GetStateKey())
	if err != nil {
		return &pb.GoPayUserSignupCompleteResponse{ErrorMessage: err.Error()}, nil
	}
	if strings.TrimSpace(req.GetOtp()) == "" {
		return &pb.GoPayUserSignupCompleteResponse{ErrorMessage: "otp is required"}, nil
	}
	stateJSON, err := s.loadGoPayAppStateForKey(ctx, stateKey)
	if err != nil {
		return &pb.GoPayUserSignupCompleteResponse{ErrorMessage: err.Error()}, nil
	}
	resp, err := s.gopayClient.SignupComplete(ctx, &pb.SignupCompleteRequest{Otp: req.GetOtp(), StateJson: stateJSON})
	if err == nil {
		err = s.saveGoPayAppStateForKey(ctx, stateKey, resp.GetStateJson())
	}
	if err != nil {
		return &pb.GoPayUserSignupCompleteResponse{ErrorMessage: fmt.Sprintf("SignupComplete: %v", err)}, nil
	}
	return &pb.GoPayUserSignupCompleteResponse{
		Success:          resp.GetSuccess(),
		ErrorMessage:     resp.GetErrorMessage(),
		Phone:            resp.GetPhone(),
		PinSetupRequired: resp.GetPinSetupRequired(),
	}, nil
}
