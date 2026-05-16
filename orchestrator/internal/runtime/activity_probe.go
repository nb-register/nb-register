package runtime

import (
	"context"
	"fmt"
	"orchestrator/pb"
	"strings"
)

func (s *orchestratorServer) ProbePlusTrialAtomicActivity(ctx context.Context, input ProbePlusTrialActivityInput) (ProbePlusTrialActivityOutput, error) {
	account, err := s.getAccount(ctx, input.GetAccountId())
	if err != nil {
		return ProbePlusTrialActivityOutput{}, err
	}
	if err := rejectUserAlreadyExistsAccount(account); err != nil {
		return ProbePlusTrialActivityOutput{}, err
	}

	var output ProbePlusTrialActivityOutput
	step := s.activityStep(ctx, input.GetJobId(), stepProbePlusTrial, false, true)
	_, err = step.run(func() (any, error) {
		sessionToken := strings.TrimSpace(account.GetSessionToken())
		accessToken := strings.TrimSpace(account.GetAccessToken())
		data := map[string]any{
			"account_id":            account.GetAccountId(),
			"session_token_present": sessionToken != "",
			"access_token_present":  accessToken != "",
		}
		ref := accountRef(account)
		if shouldSkipPlusTrialProbe(ref) {
			for key, value := range skippedPlusTrialProbeData(ref) {
				data[key] = value
			}
			source := "account.plus_active"
			if normalizeTier(ref.GetTier()) == "plus" {
				source = "account.tier"
			}
			output.Success = true
			output.Checked = true
			output.PlusTrialEligible = false
			output.PlusActive = true
			output.PlanType = ref.GetTier()
			output.Source = source
			data["success"] = true
			data["checked"] = true
			data["plus_trial_eligible"] = false
			data["plus_active"] = true
			data["plan_type"] = ref.GetTier()
			data["source"] = source
			update := &pb.Account{
				AccountId:         input.GetAccountId(),
				PlusTrialEligible: boolPtr(false),
				PlusActive:        boolPtr(true),
			}
			if normalizeTier(ref.GetTier()) != "" {
				update.Tier = ref.GetTier()
			}
			if normalizeTier(ref.GetTier()) == "plus" || ref.GetPlusActive() {
				update.Status = accountStatusActivated
				update.ErrorMessage = ""
			}
			if updateErr := s.updateAccount(ctx, update); updateErr != nil {
				data["account_update_error"] = updateErr.Error()
				output.Data = protoData(data)
				return data, updateErr
			}
			data["account_updated"] = true
			output.Data = protoData(data)
			return data, nil
		}
		if sessionToken == "" && accessToken == "" {
			output.Data = protoData(data)
			return data, fmt.Errorf("session_token or access_token is required")
		}

		resp, callErr := s.paymentClient.ProbePlusTrial(ctx, &pb.ProbePlusTrialPaymentRequest{
			SessionToken: sessionToken,
			AccessToken:  accessToken,
		})
		data["payment_probe"] = plusTrialProbeData(resp)
		if resp != nil {
			output.Success = resp.GetSuccess()
			output.Checked = resp.GetChecked()
			output.PlusTrialEligible = resp.GetPlusTrialEligible()
			output.PlusActive = resp.GetPlusActive()
			output.Amount = resp.GetAmount()
			output.Currency = resp.GetCurrency()
			output.Source = resp.GetSource()
			output.PlanType = resp.GetPlanType()
			output.CheckoutUrl = resp.GetCheckoutUrl()
			output.CheckoutSessionId = resp.GetCheckoutSessionId()
			output.ErrorMessage = resp.GetErrorMessage()
			data["success"] = resp.GetSuccess()
			data["checked"] = resp.GetChecked()
			data["plus_trial_eligible"] = resp.GetPlusTrialEligible()
			data["plus_active"] = resp.GetPlusActive()
			data["plan_type"] = resp.GetPlanType()
			data["amount"] = resp.GetAmount()
			data["currency"] = resp.GetCurrency()
			data["source"] = resp.GetSource()
			data["checkout_url"] = resp.GetCheckoutUrl()
			data["checkout_session_id"] = resp.GetCheckoutSessionId()
			data["error_message"] = resp.GetErrorMessage()
		}
		if callErr != nil {
			output.Data = protoData(data)
			return data, callErr
		}
		if resp == nil {
			output.Data = protoData(data)
			return data, fmt.Errorf("payment service returned empty probe response")
		}
		if !resp.GetSuccess() {
			msg := resp.GetErrorMessage()
			if msg == "" {
				msg = "plus trial probe failed"
			}
			output.Data = protoData(data)
			return data, fmt.Errorf("%s", msg)
		}
		if resp.GetChecked() {
			tier := normalizeTier(resp.GetPlanType())
			if tier == "" && !resp.GetPlusActive() {
				tier = "free"
			}
			update := &pb.Account{
				AccountId:         input.GetAccountId(),
				PlusTrialEligible: boolPtr(resp.GetPlusTrialEligible()),
				PlusActive:        boolPtr(resp.GetPlusActive()),
				Tier:              tier,
			}
			if resp.GetPlusActive() {
				update.Status = accountStatusActivated
				update.ErrorMessage = ""
			}
			if updateErr := s.updateAccount(ctx, update); updateErr != nil {
				data["account_update_error"] = updateErr.Error()
				output.Data = protoData(data)
				return data, updateErr
			}
			data["account_updated"] = true
		}
		output.Data = protoData(data)
		return data, nil
	})
	if err != nil {
		return output, err
	}
	return output, nil
}

func (s *orchestratorServer) ProbeTierAtomicActivity(ctx context.Context, input ProbeTierActivityInput) (ProbeTierActivityOutput, error) {
	account, err := s.getAccount(ctx, input.GetAccountId())
	if err != nil {
		return ProbeTierActivityOutput{}, err
	}
	if err := rejectUserAlreadyExistsAccount(account); err != nil {
		return ProbeTierActivityOutput{}, err
	}

	var output ProbeTierActivityOutput
	step := s.activityStep(ctx, input.GetJobId(), stepProbeTier, false, true)
	_, err = step.run(func() (any, error) {
		sessionToken := strings.TrimSpace(account.GetSessionToken())
		data := map[string]any{
			"account_id":            account.GetAccountId(),
			"session_token_present": sessionToken != "",
		}
		if sessionToken == "" {
			output.Data = protoData(data)
			return data, fmt.Errorf("session_token is required")
		}
		resp, callErr := s.paymentClient.ProbeTier(ctx, &pb.ProbeTierPaymentRequest{
			SessionToken: sessionToken,
		})
		data["tier_probe"] = tierProbeData(resp)
		if resp != nil {
			output.Success = resp.GetSuccess()
			output.Checked = resp.GetChecked()
			output.Tier = normalizeTier(resp.GetTier())
			output.PlusActive = resp.GetPlusActive()
			output.Source = resp.GetSource()
			output.ErrorMessage = resp.GetErrorMessage()
			data["success"] = resp.GetSuccess()
			data["checked"] = resp.GetChecked()
			data["tier"] = output.Tier
			data["plus_active"] = resp.GetPlusActive()
			data["source"] = resp.GetSource()
			data["error_message"] = resp.GetErrorMessage()
		}
		if callErr != nil {
			output.Data = protoData(data)
			return data, callErr
		}
		if resp == nil {
			output.Data = protoData(data)
			return data, fmt.Errorf("payment service returned empty tier response")
		}
		if !resp.GetSuccess() {
			msg := resp.GetErrorMessage()
			if msg == "" {
				msg = "tier probe failed"
			}
			output.Data = protoData(data)
			return data, fmt.Errorf("%s", msg)
		}
		if resp.GetChecked() {
			update := &pb.Account{
				AccountId:  input.GetAccountId(),
				Tier:       output.Tier,
				PlusActive: boolPtr(resp.GetPlusActive()),
			}
			if resp.GetPlusActive() {
				update.Status = accountStatusActivated
				update.ErrorMessage = ""
			}
			if updateErr := s.updateAccount(ctx, update); updateErr != nil {
				data["account_update_error"] = updateErr.Error()
				output.Data = protoData(data)
				return data, updateErr
			}
			data["account_updated"] = true
		}
		output.Data = protoData(data)
		return data, nil
	})
	if err != nil {
		return output, err
	}
	return output, nil
}
