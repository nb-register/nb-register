package app

import "strings"

func isAccountAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	return isAccountAlreadyExistsMessage(err.Error())
}

func isAccountAlreadyExistsMessage(message string) bool {
	normalized := strings.ToLower(message)
	normalized = strings.NewReplacer("_", " ", "-", " ", ".", " ", ":", " ").Replace(normalized)

	return strings.Contains(normalized, "user already exist") ||
		strings.Contains(normalized, "account already exist")
}

func isUserAlreadyExistsStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case accountStatusUserAlreadyExists, "EMAIL_ALREADY_EXISTS":
		return true
	default:
		return false
	}
}

func registerFailurePolicy(err error) (status string, recoverable bool, retryable bool) {
	if isAccountAlreadyExistsError(err) {
		return statusFailedFinal, false, false
	}
	return statusFailedRetryable, false, true
}
