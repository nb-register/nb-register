package runtime

import (
	"testing"
	"time"
)

func TestGoPayAppRuntimeConfigFallbacks(t *testing.T) {
	server := &orchestratorServer{}

	if got := server.gopayAppStepResponseBodyLimit(); got != 6000 {
		t.Fatalf("gopayAppStepResponseBodyLimit = %d", got)
	}
	if got := server.gopayAppLinkPaymentWaitTimeout(); got != 180*time.Second {
		t.Fatalf("gopayAppLinkPaymentWaitTimeout = %s", got)
	}
	if got := server.gopayAppUnlinkWaitTimeout(); got != 15*time.Second {
		t.Fatalf("gopayAppUnlinkWaitTimeout = %s", got)
	}
}

func TestGoPayAppRuntimeConfigOverrides(t *testing.T) {
	server := &orchestratorServer{
		gopayAppStepBodyLimit:      7000,
		gopayAppLinkPaymentTimeout: 181 * time.Second,
		gopayAppUnlinkTimeout:      16 * time.Second,
	}

	if got := server.gopayAppStepResponseBodyLimit(); got != 7000 {
		t.Fatalf("gopayAppStepResponseBodyLimit = %d", got)
	}
	if got := server.gopayAppLinkPaymentWaitTimeout(); got != 181*time.Second {
		t.Fatalf("gopayAppLinkPaymentWaitTimeout = %s", got)
	}
	if got := server.gopayAppUnlinkWaitTimeout(); got != 16*time.Second {
		t.Fatalf("gopayAppUnlinkWaitTimeout = %s", got)
	}
}
