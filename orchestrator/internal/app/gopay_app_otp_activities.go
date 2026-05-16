package app

import (
	"context"
	"fmt"
)

const (
	goPayAppOTPOperationAuth      = "auth"
	goPayAppOTPOperationSignup    = "signup"
	goPayAppOTPOperationCreatePin = "create_pin"
)

func (s *orchestratorServer) GoPayAppOTPStartActivity(ctx context.Context, input GoPayAppOTPStartInput) (GoPayAppOTPOutput, error) {
	switch input.GetOperation() {
	case goPayAppOTPOperationAuth:
		return s.startGoPayAppAuth(ctx, input)
	case goPayAppOTPOperationSignup:
		return s.startGoPayAppSignup(ctx, input)
	case goPayAppOTPOperationCreatePin:
		return s.startGoPayAppCreatePin(ctx, input)
	default:
		return GoPayAppOTPOutput{}, fmt.Errorf("unsupported gopay app otp operation: %s", input.GetOperation())
	}
}

func (s *orchestratorServer) GoPayAppOTPCompleteActivity(ctx context.Context, input GoPayAppOTPCompleteInput) (GoPayAppOTPOutput, error) {
	switch input.GetOperation() {
	case goPayAppOTPOperationAuth:
		return s.completeGoPayAppAuth(ctx, input)
	case goPayAppOTPOperationSignup:
		return s.completeGoPayAppSignup(ctx, input)
	case goPayAppOTPOperationCreatePin:
		return s.completeGoPayAppCreatePin(ctx, input)
	default:
		return GoPayAppOTPOutput{}, fmt.Errorf("unsupported gopay app otp operation: %s", input.GetOperation())
	}
}
