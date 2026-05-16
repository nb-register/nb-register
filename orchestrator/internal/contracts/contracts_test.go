package contracts

import "testing"

func TestWorkflowID(t *testing.T) {
	cases := map[string]string{
		ActionRegister:            "register-job-1",
		ActionActivate:            "activate-job-1",
		ActionAutopay:             "autopay-job-1",
		ActionGoPayApp:            "gopay-app-job-1",
		ActionProbeAccount:        "probe-job-1",
		ActionLoginSession:        "login-session-job-1",
		ActionRegisterAndActivate: "register-activate-job-1",
		ActionRegisterMailbox:     "register-mailbox-job-1",
		ActionMailboxOAuth:        "mailbox-oauth-job-1",
	}
	for action, want := range cases {
		got, ok := WorkflowID(action, "job-1")
		if !ok || got != want {
			t.Fatalf("WorkflowID(%q) = %q, %v; want %q, true", action, got, ok, want)
		}
	}
	if got, ok := WorkflowID("UNKNOWN", "job-1"); ok || got != "" {
		t.Fatalf("WorkflowID(UNKNOWN) = %q, %v; want empty, false", got, ok)
	}
}

func TestManualOTPWorkflowID(t *testing.T) {
	for _, action := range []string{
		ActionRegister,
		ActionActivate,
		ActionAutopay,
		ActionGoPayApp,
		ActionRegisterAndActivate,
		ActionLoginSession,
	} {
		if got, ok := ManualOTPWorkflowID(action, "job-1"); !ok || got == "" {
			t.Fatalf("ManualOTPWorkflowID(%q) = %q, %v; want id, true", action, got, ok)
		}
	}
	if got, ok := ManualOTPWorkflowID(ActionProbeAccount, "job-1"); ok || got != "" {
		t.Fatalf("ManualOTPWorkflowID(probe) = %q, %v; want empty, false", got, ok)
	}
}
