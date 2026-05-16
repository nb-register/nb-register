package runtime

import (
	"strings"

	pb "orchestrator/pb"
)

func normalizeIndonesiaPhone(phone string) string {
	value := strings.TrimPrefix(strings.TrimSpace(phone), "+")
	if strings.HasPrefix(value, "62") {
		return strings.TrimPrefix(value[2:], "0")
	}
	return value
}

func checkPhoneStatus(resp *pb.CheckPhoneResponse) string {
	if resp == nil {
		return "error"
	}
	status := strings.ToLower(strings.TrimSpace(resp.GetStatus()))
	if status != "" {
		return status
	}
	if resp.GetAvailable() {
		return "available"
	}
	switch strings.ToUpper(strings.TrimSpace(resp.GetErrorMessage())) {
	case "PHONE_REGISTERED":
		return "registered"
	case "PHONE_EXHAUSTED":
		return "exhausted"
	}
	return "unavailable"
}
