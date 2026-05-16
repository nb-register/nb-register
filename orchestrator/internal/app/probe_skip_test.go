package app

import (
	"testing"

	pb "orchestrator/pb"
)

func TestShouldSkipPlusTrialProbeForKnownPlusTier(t *testing.T) {
	ref := accountRef(&pb.Account{AccountId: "acc-1", Tier: " Plus "})

	if !shouldSkipPlusTrialProbe(ref) {
		t.Fatalf("expected plus tier to skip plus trial probe")
	}

	data := skippedPlusTrialProbeData(ref)
	if data["reason"] != "tier_plus" {
		t.Fatalf("reason = %v, want tier_plus", data["reason"])
	}
}

func TestShouldNotSkipPlusTrialProbeForFreeTier(t *testing.T) {
	ref := accountRef(&pb.Account{AccountId: "acc-1", Tier: "free"})

	if shouldSkipPlusTrialProbe(ref) {
		t.Fatalf("free tier should still run plus trial probe")
	}
}
