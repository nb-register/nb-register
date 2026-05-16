package app

import (
	"context"
	"fmt"

	pb "orchestrator/pb"
)

func (s *orchestratorServer) finishEnsureLogon(ctx context.Context, output *pb.EnsureLogonResponse) (map[string]any, error) {
	statusResp, statusErr := s.goPayStatus(ctx)
	output.StatusAfter = goPayStatusSnapshot(statusResp, statusErr)
	if statusErr != nil {
		output.ErrorMessage = statusErr.Error()
		return ensureLogonData(output), statusErr
	}
	if !goPayStatusTokenReady(statusResp) {
		err := fmt.Errorf("gopay-app not logged on after logon: stage=%s", statusResp.GetStage())
		output.ErrorMessage = err.Error()
		return ensureLogonData(output), err
	}
	output.Ready = true
	output.Stage = statusResp.GetStage()
	output.Phone = statusResp.GetPhone()
	output.LogonComplete = true
	return ensureLogonData(output), nil
}

func goPayStatusSnapshot(resp *pb.StatusResponse, err error) *pb.StatusResponse {
	if resp == nil && err == nil {
		return nil
	}
	snapshot := &pb.StatusResponse{}
	if resp != nil {
		snapshot.Stage = resp.GetStage()
		snapshot.Phone = resp.GetPhone()
		snapshot.TokenPresent = resp.GetTokenPresent()
		snapshot.DeviceFingerprint = resp.GetDeviceFingerprint()
		snapshot.DeactivatedAt = resp.GetDeactivatedAt()
		snapshot.ErrorMessage = resp.GetErrorMessage()
		snapshot.BalanceAmount = resp.GetBalanceAmount()
		snapshot.HasMinBalance = resp.GetHasMinBalance()
		snapshot.BalanceCurrency = resp.GetBalanceCurrency()
	}
	if err != nil {
		snapshot.ErrorMessage = err.Error()
	}
	return snapshot
}

func ensureLogonData(resp *pb.EnsureLogonResponse) map[string]any {
	if resp == nil || ensureLogonResponseEmpty(resp) {
		return nil
	}
	data := map[string]any{
		"ready":                 resp.GetReady(),
		"already_ready":         resp.GetAlreadyReady(),
		"change_phone_complete": resp.GetChangePhoneComplete(),
		"deactivate_complete":   resp.GetDeactivateComplete(),
		"logon_complete":        resp.GetLogonComplete(),
		"signup_complete":       resp.GetSignupComplete(),
		"signup_pin_complete":   resp.GetSignupPinComplete(),
		"account_token_ready":   resp.GetAccountTokenReady(),
	}
	if resp.GetStage() != "" {
		data["stage"] = resp.GetStage()
	}
	if resp.GetPhone() != "" {
		data["phone"] = resp.GetPhone()
	}
	if status := goPayStatusSnapshotData(resp.GetStatusBefore()); status != nil {
		data["status_before"] = status
	}
	if status := goPayStatusSnapshotData(resp.GetStatusAfter()); status != nil {
		data["status_after"] = status
	}
	if resp.GetErrorMessage() != "" {
		data["error_message"] = resp.GetErrorMessage()
	}
	return data
}

func ensureLogonResponseEmpty(resp *pb.EnsureLogonResponse) bool {
	return !resp.GetReady() &&
		resp.GetStage() == "" &&
		resp.GetPhone() == "" &&
		resp.GetStatusBefore() == nil &&
		resp.GetStatusAfter() == nil &&
		!resp.GetAlreadyReady() &&
		!resp.GetChangePhoneComplete() &&
		!resp.GetDeactivateComplete() &&
		!resp.GetLogonComplete() &&
		!resp.GetSignupComplete() &&
		!resp.GetSignupPinComplete() &&
		!resp.GetAccountTokenReady() &&
		resp.GetErrorMessage() == ""
}

func goPayStatusSnapshotData(snapshot *pb.StatusResponse) map[string]any {
	if snapshot == nil || goPayStatusSnapshotEmpty(snapshot) {
		return nil
	}
	data := map[string]any{
		"token_present": snapshot.GetTokenPresent(),
	}
	if snapshot.GetStage() != "" {
		data["stage"] = snapshot.GetStage()
	}
	if snapshot.GetPhone() != "" {
		data["phone"] = snapshot.GetPhone()
	}
	if snapshot.GetDeviceFingerprint() != "" {
		data["device_fingerprint"] = snapshot.GetDeviceFingerprint()
	}
	if snapshot.GetDeactivatedAt() != 0 {
		data["deactivated_at"] = snapshot.GetDeactivatedAt()
	}
	if snapshot.GetErrorMessage() != "" {
		data["error_message"] = snapshot.GetErrorMessage()
	}
	if snapshot.GetBalanceAmount() != 0 || snapshot.GetBalanceCurrency() != "" || snapshot.GetHasMinBalance() {
		data["balance_amount"] = snapshot.GetBalanceAmount()
		data["has_min_balance"] = snapshot.GetHasMinBalance()
		if snapshot.GetBalanceCurrency() != "" {
			data["balance_currency"] = snapshot.GetBalanceCurrency()
		}
	}
	return data
}

func checkTokenValidData(resp *pb.CheckTokenValidResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	data := map[string]any{
		"response_present": true,
		"success":          resp.GetSuccess(),
		"token_valid":      resp.GetTokenValid(),
		"refreshed":        resp.GetRefreshed(),
		"has_min_balance":  resp.GetHasMinBalance(),
	}
	if resp.GetStage() != "" {
		data["stage"] = resp.GetStage()
	}
	if resp.GetPhone() != "" {
		data["phone"] = resp.GetPhone()
	}
	if resp.GetBalanceAmount() != 0 || resp.GetBalanceCurrency() != "" {
		data["balance_amount"] = resp.GetBalanceAmount()
		if resp.GetBalanceCurrency() != "" {
			data["balance_currency"] = resp.GetBalanceCurrency()
		}
	}
	if resp.GetErrorMessage() != "" {
		data["error_message"] = resp.GetErrorMessage()
	}
	return data
}

func goPayStatusSnapshotEmpty(snapshot *pb.StatusResponse) bool {
	return snapshot.GetStage() == "" &&
		snapshot.GetPhone() == "" &&
		!snapshot.GetTokenPresent() &&
		snapshot.GetDeviceFingerprint() == "" &&
		snapshot.GetDeactivatedAt() == 0 &&
		snapshot.GetErrorMessage() == "" &&
		snapshot.GetBalanceAmount() == 0 &&
		!snapshot.GetHasMinBalance() &&
		snapshot.GetBalanceCurrency() == ""
}
