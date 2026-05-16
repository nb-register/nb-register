package runtime

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"orchestrator/pb"
)

const (
	accountStatusDeactivated      = "DEACTIVATED"
	openAIDeactivationSender      = "trustandsafety@tm.openai.com"
	openAIDeactivationSubject     = "Access Deactivated"
	openAIDeactivationErrorFormat = "OpenAI access deactivated notice received for %s"
)

var inboxEmailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)

func (s *orchestratorServer) FetchMailboxInboxes(ctx context.Context, req *pb.FetchMailboxInboxesRequest) (*pb.FetchMailboxInboxesResponse, error) {
	resp, err := s.emailClient.FetchInboxes(ctx, &pb.FetchInboxesRequest{
		LimitPerMailbox: req.GetLimitPerMailbox(),
		MaxMailboxes:    req.GetMaxMailboxes(),
		EmailAddress:    req.GetEmailAddress(),
	})
	if err != nil {
		return nil, err
	}

	out := &pb.FetchMailboxInboxesResponse{}
	if resp != nil {
		out.Results = resp.GetResults()
		out.MailboxCount = resp.GetMailboxCount()
		out.FetchedCount = resp.GetFetchedCount()
		out.FailedCount = resp.GetFailedCount()
		out.MessageCount = resp.GetMessageCount()
	}

	out.Bans = s.detectOpenAIDeactivations(ctx, out.GetResults())
	out.BanCount = int32(len(out.GetBans()))
	return out, nil
}

func (s *orchestratorServer) detectOpenAIDeactivations(ctx context.Context, results []*pb.FetchMailboxInboxResult) []*pb.MailboxBanDetection {
	detections := []*pb.MailboxBanDetection{}
	for _, result := range results {
		mailboxEmail := ""
		if result.GetMailbox() != nil {
			mailboxEmail = result.GetMailbox().GetEmailAddress()
		}
		for _, message := range result.GetMessages() {
			if !isOpenAIDeactivationNotice(message) {
				continue
			}
			recipients := deactivationRecipients(message)
			if len(recipients) == 0 {
				detections = append(detections, &pb.MailboxBanDetection{
					MailboxEmail:   mailboxEmail,
					FromAddress:    normalizeInboxEmail(message.GetFromAddress()),
					Subject:        strings.TrimSpace(message.GetSubject()),
					ReceivedAtUnix: message.GetReceivedAtUnix(),
					ErrorMessage:   "recipient email not found",
				})
				continue
			}
			for _, recipient := range recipients {
				detection := &pb.MailboxBanDetection{
					EmailAddress:   recipient,
					MailboxEmail:   mailboxEmail,
					FromAddress:    normalizeInboxEmail(message.GetFromAddress()),
					Subject:        strings.TrimSpace(message.GetSubject()),
					ReceivedAtUnix: message.GetReceivedAtUnix(),
				}
				account, err := s.accountByEmail(ctx, recipient)
				if err != nil {
					detection.ErrorMessage = err.Error()
					detections = append(detections, detection)
					continue
				}
				if account == nil {
					detection.ErrorMessage = "account not found"
					detections = append(detections, detection)
					continue
				}
				detection.AccountId = account.GetAccountId()
				_, err = s.accountClient.UpdateAccount(ctx, &pb.UpdateAccountRequest{Account: &pb.Account{
					AccountId:         account.GetAccountId(),
					Status:            accountStatusDeactivated,
					ErrorMessage:      fmt.Sprintf(openAIDeactivationErrorFormat, recipient),
					SessionToken:      account.GetSessionToken(),
					AccessToken:       account.GetAccessToken(),
					Email:             account.GetEmail(),
					Password:          account.GetPassword(),
					ChargeRef:         account.GetChargeRef(),
					FirstName:         account.GetFirstName(),
					LastName:          account.GetLastName(),
					Dob:               account.GetDob(),
					CreatedAt:         account.GetCreatedAt(),
					UpdatedAt:         account.GetUpdatedAt(),
					PlusTrialEligible: account.PlusTrialEligible,
				}})
				if err != nil {
					detection.ErrorMessage = err.Error()
				} else {
					detection.AccountUpdated = true
				}
				detections = append(detections, detection)
			}
		}
	}
	return detections
}

func (s *orchestratorServer) accountByEmail(ctx context.Context, email string) (*pb.Account, error) {
	resp, err := s.accountClient.ListAccounts(ctx, &pb.ListAccountsRequest{
		Email: normalizeInboxEmail(email),
		Limit: 2,
	})
	if err != nil {
		return nil, err
	}
	accounts := resp.GetAccounts()
	if len(accounts) == 0 {
		return nil, nil
	}
	return accounts[0], nil
}

func isOpenAIDeactivationNotice(message *pb.EmailInboxMessage) bool {
	if message == nil {
		return false
	}
	subject := strings.ToLower(strings.TrimSpace(message.GetSubject()))
	return normalizeInboxEmail(message.GetFromAddress()) == openAIDeactivationSender &&
		strings.Contains(subject, strings.ToLower(openAIDeactivationSubject))
}

func deactivationRecipients(message *pb.EmailInboxMessage) []string {
	if message == nil {
		return nil
	}
	seen := map[string]struct{}{}
	out := []string{}
	for _, value := range message.GetRecipients() {
		for _, match := range inboxEmailPattern.FindAllString(value, -1) {
			email := normalizeInboxEmail(match)
			if email == "" {
				continue
			}
			if _, ok := seen[email]; ok {
				continue
			}
			seen[email] = struct{}{}
			out = append(out, email)
		}
	}
	return out
}

func normalizeInboxEmail(value string) string {
	match := inboxEmailPattern.FindString(strings.TrimSpace(value))
	if match == "" {
		return strings.ToLower(strings.TrimSpace(value))
	}
	return strings.ToLower(match)
}
