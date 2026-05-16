package app

import (
	"strings"
)

func (s *orchestratorServer) paymentOtpTimeout() int32 {
	if s.otpTimeout <= 0 {
		return 60
	}
	return s.otpTimeout
}

func (s *orchestratorServer) registrationOtpTimeout() int32 {
	if s.regOTPTimeout <= 0 {
		return 120
	}
	return s.regOTPTimeout
}

func normalizeOTP(value string) string {
	replacer := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "", "-", "")
	return strings.TrimSpace(replacer.Replace(value))
}

func normalizeTier(tier string) string {
	return strings.ToLower(strings.TrimSpace(tier))
}
