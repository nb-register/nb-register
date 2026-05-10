package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"orchestrator/db"
	"orchestrator/pb"
)

func boolPtr(value bool) *bool {
	return &value
}

func isFreeTrialIneligibleError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "checkout amount") && strings.Contains(text, "not free-trial 0")
}

func (s *orchestratorServer) CreateJobActivity(ctx context.Context, input CreateJobInput) error {
	_, err := s.createJobWithID(ctx, input.JobID, input.AccountID, input.Action, input.Params)
	return err
}

func (s *orchestratorServer) EnsureAccountActivity(ctx context.Context, input EnsureAccountInput) (AccountRef, error) {
	spec := input.Account
	if spec.AccountID == "" {
		return AccountRef{}, fmt.Errorf("account_id is required")
	}

	if account, err := s.getAccount(ctx, spec.AccountID); err == nil {
		if strings.TrimSpace(account.GetEmail()) == "" {
			email, err := s.acquireEmail(ctx, nil)
			if err != nil {
				return AccountRef{}, err
			}
			if err := s.updateAccount(ctx, &pb.Account{
				AccountId: spec.AccountID,
				Email:     email,
				Status:    statusCreated,
			}); err != nil {
				return AccountRef{}, err
			}
		}
		return AccountRef{AccountID: account.GetAccountId()}, nil
	}

	email := spec.Email
	if strings.TrimSpace(email) == "" {
		var err error
		email, err = s.acquireEmail(ctx, nil)
		if err != nil {
			return AccountRef{}, err
		}
	}

	resp, err := s.accountClient.CreateAccount(ctx, &pb.CreateAccountRequest{Account: &pb.Account{
		AccountId: spec.AccountID,
		Email:     email,
		Password:  spec.Password,
		Status:    statusCreated,
	}})
	if err != nil {
		if account, getErr := s.getAccount(ctx, spec.AccountID); getErr == nil {
			return AccountRef{AccountID: account.GetAccountId()}, nil
		}
		return AccountRef{}, err
	}
	if resp.GetAccount() == nil || resp.GetAccount().GetAccountId() == "" {
		return AccountRef{}, fmt.Errorf("account-db returned empty account")
	}
	return AccountRef{AccountID: resp.GetAccount().GetAccountId()}, nil
}

func (s *orchestratorServer) acquireEmail(ctx context.Context, excludes []string) (string, error) {
	resp, err := s.emailClient.GetEmail(ctx, &pb.GetEmailRequest{
		ExcludeEmailAddresses: excludes,
	})
	if err != nil {
		return "", err
	}
	email := strings.TrimSpace(resp.GetEmailAddress())
	if email == "" && resp.GetMailbox() != nil {
		email = strings.TrimSpace(resp.GetMailbox().GetEmailAddress())
	}
	if email == "" {
		return "", fmt.Errorf("email service returned empty email")
	}
	return email, nil
}

func (s *orchestratorServer) ResolveAccountFromJobActivity(ctx context.Context, input ResolveAccountInput) (AccountRef, error) {
	if input.AccountID != "" {
		account, err := s.getAccount(ctx, input.AccountID)
		if err != nil {
			return AccountRef{}, err
		}
		return AccountRef{AccountID: account.GetAccountId()}, nil
	}
	job, err := s.getJob(ctx, input.SourceJobID)
	if err != nil {
		return AccountRef{}, err
	}
	account, err := s.getAccount(ctx, job.AccountID)
	if err != nil {
		return AccountRef{}, err
	}
	return AccountRef{AccountID: account.GetAccountId()}, nil
}

func (s *orchestratorServer) RegisterAccountAtomicActivity(ctx context.Context, input RegisterActivityInput) (RegisterActivityOutput, error) {
	account, err := s.getAccount(ctx, input.AccountID)
	if err != nil {
		return RegisterActivityOutput{}, err
	}

	var result *pb.RegisterResponse
	var data map[string]any
	_, err = s.runAtomicStep(ctx, input.JobID, stepRegisterAccount, false, true, func() (any, error) {
		var stepErr error
		result, data, stepErr = s.registerWithMailboxRotation(ctx, input.JobID, input.AccountID, account)
		return data, stepErr
	})
	if err != nil {
		if isAccountAlreadyExistsError(err) {
			if data == nil {
				data = map[string]any{}
			}
			data["terminal_reason"] = "email_already_registered"
			updateErr := s.updateAccount(ctx, &pb.Account{
				AccountId:    input.AccountID,
				Status:       accountStatusEmailAlreadyExists,
				ErrorMessage: err.Error(),
			})
			if updateErr != nil {
				return RegisterActivityOutput{Data: data}, fmt.Errorf("%w; additionally failed to mark email already exists: %v", err, updateErr)
			}
		}
		return RegisterActivityOutput{Data: data}, err
	}
	return RegisterActivityOutput{
		SessionToken:      result.GetSessionToken(),
		AccessToken:       result.GetAccessToken(),
		DeviceID:          result.GetDeviceId(),
		PlusTrialEligible: result.GetPlusTrialEligible(),
		PlusTrialChecked:  result.GetPlusTrialChecked(),
		CheckoutURL:       result.GetCheckoutUrl(),
		Data:              data,
	}, nil
}

func (s *orchestratorServer) registerWithMailboxRotation(ctx context.Context, jobID, accountID string, account *pb.Account) (*pb.RegisterResponse, map[string]any, error) {
	data := map[string]any{
		"account_id": accountID,
		"attempts":   []map[string]any{},
	}
	current := account
	var lastErr error

	for attempt := 1; ; attempt++ {
		result, attemptData, err := s.register(ctx, jobID, current)
		if attemptData == nil {
			attemptData = map[string]any{}
		}
		attemptData["attempt"] = attempt
		attemptData["email"] = current.GetEmail()
		data["email"] = current.GetEmail()
		data["attempts"] = append(data["attempts"].([]map[string]any), attemptData)
		if err == nil {
			data["browser_complete"] = attemptData["browser_complete"]
			_, _ = s.emailClient.MarkEmailStatus(ctx, &pb.MarkEmailStatusRequest{
				EmailAddress: current.GetEmail(),
				Status:       emailStatusRegistered,
			})
			return result, data, nil
		}

		lastErr = err
		data["last_error"] = err.Error()
		if !isAccountAlreadyExistsError(err) {
			_, _ = s.emailClient.MarkEmailStatus(ctx, &pb.MarkEmailStatusRequest{
				EmailAddress: current.GetEmail(),
				Status:       emailStatusRegistrationFail,
				LastError:    err.Error(),
			})
			return nil, data, err
		}

		attemptData["terminal_reason"] = "email_already_registered"

		nextAccount, rotationData, rotateErr := s.rotateAccountMailbox(ctx, current, err)
		attemptData["mailbox_rotation"] = rotationData
		if rotateErr != nil {
			data["terminal_reason"] = "email_already_registered"
			return nil, data, fmt.Errorf("%w; additionally failed to acquire replacement mailbox: %v", err, rotateErr)
		}
		current = nextAccount
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("registration failed")
	}
	data["terminal_reason"] = "email_already_registered"
	return nil, data, lastErr
}

func (s *orchestratorServer) rotateAccountMailbox(ctx context.Context, account *pb.Account, cause error) (*pb.Account, map[string]any, error) {
	currentEmail := account.GetEmail()
	rotationData := map[string]any{
		"from_email": currentEmail,
	}

	if currentEmail != "" {
		_, err := s.emailClient.MarkEmailStatus(ctx, &pb.MarkEmailStatusRequest{
			EmailAddress: currentEmail,
			Status:       emailStatusUserAlreadyExists,
			LastError:    cause.Error(),
		})
		if err != nil && status.Code(err) != codes.NotFound {
			rotationData["release_error"] = err.Error()
			return nil, rotationData, err
		}
		if err == nil {
			rotationData["released"] = true
		}
	}

	email, err := s.acquireEmail(ctx, []string{currentEmail})
	if err != nil {
		rotationData["acquire_error"] = err.Error()
		return nil, rotationData, err
	}

	rotationData["to_email"] = email
	if err := s.updateAccount(ctx, &pb.Account{
		AccountId:    account.GetAccountId(),
		Email:        email,
		Status:       statusCreated,
		ErrorMessage: "",
	}); err != nil {
		rotationData["account_update_error"] = err.Error()
		return nil, rotationData, err
	}

	nextAccount, err := s.getAccount(ctx, account.GetAccountId())
	if err != nil {
		rotationData["account_reload_error"] = err.Error()
		return nil, rotationData, err
	}
	return nextAccount, rotationData, nil
}

func (s *orchestratorServer) GoPayPaymentAtomicActivity(ctx context.Context, input GoPayActivityInput) (GoPayActivityOutput, error) {
	account, err := s.getAccount(ctx, input.AccountID)
	if err != nil {
		return GoPayActivityOutput{}, err
	}

	var result *pb.GoPayResponse
	var data map[string]any
	_, err = s.runAtomicStep(ctx, input.JobID, stepGoPayPayment, false, true, func() (any, error) {
		var stepErr error
		result, data, stepErr = s.pay(ctx, account, input.SessionToken, input.AccessToken)
		return data, stepErr
	})
	if err != nil {
		if isFreeTrialIneligibleError(err) {
			if updateErr := s.updateAccount(ctx, &pb.Account{
				AccountId:         input.AccountID,
				PlusTrialEligible: boolPtr(false),
			}); updateErr != nil {
				return GoPayActivityOutput{Data: data}, fmt.Errorf("%w; additionally failed to mark plus trial ineligible: %v", err, updateErr)
			}
		}
		return GoPayActivityOutput{Data: data}, err
	}
	return GoPayActivityOutput{
		ChargeRef:         result.GetChargeRef(),
		SnapToken:         result.GetSnapToken(),
		PlusTrialEligible: true,
		PlusTrialChecked:  true,
		Data:              data,
	}, nil
}

func (s *orchestratorServer) ProbePlusTrialAtomicActivity(ctx context.Context, input ProbePlusTrialActivityInput) (ProbePlusTrialActivityOutput, error) {
	account, err := s.getAccount(ctx, input.AccountID)
	if err != nil {
		return ProbePlusTrialActivityOutput{}, err
	}

	var output ProbePlusTrialActivityOutput
	_, err = s.runAtomicStep(ctx, input.JobID, stepProbePlusTrial, false, true, func() (any, error) {
		sessionToken := strings.TrimSpace(account.GetSessionToken())
		accessToken := strings.TrimSpace(account.GetAccessToken())
		data := map[string]any{
			"account_id":            account.GetAccountId(),
			"session_token_present": sessionToken != "",
			"access_token_present":  accessToken != "",
		}
		output.Data = data
		if sessionToken == "" && accessToken == "" {
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
			output.Amount = resp.GetAmount()
			output.Currency = resp.GetCurrency()
			output.Source = resp.GetSource()
			output.CheckoutURL = resp.GetCheckoutUrl()
			output.ErrorMessage = resp.GetErrorMessage()
			data["success"] = resp.GetSuccess()
			data["checked"] = resp.GetChecked()
			data["plus_trial_eligible"] = resp.GetPlusTrialEligible()
			data["amount"] = resp.GetAmount()
			data["currency"] = resp.GetCurrency()
			data["source"] = resp.GetSource()
			data["checkout_url"] = resp.GetCheckoutUrl()
			data["error_message"] = resp.GetErrorMessage()
		}
		if callErr != nil {
			return data, callErr
		}
		if resp == nil {
			return data, fmt.Errorf("payment service returned empty probe response")
		}
		if !resp.GetSuccess() {
			msg := resp.GetErrorMessage()
			if msg == "" {
				msg = "plus trial probe failed"
			}
			return data, fmt.Errorf("%s", msg)
		}
		if resp.GetChecked() {
			if updateErr := s.updateAccount(ctx, &pb.Account{
				AccountId:         input.AccountID,
				PlusTrialEligible: boolPtr(resp.GetPlusTrialEligible()),
			}); updateErr != nil {
				data["account_update_error"] = updateErr.Error()
				return data, updateErr
			}
			data["account_updated"] = true
		}
		return data, nil
	})
	if err != nil {
		return output, err
	}
	return output, nil
}

func (s *orchestratorServer) RegisterMailboxAtomicActivity(ctx context.Context, input MailboxRegistrationActivityInput) (MailboxRegistrationActivityOutput, error) {
	var output MailboxRegistrationActivityOutput
	_, err := s.runAtomicStep(ctx, input.JobID, stepRegisterMailbox, false, true, func() (any, error) {
		resp, callErr := s.mailboxRegisterClient.RunMailboxRegistration(ctx, &pb.RunMailboxRegistrationRequest{
			Enabled:    input.Enabled,
			ImportOnly: input.ImportOnly,
		})
		data := map[string]any{
			"enabled":        input.Enabled,
			"import_only":    input.ImportOnly,
			"mailboxes":      []map[string]any{},
			"account_count":  0,
			"imported_count": 0,
		}
		if resp != nil {
			output.Success = resp.GetSuccess()
			output.ExitCode = resp.GetExitCode()
			output.ErrorMessage = resp.GetErrorMessage()
			data["success"] = resp.GetSuccess()
			data["exit_code"] = resp.GetExitCode()
			data["error_message"] = resp.GetErrorMessage()
			data["account_count"] = len(resp.GetAccounts())
		}
		output.Data = data
		if callErr != nil {
			return data, callErr
		}
		if resp == nil {
			return data, fmt.Errorf("mailbox registration service returned empty response")
		}
		if !resp.GetSuccess() {
			msg := resp.GetErrorMessage()
			if msg == "" {
				msg = fmt.Sprintf("mailbox registration failed with exit code %d", resp.GetExitCode())
			}
			return data, fmt.Errorf("%s", msg)
		}
		if len(resp.GetAccounts()) == 0 {
			msg := "mailbox registration returned no accounts"
			output.Success = false
			output.ErrorMessage = msg
			data["success"] = false
			data["error_message"] = msg
			return data, fmt.Errorf("%s", msg)
		}

		imported := make([]map[string]any, 0, len(resp.GetAccounts()))
		for _, account := range resp.GetAccounts() {
			email := strings.ToLower(strings.TrimSpace(account.GetEmailAddress()))
			password := strings.TrimSpace(account.GetPassword())
			if email == "" {
				msg := "mailbox registration returned account without email"
				output.Success = false
				output.ErrorMessage = msg
				data["success"] = false
				data["error_message"] = msg
				return data, fmt.Errorf("%s", msg)
			}
			if password == "" {
				msg := fmt.Sprintf("mailbox registration returned %s without password", email)
				output.Success = false
				output.ErrorMessage = msg
				data["success"] = false
				data["error_message"] = msg
				return data, fmt.Errorf("%s", msg)
			}

			refreshToken := strings.TrimSpace(account.GetRefreshToken())
			accessToken := strings.TrimSpace(account.GetAccessToken())
			statusValue := emailStatusAvailable
			if refreshToken == "" {
				statusValue = emailStatusOAuthPending
			}
			upsertResp, upsertErr := s.emailClient.UpsertMailbox(ctx, &pb.UpsertEmailMailboxRequest{
				Mailbox: &pb.EmailMailbox{
					EmailAddress: email,
					Password:     password,
					RefreshToken: refreshToken,
					AccessToken:  accessToken,
					Status:       statusValue,
					LastError:    "",
					IsPrimary:    true,
					PrimaryEmail: email,
				},
			})
			if upsertErr != nil {
				output.Success = false
				output.ErrorMessage = upsertErr.Error()
				data["success"] = false
				data["error_message"] = upsertErr.Error()
				return data, upsertErr
			}
			if upsertResp == nil || upsertResp.GetMailbox() == nil || strings.TrimSpace(upsertResp.GetMailbox().GetEmailAddress()) == "" {
				msg := "email service returned empty mailbox after import"
				output.Success = false
				output.ErrorMessage = msg
				data["success"] = false
				data["error_message"] = msg
				return data, fmt.Errorf("%s", msg)
			}
			importedMailbox := RegisteredMailboxResult{
				EmailAddress: upsertResp.GetMailbox().GetEmailAddress(),
				Status:       upsertResp.GetMailbox().GetStatus(),
			}
			output.Mailboxes = append(output.Mailboxes, importedMailbox)
			imported = append(imported, map[string]any{
				"email_address": importedMailbox.EmailAddress,
				"status":        importedMailbox.Status,
			})
		}
		output.Success = len(output.Mailboxes) > 0
		data["success"] = output.Success
		data["imported_count"] = len(output.Mailboxes)
		data["mailboxes"] = imported
		return data, nil
	})
	if err != nil {
		return output, err
	}
	return output, nil
}

func (s *orchestratorServer) MailboxOAuthAtomicActivity(ctx context.Context, input MailboxOAuthActivityInput) (MailboxOAuthActivityOutput, error) {
	var output MailboxOAuthActivityOutput
	_, err := s.runAtomicStep(ctx, input.JobID, stepMailboxOAuth, false, true, func() (any, error) {
		accounts, selectErr := s.mailboxOAuthAccounts(ctx, input)
		data := map[string]any{
			"email_address": strings.TrimSpace(input.EmailAddress),
			"only_missing":  input.OnlyMissing,
			"limit":         input.Limit,
			"account_count": len(accounts),
			"results":       []map[string]any{},
		}
		if selectErr != nil {
			data["error_message"] = selectErr.Error()
			return data, selectErr
		}
		resp, callErr := s.mailboxRegisterClient.RunMailboxOAuth(ctx, &pb.RunMailboxOAuthRequest{
			EmailAddress: strings.TrimSpace(input.EmailAddress),
			OnlyMissing:  input.OnlyMissing,
			Limit:        input.Limit,
			Accounts:     accounts,
		})
		if resp != nil {
			output.Success = resp.GetSuccess()
			output.Processed = resp.GetProcessed()
			output.Succeeded = resp.GetSucceeded()
			output.Failed = resp.GetFailed()
			output.ErrorMessage = resp.GetErrorMessage()
			results := make([]map[string]any, 0, len(resp.GetResults()))
			for _, item := range resp.GetResults() {
				results = append(results, map[string]any{
					"email_address":     item.GetEmailAddress(),
					"success":           item.GetSuccess(),
					"error_message":     item.GetErrorMessage(),
					"has_refresh_token": strings.TrimSpace(item.GetRefreshToken()) != "",
				})
			}
			data["success"] = resp.GetSuccess()
			data["processed"] = resp.GetProcessed()
			data["succeeded"] = resp.GetSucceeded()
			data["failed"] = resp.GetFailed()
			data["error_message"] = resp.GetErrorMessage()
			data["results"] = results
		}
		output.Data = data
		if callErr != nil {
			return data, callErr
		}
		if resp == nil {
			return data, fmt.Errorf("mailbox registration service returned empty OAuth response")
		}
		for _, item := range resp.GetResults() {
			email := strings.ToLower(strings.TrimSpace(item.GetEmailAddress()))
			refreshToken := strings.TrimSpace(item.GetRefreshToken())
			if item.GetSuccess() && refreshToken != "" {
				if _, upsertErr := s.emailClient.UpsertMailbox(ctx, &pb.UpsertEmailMailboxRequest{
					Mailbox: &pb.EmailMailbox{
						EmailAddress: email,
						RefreshToken: refreshToken,
						AccessToken:  strings.TrimSpace(item.GetAccessToken()),
						Status:       emailStatusAvailable,
						LastError:    "",
						IsPrimary:    true,
						PrimaryEmail: email,
					},
				}); upsertErr != nil {
					output.Success = false
					output.ErrorMessage = upsertErr.Error()
					data["success"] = false
					data["error_message"] = upsertErr.Error()
					return data, upsertErr
				}
				continue
			}
			if email != "" && !item.GetSuccess() {
				_, _ = s.emailClient.MarkEmailStatus(ctx, &pb.MarkEmailStatusRequest{
					EmailAddress: email,
					Status:       emailStatusAuthFailed,
					LastError:    strings.TrimSpace(item.GetErrorMessage()),
				})
			}
		}
		if !resp.GetSuccess() {
			msg := resp.GetErrorMessage()
			if msg == "" {
				msg = fmt.Sprintf("mailbox OAuth failed: %d/%d", resp.GetFailed(), resp.GetProcessed())
			}
			output.ErrorMessage = msg
			data["error_message"] = msg
			return data, fmt.Errorf("%s", msg)
		}
		return data, nil
	})
	if err != nil {
		return output, err
	}
	return output, nil
}

func (s *orchestratorServer) mailboxOAuthAccounts(ctx context.Context, input MailboxOAuthActivityInput) ([]*pb.MailboxRegistrationAccount, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	requestedEmail := strings.ToLower(strings.TrimSpace(input.EmailAddress))
	if requestedEmail != "" {
		limit = 500
	}

	resp, err := s.emailClient.ListMailboxes(ctx, &pb.ListEmailMailboxesRequest{Limit: limit})
	if err != nil {
		return nil, err
	}

	accounts := make([]*pb.MailboxRegistrationAccount, 0)
	for _, mailbox := range resp.GetMailboxes() {
		email := strings.ToLower(strings.TrimSpace(mailbox.GetEmailAddress()))
		if email == "" {
			continue
		}
		if requestedEmail != "" && email != requestedEmail {
			continue
		}
		if !mailbox.GetIsPrimary() {
			continue
		}
		if strings.TrimSpace(mailbox.GetPassword()) == "" {
			continue
		}
		if input.OnlyMissing && strings.TrimSpace(mailbox.GetRefreshToken()) != "" {
			continue
		}
		accounts = append(accounts, &pb.MailboxRegistrationAccount{
			EmailAddress: email,
			Password:     strings.TrimSpace(mailbox.GetPassword()),
			RefreshToken: strings.TrimSpace(mailbox.GetRefreshToken()),
			AccessToken:  strings.TrimSpace(mailbox.GetAccessToken()),
			Source:       "mailboxes",
		})
		if requestedEmail == "" && len(accounts) >= int(limit) {
			break
		}
	}

	if requestedEmail != "" && len(accounts) == 0 {
		return nil, fmt.Errorf("mailbox not found or not eligible for OAuth: %s", requestedEmail)
	}
	return accounts, nil
}

func (s *orchestratorServer) PersistRegisteredActivity(ctx context.Context, input PersistRegisteredInput) error {
	account := &pb.Account{
		AccountId:    input.AccountID,
		Status:       "REGISTERED",
		SessionToken: input.SessionToken,
		AccessToken:  input.AccessToken,
	}
	if input.PlusTrialChecked {
		account.PlusTrialEligible = boolPtr(input.PlusTrialEligible)
	}
	return s.updateAccount(ctx, account)
}

func (s *orchestratorServer) PersistActivatedActivity(ctx context.Context, input PersistActivatedInput) error {
	account, err := s.getAccount(ctx, input.AccountID)
	if err != nil {
		return err
	}
	sessionToken := input.SessionToken
	if sessionToken == "" {
		sessionToken = account.GetSessionToken()
	}
	accessToken := input.AccessToken
	if accessToken == "" {
		accessToken = account.GetAccessToken()
	}
	update := &pb.Account{
		AccountId:    input.AccountID,
		Status:       "ACTIVATED",
		SessionToken: sessionToken,
		AccessToken:  accessToken,
		ChargeRef:    input.ChargeRef,
	}
	if input.PlusTrialChecked {
		update.PlusTrialEligible = boolPtr(input.PlusTrialEligible)
	}
	return s.updateAccount(ctx, update)
}

func (s *orchestratorServer) MarkJobFailedActivity(ctx context.Context, input JobFailureInput) error {
	if input.Status == "" {
		input.Status = failedStatus(input.Recoverable, input.Retryable)
	}
	s.updateJob(ctx, input.JobID, input.Status, input.ErrorMessage, input.Result)
	if input.StepName != "" {
		return s.markStepFailed(ctx, input)
	}
	return nil
}

func (s *orchestratorServer) MarkJobSucceededActivity(ctx context.Context, input JobSuccessInput) error {
	s.updateJob(ctx, input.JobID, statusSucceeded, "", input.Result)
	return nil
}

func (s *orchestratorServer) createJobWithID(ctx context.Context, jobID, accountID, action string, params map[string]string) (*db.Job, error) {
	job := &db.Job{
		ID:        jobID,
		AccountID: accountID,
		Action:    action,
		Status:    statusCreated,
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(job).Error; err != nil {
			return err
		}
		if err := upsertJobParams(ctx, tx, jobID, params); err != nil {
			return err
		}
		return tx.First(job, "id = ?", jobID).Error
	})
	if err != nil {
		return nil, err
	}
	return job, nil
}

func (s *orchestratorServer) markStepFailed(ctx context.Context, input JobFailureInput) error {
	now := time.Now().Unix()
	step := db.JobStep{
		JobID:        input.JobID,
		StepName:     input.StepName,
		Status:       input.Status,
		Recoverable:  input.Recoverable,
		Retryable:    input.Retryable,
		ErrorMessage: input.ErrorMessage,
		CompletedAt:  now,
	}
	updates := map[string]any{
		"status":        input.Status,
		"recoverable":   input.Recoverable,
		"retryable":     input.Retryable,
		"error_message": input.ErrorMessage,
		"completed_at":  now,
	}
	if len(input.Result) > 0 {
		updates["result_json"] = marshalStepResult(input.JobID, input.StepName, input.Result)
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "job_id"}, {Name: "step_name"}},
		DoUpdates: clause.Assignments(updates),
	}).Create(&step).Error
}
