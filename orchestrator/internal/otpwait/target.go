package otpwait

import "orchestrator/pb"

const (
	ChannelEmail   = "email"
	ChannelPayment = "payment"
	ChannelSMS     = "sms"
)

func Channel(input *pb.OTPWaitInput) string {
	if input == nil {
		return ""
	}
	switch {
	case input.GetEmail() != nil:
		return ChannelEmail
	case input.GetPayment() != nil:
		return ChannelPayment
	case input.GetSms() != nil:
		return ChannelSMS
	default:
		return ""
	}
}

func TimeoutSeconds(input *pb.OTPWaitInput, fallback int32) int32 {
	if input == nil {
		return fallback
	}
	if input.GetTimeoutSeconds() > 0 {
		return input.GetTimeoutSeconds()
	}
	return fallback
}
