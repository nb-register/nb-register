package app

import (
	"fmt"
	"strings"
)

func normalizeGoPayUserStateKey(value string) (string, error) {
	source, err := normalizeGoPaySource(value)
	if err != nil {
		return "", fmt.Errorf("state_key must be local or tg:<user_id>")
	}
	return source, nil
}

func normalizeGoPaySource(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "local" {
		return "local", nil
	}
	if strings.HasPrefix(value, "tg:") {
		userID := strings.TrimSpace(strings.TrimPrefix(value, "tg:"))
		if validTelegramUserID(userID) {
			return "tg:" + userID, nil
		}
	}
	return "", fmt.Errorf("source must be local or tg:<user_id>")
}

func validTelegramUserID(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func goPayOTPQueueKey(source, purpose string) (string, error) {
	source, err := normalizeGoPaySource(source)
	if err != nil {
		return "", err
	}
	purpose = strings.ToLower(strings.TrimSpace(purpose))
	if purpose == "" {
		return "", fmt.Errorf("otp purpose is required")
	}
	if strings.Contains(purpose, "/") {
		return "", fmt.Errorf("otp purpose must be a single path segment")
	}
	return source + "/" + purpose, nil
}
