package runtime

import "testing"

func TestMailboxOAuthFailureStatusNeedsManualVerification(t *testing.T) {
	cases := []string{
		"NEEDS_MANUAL_VERIFICATION: Microsoft account redirected to account.live.com/Abuse",
		"oauth failed after redirect to https://account.live.com/Abuse",
	}
	for _, tc := range cases {
		if got := mailboxOAuthFailureStatus(tc); got != emailAuthStatusNeedsManualVerify {
			t.Fatalf("mailboxOAuthFailureStatus(%q) = %q; want %q", tc, got, emailAuthStatusNeedsManualVerify)
		}
	}
}

func TestMailboxOAuthFailureStatusAuthFailedDefault(t *testing.T) {
	if got := mailboxOAuthFailureStatus("OAuth browser flow timed out before authorization code was captured"); got != emailAuthStatusAuthFailed {
		t.Fatalf("mailboxOAuthFailureStatus(default) = %q; want %q", got, emailAuthStatusAuthFailed)
	}
}
