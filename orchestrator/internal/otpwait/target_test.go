package otpwait

import (
	"orchestrator/pb"
	"testing"
)

func TestChannel(t *testing.T) {
	cases := []struct {
		name  string
		input *pb.OTPWaitInput
		want  string
	}{
		{"email", &pb.OTPWaitInput{Target: &pb.OTPWaitInput_Email{Email: &pb.OTPWaitEmailTarget{Email: "a@b.c"}}}, ChannelEmail},
		{"payment", &pb.OTPWaitInput{Target: &pb.OTPWaitInput_Payment{Payment: &pb.OTPWaitPaymentTarget{Source: "local"}}}, ChannelPayment},
		{"sms", &pb.OTPWaitInput{Target: &pb.OTPWaitInput_Sms{Sms: &pb.OTPWaitSMSTarget{ActivationId: "1"}}}, ChannelSMS},
		{"missing", &pb.OTPWaitInput{}, ""},
	}
	for _, tc := range cases {
		if got := Channel(tc.input); got != tc.want {
			t.Fatalf("%s: Channel() = %q; want %q", tc.name, got, tc.want)
		}
	}
}
