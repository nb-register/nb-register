package contracts

import "strings"

const (
	ActionRegister            = "REGISTER"
	ActionActivate            = "ACTIVATE"
	ActionAutopay             = "AUTOPAY"
	ActionGoPayApp            = "GOPAY_APP"
	ActionProbeAccount        = "PROBE_ACCOUNT"
	ActionLoginSession        = "LOGIN_SESSION"
	ActionRegisterAndActivate = "REGISTER_AND_ACTIVATE"
	ActionRegisterMailbox     = "REGISTER_MAILBOX"
	ActionMailboxOAuth        = "MAILBOX_OAUTH"
)

func WorkflowID(action string, jobID string) (string, bool) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return "", false
	}
	switch strings.TrimSpace(action) {
	case ActionRegister:
		return "register-" + jobID, true
	case ActionActivate:
		return "activate-" + jobID, true
	case ActionAutopay:
		return "autopay-" + jobID, true
	case ActionGoPayApp:
		return "gopay-app-" + jobID, true
	case ActionProbeAccount:
		return "probe-" + jobID, true
	case ActionLoginSession:
		return "login-session-" + jobID, true
	case ActionRegisterAndActivate:
		return "register-activate-" + jobID, true
	case ActionRegisterMailbox:
		return "register-mailbox-" + jobID, true
	case ActionMailboxOAuth:
		return "mailbox-oauth-" + jobID, true
	default:
		return "", false
	}
}

func ManualOTPWorkflowID(action string, jobID string) (string, bool) {
	switch strings.TrimSpace(action) {
	case ActionRegister, ActionActivate, ActionAutopay, ActionGoPayApp, ActionRegisterAndActivate, ActionLoginSession:
		return WorkflowID(action, jobID)
	default:
		return "", false
	}
}
