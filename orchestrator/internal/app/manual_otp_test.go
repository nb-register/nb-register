package app

import (
	"context"
	"testing"

	"orchestrator/db"
	"orchestrator/internal/manualinput"
)

func TestManualOTPParamsForJob(t *testing.T) {
	cases := []struct {
		name      string
		job       db.Job
		wantParam string
		wantKind  string
		wantErr   bool
	}{
		{
			name:      "register",
			job:       db.Job{Action: actionRegister},
			wantParam: registrationOTPParam,
			wantKind:  "registration",
		},
		{
			name:      "activate",
			job:       db.Job{Action: actionActivate},
			wantParam: paymentOTPParam,
			wantKind:  "payment",
		},
		{
			name:      "autopay",
			job:       db.Job{Action: actionAutopay},
			wantParam: paymentOTPParam,
			wantKind:  "payment",
		},
		{
			name:      "register and activate during registration",
			job:       db.Job{Action: actionRegisterAndActivate, LastStep: stepRegisterAccount},
			wantParam: registrationOTPParam,
			wantKind:  "registration",
		},
		{
			name:      "register and activate during payment",
			job:       db.Job{Action: actionRegisterAndActivate, LastStep: stepGoPayPayment},
			wantParam: paymentOTPParam,
			wantKind:  "payment",
		},
		{
			name:      "register and activate during gopay login",
			job:       db.Job{Action: actionRegisterAndActivate, LastStep: stepEnsureLogon},
			wantParam: paymentOTPParam,
			wantKind:  "payment",
		},
		{
			name:    "unsupported",
			job:     db.Job{Action: actionProbeAccount},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			param, _, kind, err := manualOTPParamsForJobSnapshot(&tc.job)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if param != tc.wantParam || kind != tc.wantKind {
				t.Fatalf("param=%q kind=%q", param, kind)
			}
		})
	}
}

func TestManualOTPSubmittedAfter(t *testing.T) {
	ctx := context.Background()

	fresh := newManualOTPParamStore(map[string]string{
		paymentOTPParam:            "1234",
		paymentOTPSubmittedAtParam: "200",
	})
	if !manualinput.OTPSubmittedAfter(ctx, fresh, "job-1", paymentOTPParam, paymentOTPSubmittedAtParam, 199) {
		t.Fatalf("expected fresh manual OTP to pass")
	}
	if _, ok := fresh.values[paymentOTPParam]; !ok {
		t.Fatalf("fresh OTP should not be deleted")
	}

	stale := newManualOTPParamStore(map[string]string{
		paymentOTPParam:            "1234",
		paymentOTPSubmittedAtParam: "100",
	})
	if manualinput.OTPSubmittedAfter(ctx, stale, "job-1", paymentOTPParam, paymentOTPSubmittedAtParam, 101) {
		t.Fatalf("expected stale manual OTP to be rejected")
	}
	if _, ok := stale.values[paymentOTPParam]; ok {
		t.Fatalf("stale OTP should be deleted")
	}
	if _, ok := stale.values[paymentOTPSubmittedAtParam]; ok {
		t.Fatalf("stale submitted_at should be deleted")
	}
}

type manualOTPParamStore struct {
	values map[string]string
}

func newManualOTPParamStore(values map[string]string) *manualOTPParamStore {
	return &manualOTPParamStore{values: values}
}

func (s *manualOTPParamStore) GetParam(ctx context.Context, jobID, key string) (string, bool, error) {
	value, ok := s.values[key]
	return value, ok, nil
}

func (s *manualOTPParamStore) DeleteParam(ctx context.Context, jobID, key string) error {
	delete(s.values, key)
	return nil
}
