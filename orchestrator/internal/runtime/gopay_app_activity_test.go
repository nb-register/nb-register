package runtime

import (
	"testing"

	pb "orchestrator/pb"
)

func TestEnsureLogonDataUsesLogonComplete(t *testing.T) {
	data := ensureLogonData(&pb.EnsureLogonResponse{
		Ready:         true,
		LogonComplete: true,
	})

	if data["logon_complete"] != true {
		t.Fatalf("logon_complete = %v, want true", data["logon_complete"])
	}
}

func TestNormalizeIndonesiaPhone(t *testing.T) {
	tests := map[string]string{
		"+6281234567890": "81234567890",
		"6281234567890":  "81234567890",
		"081234567890":   "081234567890",
	}
	for input, want := range tests {
		if got := normalizeIndonesiaPhone(input); got != want {
			t.Fatalf("normalizeIndonesiaPhone(%q) = %q, want %q", input, got, want)
		}
	}
}
