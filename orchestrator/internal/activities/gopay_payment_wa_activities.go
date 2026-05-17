package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm/clause"

	"orchestrator/db"
	pb "orchestrator/pb"
)

func (s *Server) GoPayResolveWAPhoneActivity(ctx context.Context, input GoPayResolveWAPhoneInput) (GoPayResolveWAPhoneOutput, error) {
	userID, err := normalizeGoPayUserID(input.GetUserId())
	if err != nil {
		return GoPayResolveWAPhoneOutput{}, err
	}
	output := GoPayResolveWAPhoneOutput{UserId: userID}
	data := map[string]any{"user_id": userID}
	step := s.activityStep(ctx, input.GetJobId(), stepGoPayAppWAPhoneCheck, false, true)
	_, err = step.run(func() (any, error) {
		if s.db == nil {
			err := fmt.Errorf("orchestrator db not configured")
			data["error_message"] = err.Error()
			return data, err
		}
		if s.gopayClient == nil {
			err := fmt.Errorf("gopay app client not configured")
			data["error_message"] = err.Error()
			return data, err
		}

		phone := normalizeIndonesiaPhone(input.GetWaPhone())
		data["request_phone_present"] = phone != ""
		if phone == "" && userID == goPayLocalSource {
			phone = configuredGoPayWAPhone()
			data["env_phone_present"] = phone != ""
		}
		if phone == "" {
			stored, err := s.loadGoPayWAPhoneProfile(ctx, userID)
			if err != nil {
				data["profile_error"] = err.Error()
				return data, err
			}
			phone = stored
			data["profile_phone_present"] = phone != ""
		}
		if phone == "" {
			err := fmt.Errorf("wa_phone is required for WA GoPay payment")
			data["error_message"] = err.Error()
			return data, err
		}

		checkResp, err := s.gopayClient.CheckPhone(ctx, &pb.CheckPhoneRequest{
			Phone:       phone,
			CountryCode: configuredGoPayCountryCode(),
		})
		if err != nil {
			err = fmt.Errorf("CheckPhone: %w", err)
			data["error_message"] = err.Error()
			return data, err
		}
		status := checkPhoneStatus(checkResp)
		data["phone_present"] = true
		data["phone_status"] = status
		message := ""
		if checkResp != nil {
			message = strings.TrimSpace(checkResp.GetErrorMessage())
		}
		if err := goPayWAPhoneCheckError(status, message); err != nil {
			data["error_message"] = err.Error()
			return data, err
		}
		if err := s.saveGoPayWAPhoneProfile(ctx, userID, phone); err != nil {
			data["profile_error"] = err.Error()
			return data, err
		}
		data["profile_saved"] = true
		output.WaPhone = phone
		return data, nil
	})
	output.Data = protoData(data)
	return output, err
}

func goPayWAPhoneCheckError(status, message string) error {
	status = strings.ToLower(strings.TrimSpace(status))
	message = strings.TrimSpace(message)
	if status == "available" || status == "registered" {
		return nil
	}
	if message == "" {
		message = status
	}
	switch status {
	case "rate_limited", "error":
		return fmt.Errorf("wa_phone check inconclusive: %s", message)
	default:
		return fmt.Errorf("wa_phone unavailable: %s", message)
	}
}

func (s *Server) GoPayAppLoadStateActivity(ctx context.Context, input GoPayAppStateActivityInput) (GoPayAppStateActivityOutput, error) {
	userID, err := normalizeGoPayUserID(input.GetUserId())
	if err != nil {
		return GoPayAppStateActivityOutput{}, err
	}
	output := GoPayAppStateActivityOutput{UserId: userID}
	data := map[string]any{"user_id": userID, "reason": input.GetReason()}
	stateJSON, err := s.loadGoPayAppStateForUser(ctx, userID)
	if err != nil {
		data["error_message"] = err.Error()
		output.Data = protoData(data)
		return output, err
	}
	output.StateJson = stateJSON
	data["state_present"] = strings.TrimSpace(stateJSON) != ""
	output.Data = protoData(data)
	return output, nil
}

func (s *Server) GoPayAppSaveStateActivity(ctx context.Context, input GoPayAppStateActivityInput) (GoPayAppStateActivityOutput, error) {
	userID, err := normalizeGoPayUserID(input.GetUserId())
	if err != nil {
		return GoPayAppStateActivityOutput{}, err
	}
	stateJSON := normalizeGoPayWorkflowStateJSON(input.GetStateJson())
	output := GoPayAppStateActivityOutput{UserId: userID, StateJson: stateJSON}
	data := map[string]any{
		"user_id":       userID,
		"reason":        input.GetReason(),
		"state_present": strings.TrimSpace(stateJSON) != "",
	}
	if err := s.saveGoPayAppStateForUser(ctx, userID, stateJSON); err != nil {
		data["error_message"] = err.Error()
		output.Data = protoData(data)
		return output, err
	}
	data["saved"] = true
	output.Data = protoData(data)
	return output, nil
}

func (s *Server) GoPayAppDeleteStateActivity(ctx context.Context, input GoPayAppStateActivityInput) (GoPayAppStateActivityOutput, error) {
	userID, err := normalizeGoPayUserID(input.GetUserId())
	if err != nil {
		return GoPayAppStateActivityOutput{}, err
	}
	output := GoPayAppStateActivityOutput{UserId: userID}
	data := map[string]any{
		"user_id": userID,
		"reason":  input.GetReason(),
	}
	if err := s.deleteGoPayAppStateForUser(ctx, userID); err != nil {
		data["error_message"] = err.Error()
		output.Data = protoData(data)
		return output, err
	}
	data["deleted"] = true
	output.Data = protoData(data)
	return output, nil
}

func (s *Server) GoPayPaymentRebindSourceActivity(ctx context.Context, input GoPayPaymentRebindSourceInput) (GoPayPaymentRebindSourceOutput, error) {
	output := GoPayPaymentRebindSourceOutput{SourceJobId: strings.TrimSpace(input.GetSourceJobId())}
	data := map[string]any{"source_job_id": output.GetSourceJobId()}
	if output.GetSourceJobId() == "" {
		err := fmt.Errorf("source_job_id is required")
		output.Data = protoData(data)
		return output, err
	}
	sourceJob, err := s.jobStore.Get(ctx, output.GetSourceJobId())
	if err != nil {
		err = fmt.Errorf("load source job: %w", err)
		data["error_message"] = err.Error()
		output.Data = protoData(data)
		return output, err
	}
	if sourceJob.Action != actionGoPayPayment {
		err := fmt.Errorf("source job is not GOPAY_PAYMENT: %s", sourceJob.Action)
		data["error_message"] = err.Error()
		output.Data = protoData(data)
		return output, err
	}

	result := map[string]any{}
	if strings.TrimSpace(sourceJob.ResultJSON) != "" {
		_ = json.Unmarshal([]byte(sourceJob.ResultJSON), &result)
	}
	accountID := firstNonEmpty(input.GetAccountId(), sourceJob.AccountID, stringField(result, "account_id"))
	userID := firstNonEmpty(input.GetUserId(), jobParam(ctx, s, output.GetSourceJobId(), "user_id"), stringField(result, "user_id"))
	userID, err = normalizeGoPayUserID(userID)
	if err != nil {
		data["error_message"] = err.Error()
		output.Data = protoData(data)
		return output, err
	}
	waPhone := firstNonEmpty(jobParam(ctx, s, output.GetSourceJobId(), "wa_phone"), stringField(result, "wa_phone"))
	if waPhone == "" {
		waPhone, _ = s.loadGoPayWAPhoneProfile(ctx, userID)
	}
	if waPhone == "" {
		err := fmt.Errorf("source job wa_phone is required for GoPay WA rebind")
		data["error_message"] = err.Error()
		output.Data = protoData(data)
		return output, err
	}
	chargeRef := firstNonEmpty(stringField(result, "charge_ref"), nestedStringField(result, "gopay_payment", "charge_ref"), nestedStringField(result, "payment", "charge_ref"))
	snapToken := firstNonEmpty(stringField(result, "snap_token"), nestedStringField(result, "gopay_payment", "snap_token"), nestedStringField(result, "payment", "snap_token"))
	if chargeRef == "" && snapToken == "" {
		err := fmt.Errorf("source job has no completed GoPay payment result")
		data["error_message"] = err.Error()
		output.Data = protoData(data)
		return output, err
	}

	output.AccountId = accountID
	output.UserId = userID
	output.WaPhone = waPhone
	output.ChargeRef = chargeRef
	output.SnapToken = snapToken
	data["account_id"] = accountID
	data["user_id"] = userID
	data["wa_phone_present"] = waPhone != ""
	data["charge_ref_present"] = chargeRef != ""
	data["snap_token_present"] = snapToken != ""
	output.Data = protoData(data)
	return output, nil
}

func (s *Server) loadGoPayWAPhoneProfile(ctx context.Context, userID string) (string, error) {
	if s.db == nil {
		return "", fmt.Errorf("orchestrator db not configured")
	}
	var profile db.GoPayUserProfile
	result := s.db.WithContext(ctx).Where("state_key = ?", userID).Limit(1).Find(&profile)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 0 {
		return "", nil
	}
	return normalizeIndonesiaPhone(profile.WAPhone), nil
}

func (s *Server) saveGoPayWAPhoneProfile(ctx context.Context, userID, phone string) error {
	if s.db == nil {
		return fmt.Errorf("orchestrator db not configured")
	}
	phone = normalizeIndonesiaPhone(phone)
	if phone == "" {
		return fmt.Errorf("wa_phone is required")
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "state_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"wa_phone", "updated_at"}),
	}).Create(&db.GoPayUserProfile{StateKey: userID, WAPhone: phone}).Error
}

func jobParam(ctx context.Context, s *Server, jobID, key string) string {
	if s == nil || s.jobStore == nil {
		return ""
	}
	value, found, err := s.jobStore.GetParam(ctx, jobID, key)
	if err != nil || !found {
		return ""
	}
	return strings.TrimSpace(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func stringField(data map[string]any, key string) string {
	value, ok := data[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}

func nestedStringField(data map[string]any, keys ...string) string {
	current := any(data)
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	if value, ok := current.(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}
