package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"

	"dashboard/pb"
)

type server struct {
	accountClient         pb.AccountDatabaseServiceClient
	accountWorkflowClient pb.AccountWorkflowServiceClient
	paymentWorkflowClient pb.PaymentWorkflowServiceClient
	gopayAppClient        pb.GoPayAppWorkflowServiceClient
	mailboxClient         pb.MailboxWorkflowServiceClient
	otpClient             pb.OTPServiceClient
	jobClient             pb.JobServiceClient
	paymentClient         pb.PaymentServiceClient
	emailClient           pb.EmailServiceClient
	staticDir             string
	mailboxRegisterMu     sync.Mutex
	mailboxRegistering    bool
}

type createAccountRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type upsertMailboxRequest struct {
	MailboxID    string `json:"mailbox_id"`
	Email        string `json:"email"`
	Password     string `json:"password"`
	RefreshToken string `json:"refresh_token"`
	AccessToken  string `json:"access_token"`
	Status       string `json:"status"`
	AuthStatus   string `json:"auth_status"`
	LastError    string `json:"last_error"`
}

type mailboxOAuthRequest struct {
	EmailAddress string `json:"email_address"`
	OnlyMissing  bool   `json:"only_missing"`
	Limit        int32  `json:"limit"`
}

type mailboxInboxRequest struct {
	LimitPerMailbox int32  `json:"limit_per_mailbox"`
	MaxMailboxes    int32  `json:"max_mailboxes"`
	EmailAddress    string `json:"email_address"`
}

type submitJobOTPRequest struct {
	OTP string `json:"otp"`
}

type updateAccountRequest struct {
	SessionToken      string  `json:"session_token"`
	AccessToken       string  `json:"access_token"`
	ActivationChannel *string `json:"activation_channel"`
}

const (
	nextAuthSessionCookieName         = "__Secure-next-auth.session-token"
	nextAuthSessionCookieFallbackName = "next-auth.session-token"
	nextAuthSessionCookieChunkSize    = 4096 - 163
)

func main() {
	accountConn, err := newGRPCClient(envDefault("ACCOUNT_DB_ADDR", "account-db:50051"))
	if err != nil {
		log.Fatalf("connect account-db: %v", err)
	}
	defer accountConn.Close()

	orchestratorConn, err := newGRPCClient(envDefault("ORCHESTRATOR_ADDR", "orchestrator:50051"))
	if err != nil {
		log.Fatalf("connect orchestrator: %v", err)
	}
	defer orchestratorConn.Close()

	paymentConn, err := newGRPCClient(envDefault("PAYMENT_ADDR", "gopay-payment:50051"))
	if err != nil {
		log.Fatalf("connect payment: %v", err)
	}
	defer paymentConn.Close()

	emailConn, err := newGRPCClient(envDefault("EMAIL_ADDR", "outlook-imap-service:50051"))
	if err != nil {
		log.Fatalf("connect email service: %v", err)
	}
	defer emailConn.Close()

	s := &server{
		accountClient:         pb.NewAccountDatabaseServiceClient(accountConn),
		accountWorkflowClient: pb.NewAccountWorkflowServiceClient(orchestratorConn),
		paymentWorkflowClient: pb.NewPaymentWorkflowServiceClient(orchestratorConn),
		gopayAppClient:        pb.NewGoPayAppWorkflowServiceClient(orchestratorConn),
		mailboxClient:         pb.NewMailboxWorkflowServiceClient(orchestratorConn),
		otpClient:             pb.NewOTPServiceClient(orchestratorConn),
		jobClient:             pb.NewJobServiceClient(orchestratorConn),
		paymentClient:         pb.NewPaymentServiceClient(paymentConn),
		emailClient:           pb.NewEmailServiceClient(emailConn),
		staticDir:             envDefault("STATIC_DIR", "web/dist"),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/accounts", s.handleAccounts)
	mux.HandleFunc("/api/accounts/", s.handleAccount)
	mux.HandleFunc("/api/mailboxes/register", s.handleMailboxRegister)
	mux.HandleFunc("/api/mailboxes/oauth", s.handleMailboxOAuth)
	mux.HandleFunc("/api/mailboxes/inbox", s.handleMailboxInbox)
	mux.HandleFunc("/api/mailboxes/", s.handleMailbox)
	mux.HandleFunc("/api/mailboxes", s.handleMailboxes)
	mux.HandleFunc("/api/gpt-email-allocations", s.handleGPTEmailAllocations)
	mux.HandleFunc("/api/jobs", s.handleJobs)
	mux.HandleFunc("/api/jobs/events", s.streamJobsEvents)
	mux.HandleFunc("/api/jobs/", s.handleJob)
	mux.HandleFunc("/api/gopay/state", s.handleGoPayState)
	mux.HandleFunc("/api/workflows/register", s.handleRegister)
	mux.HandleFunc("/api/workflows/activate", s.handleActivate)
	mux.HandleFunc("/api/workflows/autopay", s.handleAutopay)
	mux.HandleFunc("/api/workflows/login", s.handleLogin)
	mux.HandleFunc("/api/workflows/probe", s.handleProbeAccount)
	mux.HandleFunc("/api/workflows/gopay-app", s.handleGoPayApp)
	mux.HandleFunc("/api/workflows/gopay-payment/rebind", s.handleGoPayPaymentRebind)
	mux.HandleFunc("/api/workflows/gopay-payment", s.handleGoPayPayment)
	mux.HandleFunc("/api/workflows/register-and-activate", s.handleRegisterAndActivate)
	mux.HandleFunc("/", s.handleStatic)

	addr := envDefault("LISTEN_ADDR", ":8080")
	log.Printf("dashboard listening on %s", addr)
	if err := http.ListenAndServe(addr, withCORS(mux)); err != nil {
		log.Fatal(err)
	}
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit := int32(queryInt(r, "limit", 100))
		resp, err := s.accountClient.ListAccounts(r.Context(), &pb.ListAccountsRequest{
			Status: r.URL.Query().Get("status"),
			Limit:  limit,
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		accounts := resp.GetAccounts()
		if accounts == nil {
			accounts = []*pb.Account{}
		}
		writeJSON(w, http.StatusOK, accounts)
	case http.MethodPost:
		var req createAccountRequest
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		accountID := randomID()
		resp, err := s.accountWorkflowClient.CreateGPTAccount(r.Context(), &pb.CreateGPTAccountRequest{
			AccountId: accountID,
			Email:     strings.TrimSpace(req.Email),
			Password:  req.Password,
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		if resp.GetErrorMessage() != "" {
			writeError(w, http.StatusBadGateway, errors.New(resp.GetErrorMessage()))
			return
		}
		writeJSON(w, http.StatusCreated, resp.GetAccount())
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *server) handleMailboxes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit := int32(queryInt(r, "limit", 100))
		resp, err := s.emailClient.ListMailboxes(r.Context(), &pb.ListEmailMailboxesRequest{
			Status: r.URL.Query().Get("status"),
			Limit:  limit,
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		mailboxes := resp.GetMailboxes()
		if mailboxes == nil {
			mailboxes = []*pb.EmailMailbox{}
		}
		writeJSON(w, http.StatusOK, mailboxes)
	case http.MethodPost:
		var req upsertMailboxRequest
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.emailClient.UpsertMailbox(r.Context(), &pb.UpsertEmailMailboxRequest{Mailbox: &pb.EmailMailbox{
			EmailAddress: req.Email,
			Password:     req.Password,
			RefreshToken: req.RefreshToken,
			AccessToken:  req.AccessToken,
			AuthStatus:   req.AuthStatus,
			LastError:    req.LastError,
			IsPrimary:    true,
			PrimaryEmail: req.Email,
		}})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		email := strings.ToLower(strings.TrimSpace(resp.GetMailbox().GetEmailAddress()))
		if email == "" {
			writeError(w, http.StatusBadGateway, errors.New("email service returned empty mailbox"))
			return
		}
		if _, err := s.accountClient.UpsertGPTEmailAllocation(r.Context(), &pb.UpsertGPTEmailAllocationRequest{
			Allocation: &pb.GPTEmailAllocation{
				Email:        email,
				PrimaryEmail: email,
				IsPrimary:    true,
				Status:       gptAllocationStatusFromMailboxInput(req.Status, req.AuthStatus, req.RefreshToken),
				Splittable:   strings.TrimSpace(req.Status) == "REGISTERED",
				LastError:    req.LastError,
			},
		}); err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusCreated, resp.GetMailbox())
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *server) handleGPTEmailAllocations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.accountClient.ListGPTEmailAllocations(r.Context(), &pb.ListGPTEmailAllocationsRequest{
		Status:       strings.TrimSpace(r.URL.Query().Get("status")),
		Limit:        int32(queryInt(r, "limit", 500)),
		PrimaryEmail: strings.TrimSpace(r.URL.Query().Get("primary_email")),
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	allocations := resp.GetAllocations()
	if allocations == nil {
		allocations = []*pb.GPTEmailAllocation{}
	}
	writeJSON(w, http.StatusOK, allocations)
}

func (s *server) handleMailbox(w http.ResponseWriter, r *http.Request) {
	emailPath := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/mailboxes/"), "/")
	email, err := url.PathUnescape(emailPath)
	if err != nil || strings.TrimSpace(email) == "" {
		writeError(w, http.StatusBadRequest, errors.New("email_address is required"))
		return
	}
	switch r.Method {
	case http.MethodDelete:
		resp, err := s.emailClient.DeleteMailbox(r.Context(), &pb.DeleteMailboxRequest{EmailAddress: strings.TrimSpace(email)})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *server) handleMailboxRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	s.mailboxRegisterMu.Lock()
	if s.mailboxRegistering {
		s.mailboxRegisterMu.Unlock()
		writeError(w, http.StatusConflict, errors.New("mailbox registration is already running"))
		return
	}
	s.mailboxRegistering = true
	s.mailboxRegisterMu.Unlock()

	go func() {
		defer func() {
			s.mailboxRegisterMu.Lock()
			s.mailboxRegistering = false
			s.mailboxRegisterMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(envInt("MAILBOX_REGISTER_TIMEOUT_SECONDS", 1800))*time.Second)
		defer cancel()
		resp, err := s.mailboxClient.RegisterMailbox(ctx, &pb.RegisterMailboxRequest{})
		if err != nil {
			log.Printf("mailbox registration workflow failed to start: %v", err)
			return
		}
		if resp.GetErrorMessage() != "" {
			log.Printf("mailbox registration workflow job=%s failed: %s", resp.GetJobId(), resp.GetErrorMessage())
			return
		}
		log.Printf("mailbox registration workflow job=%s completed success=%v exit_code=%d", resp.GetJobId(), resp.GetSuccess(), resp.GetExitCode())
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"started": true,
		"backend": "outlook-register-service",
	})
}

func (s *server) handleMailboxOAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req mailboxOAuthRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Limit <= 0 {
		req.Limit = 100
	}
	if strings.TrimSpace(req.EmailAddress) == "" {
		req.OnlyMissing = true
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := s.mailboxClient.RunMailboxOAuth(ctx, &pb.StartMailboxOAuthRequest{
		EmailAddress: strings.TrimSpace(req.EmailAddress),
		OnlyMissing:  req.OnlyMissing,
		Limit:        req.Limit,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	statusCode := http.StatusAccepted
	if !resp.GetStarted() || resp.GetErrorMessage() != "" {
		statusCode = http.StatusBadGateway
	}
	writeJSON(w, statusCode, map[string]any{
		"started":       resp.GetStarted(),
		"job_id":        resp.GetJobId(),
		"error_message": resp.GetErrorMessage(),
		"backend":       "outlook-register-service",
	})
}

func (s *server) handleMailboxInbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req mailboxInboxRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.LimitPerMailbox <= 0 {
		req.LimitPerMailbox = 10
	}
	if req.LimitPerMailbox > 100 {
		req.LimitPerMailbox = 100
	}
	if req.MaxMailboxes <= 0 {
		req.MaxMailboxes = 100
	}
	if req.MaxMailboxes > 500 {
		req.MaxMailboxes = 500
	}

	timeout := envInt("MAILBOX_INBOX_TIMEOUT_SECONDS", 180)
	if timeout < 30 {
		timeout = 30
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeout)*time.Second)
	defer cancel()

	resp, err := s.mailboxClient.FetchMailboxInboxes(ctx, &pb.FetchMailboxInboxesRequest{
		LimitPerMailbox: req.LimitPerMailbox,
		MaxMailboxes:    req.MaxMailboxes,
		EmailAddress:    strings.TrimSpace(req.EmailAddress),
	})
	if err != nil {
		if status.Code(err) == codes.DeadlineExceeded {
			writeError(w, http.StatusGatewayTimeout, err)
			return
		}
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleAccount(w http.ResponseWriter, r *http.Request) {
	accountPath := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/accounts/"), "/")
	parts := strings.Split(accountPath, "/")
	accountID := parts[0]
	if accountID == "" {
		writeError(w, http.StatusBadRequest, errors.New("account_id is required"))
		return
	}
	if len(parts) > 1 {
		if len(parts) == 2 && parts[1] == "access-token" {
			s.handleAccountAccessToken(w, r, accountID)
			return
		}
		if len(parts) == 2 && parts[1] == "checkout-link" {
			s.handleAccountCheckoutLink(w, r, accountID)
			return
		}
		writeError(w, http.StatusNotFound, errors.New("account endpoint not found"))
		return
	}

	switch r.Method {
	case http.MethodGet:
		resp, err := s.accountClient.GetAccount(r.Context(), &pb.GetAccountRequest{AccountId: accountID})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, resp.GetAccount())
	case http.MethodPatch, http.MethodPut:
		var req updateAccountRequest
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		sessionToken, accessToken := normalizeAccountAuthInput(req.SessionToken, req.AccessToken)
		if sessionToken == "" && accessToken == "" && req.ActivationChannel == nil {
			writeError(w, http.StatusBadRequest, errors.New("session_token, access_token, or activation_channel is required"))
			return
		}
		account := &pb.Account{
			AccountId:    accountID,
			SessionToken: sessionToken,
			AccessToken:  accessToken,
		}
		if req.ActivationChannel != nil {
			activationChannel := strings.TrimSpace(*req.ActivationChannel)
			account.ActivationChannel = &activationChannel
		}
		resp, err := s.accountClient.UpdateAccount(r.Context(), &pb.UpdateAccountRequest{Account: account})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, resp.GetAccount())
	case http.MethodDelete:
		resp, err := s.accountClient.DeleteAccount(r.Context(), &pb.DeleteAccountRequest{AccountId: accountID})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *server) handleAccountAccessToken(w http.ResponseWriter, r *http.Request, accountID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	accountResp, err := s.accountClient.GetAccount(ctx, &pb.GetAccountRequest{AccountId: accountID})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	account := accountResp.GetAccount()
	if account == nil {
		writeError(w, http.StatusNotFound, errors.New("account not found"))
		return
	}
	sessionToken := strings.TrimSpace(account.GetSessionToken())
	if sessionToken == "" {
		writeError(w, http.StatusBadRequest, errors.New("session_token is required"))
		return
	}

	accessToken, err := fetchChatGPTAccessToken(ctx, sessionToken)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	updated, err := s.accountClient.UpdateAccount(ctx, &pb.UpdateAccountRequest{Account: &pb.Account{
		AccountId:   accountID,
		AccessToken: accessToken,
	}})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, updated.GetAccount())
}

func (s *server) handleAccountCheckoutLink(w http.ResponseWriter, r *http.Request, accountID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	accountResp, err := s.accountClient.GetAccount(ctx, &pb.GetAccountRequest{AccountId: accountID})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	account := accountResp.GetAccount()
	if account == nil {
		writeError(w, http.StatusNotFound, errors.New("account not found"))
		return
	}

	sessionToken := strings.TrimSpace(account.GetSessionToken())
	accessToken := strings.TrimSpace(account.GetAccessToken())
	if sessionToken == "" && accessToken == "" {
		writeError(w, http.StatusBadRequest, errors.New("session_token or access_token is required"))
		return
	}

	resp, err := s.paymentClient.CreateCheckoutLink(ctx, &pb.CreateCheckoutLinkRequest{
		SessionToken: sessionToken,
		AccessToken:  accessToken,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if !resp.GetSuccess() || resp.GetErrorMessage() != "" {
		msg := strings.TrimSpace(resp.GetErrorMessage())
		if msg == "" {
			msg = "checkout link creation failed"
		}
		writeError(w, http.StatusBadGateway, errors.New(msg))
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func fetchChatGPTAccessToken(ctx context.Context, sessionToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://chatgpt.com/api/auth/session", nil)
	if err != nil {
		return "", err
	}
	cookieHeader := chatGPTSessionCookieHeader(sessionToken)
	if cookieHeader == "" {
		return "", errors.New("session_token is required")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Referer", "https://chatgpt.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36")
	req.Header.Set("Cookie", cookieHeader)

	resp, err := (&http.Client{Timeout: 25 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch auth session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("auth session returned status %d", resp.StatusCode)
	}

	var payload struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode auth session: %w", err)
	}
	accessToken := strings.TrimSpace(payload.AccessToken)
	if accessToken == "" {
		return "", errors.New("auth session did not return access token")
	}
	return accessToken, nil
}

func normalizeAccountAuthInput(sessionInput, accessInput string) (string, string) {
	sessionToken := strings.TrimSpace(sessionInput)
	accessToken := extractAccessToken(accessInput)
	if payloadSession, payloadAccess := authSessionJSONTokens(sessionToken); payloadSession != "" || payloadAccess != "" {
		if payloadSession != "" {
			sessionToken = payloadSession
		}
		if accessToken == "" {
			accessToken = payloadAccess
		}
	}
	if payloadSession, payloadAccess := authSessionJSONTokens(accessInput); payloadSession != "" || payloadAccess != "" {
		if sessionToken == "" {
			sessionToken = payloadSession
		}
		if payloadAccess != "" {
			accessToken = payloadAccess
		}
	}
	if parsedSession := extractSessionToken(sessionToken); parsedSession != "" {
		sessionToken = parsedSession
	}
	return strings.TrimSpace(sessionToken), strings.TrimSpace(accessToken)
}

func authSessionJSONTokens(raw string) (string, string) {
	text := strings.TrimSpace(raw)
	if !strings.HasPrefix(text, "{") {
		return "", ""
	}
	var payload struct {
		SessionToken string `json:"sessionToken"`
		AccessToken  string `json:"accessToken"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return "", ""
	}
	return strings.TrimSpace(payload.SessionToken), strings.TrimSpace(payload.AccessToken)
}

func extractAccessToken(raw string) string {
	text := strings.TrimSpace(raw)
	if _, accessToken := authSessionJSONTokens(text); accessToken != "" {
		return accessToken
	}
	return text
}

func extractSessionToken(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	if sessionToken, _ := authSessionJSONTokens(text); sessionToken != "" {
		return sessionToken
	}
	exact := ""
	chunks := map[int]string{}
	for _, part := range strings.Split(text, ";") {
		name, value, ok := parseSessionCookiePart(part)
		if !ok {
			continue
		}
		if name == nextAuthSessionCookieName || name == nextAuthSessionCookieFallbackName {
			exact = value
			continue
		}
		if index, ok := sessionCookieChunkIndex(name); ok {
			chunks[index] = value
		}
	}
	if exact != "" {
		return exact
	}
	if len(chunks) == 0 {
		return ""
	}
	indexes := make([]int, 0, len(chunks))
	for index := range chunks {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	var b strings.Builder
	for _, index := range indexes {
		b.WriteString(chunks[index])
	}
	return b.String()
}

func chatGPTSessionCookieHeader(sessionToken string) string {
	token := extractSessionToken(sessionToken)
	if token == "" {
		token = strings.TrimSpace(sessionToken)
	}
	if token == "" {
		return ""
	}
	if strings.Contains(token, "=") {
		parts := make([]string, 0, 2)
		for _, part := range strings.Split(token, ";") {
			name, value, ok := parseSessionCookiePart(part)
			if ok {
				parts = append(parts, name+"="+value)
			}
		}
		if len(parts) > 0 {
			sort.SliceStable(parts, func(i, j int) bool {
				return sessionCookieSortKey(parts[i]) < sessionCookieSortKey(parts[j])
			})
			return strings.Join(parts, "; ")
		}
	}
	if len(token) <= nextAuthSessionCookieChunkSize {
		return nextAuthSessionCookieName + "=" + token
	}
	parts := make([]string, 0, (len(token)+nextAuthSessionCookieChunkSize-1)/nextAuthSessionCookieChunkSize)
	for index, offset := 0, 0; offset < len(token); index, offset = index+1, offset+nextAuthSessionCookieChunkSize {
		end := offset + nextAuthSessionCookieChunkSize
		if end > len(token) {
			end = len(token)
		}
		parts = append(parts, fmt.Sprintf("%s.%d=%s", nextAuthSessionCookieName, index, token[offset:end]))
	}
	return strings.Join(parts, "; ")
}

func parseSessionCookiePart(raw string) (string, string, bool) {
	part := strings.Trim(raw, " \t\r\n'\"\\")
	for _, base := range []string{nextAuthSessionCookieName, nextAuthSessionCookieFallbackName} {
		if idx := strings.Index(part, base); idx >= 0 {
			part = part[idx:]
			break
		}
	}
	if !strings.Contains(part, "=") {
		return "", "", false
	}
	name, value, _ := strings.Cut(part, "=")
	name = strings.TrimSpace(name)
	value = strings.Trim(value, " \t\r\n'\"\\")
	for i, r := range value {
		if r == '\'' || r == '"' || r == '\\' || r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			value = value[:i]
			break
		}
	}
	if !isSessionCookieName(name) || value == "" {
		return "", "", false
	}
	return name, value, true
}

func isSessionCookieName(name string) bool {
	if name == nextAuthSessionCookieName || name == nextAuthSessionCookieFallbackName {
		return true
	}
	_, ok := sessionCookieChunkIndex(name)
	return ok
}

func sessionCookieChunkIndex(name string) (int, bool) {
	for _, base := range []string{nextAuthSessionCookieName, nextAuthSessionCookieFallbackName} {
		prefix := base + "."
		if strings.HasPrefix(name, prefix) {
			index, err := strconv.Atoi(strings.TrimPrefix(name, prefix))
			return index, err == nil
		}
	}
	return 0, false
}

func sessionCookieSortKey(part string) int {
	name, _, _ := strings.Cut(part, "=")
	if index, ok := sessionCookieChunkIndex(name); ok {
		return index
	}
	return -1
}

func (s *server) handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	resp, err := s.jobClient.ListJobs(r.Context(), &pb.ListJobsRequest{
		Limit:     int32(queryInt(r, "limit", 100)),
		Status:    strings.TrimSpace(r.URL.Query().Get("status")),
		Action:    strings.TrimSpace(r.URL.Query().Get("action")),
		AccountId: strings.TrimSpace(r.URL.Query().Get("account_id")),
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if resp.GetErrorMessage() != "" {
		writeError(w, http.StatusBadGateway, errors.New(resp.GetErrorMessage()))
		return
	}
	writeJSON(w, http.StatusOK, resp.GetSnapshots())
}

func (s *server) handleJob(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/jobs/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		writeError(w, http.StatusBadRequest, errors.New("job_id is required"))
		return
	}
	jobID := strings.TrimSpace(parts[0])

	if len(parts) > 1 {
		switch parts[1] {
		case "otp":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.submitJobOTP(w, r, jobID)
			return
		case "add-balance":
			if len(parts) != 3 {
				writeError(w, http.StatusNotFound, fmt.Errorf("unsupported job add-balance action: %s", strings.Join(parts[1:], "/")))
				return
			}
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			switch parts[2] {
			case "confirm":
				s.confirmManualAddBalance(w, r, jobID)
			case "select":
				s.selectGoPayAddBalance(w, r, jobID)
			default:
				writeError(w, http.StatusNotFound, fmt.Errorf("unsupported job add-balance action: %s", strings.Join(parts[1:], "/")))
			}
			return
		default:
			writeError(w, http.StatusNotFound, fmt.Errorf("unsupported job action: %s", parts[1]))
			return
		}
	}

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.jobClient.GetJob(r.Context(), &pb.GetJobRequest{JobId: jobID})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if resp.GetErrorMessage() != "" {
		writeError(w, http.StatusBadGateway, errors.New(resp.GetErrorMessage()))
		return
	}
	writeJSON(w, http.StatusOK, resp.GetSnapshot())
}

func (s *server) streamJobsEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming is not supported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	stream, err := s.jobClient.WatchJobs(r.Context(), &pb.WatchJobsRequest{
		JobIds:       requestJobIDs(r),
		AfterEventId: requestLastEventID(r),
	})
	if err != nil {
		_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", sseJSON(map[string]string{"error": err.Error()}))
		flusher.Flush()
		return
	}

	for {
		event, err := stream.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) && status.Code(err) != codes.Canceled {
				_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", sseJSON(map[string]string{"error": err.Error()}))
				flusher.Flush()
			}
			return
		}
		if event.GetErrorMessage() != "" {
			_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", sseJSON(map[string]string{"error": event.GetErrorMessage()}))
			flusher.Flush()
			return
		}
		jobEvent := event.GetEvent()
		if jobEvent == nil {
			continue
		}
		_, _ = fmt.Fprintf(w, "id: %d\nevent: job\ndata: %s\n\n", jobEvent.GetEventId(), sseJSON(jobEvent))
		flusher.Flush()
	}
}

func sseJSON(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		b, _ = json.Marshal(map[string]string{"error": err.Error()})
	}
	return string(b)
}

func requestJobIDs(r *http.Request) []string {
	query := r.URL.Query()
	values := append([]string{}, query["job_id"]...)
	values = append(values, query["job_ids"]...)
	out := []string{}
	seen := map[string]struct{}{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if _, ok := seen[part]; ok {
				continue
			}
			seen[part] = struct{}{}
			out = append(out, part)
		}
	}
	return out
}

func requestLastEventID(r *http.Request) int64 {
	value := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if value == "" {
		value = strings.TrimSpace(r.URL.Query().Get("after_event_id"))
	}
	if value == "" {
		return 0
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func (s *server) submitJobOTP(w http.ResponseWriter, r *http.Request, jobID string) {
	var req submitJobOTPRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	resp, err := s.otpClient.SubmitOTP(r.Context(), &pb.SubmitOTPRequest{
		JobId: jobID,
		Otp:   req.OTP,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if resp.GetErrorMessage() != "" {
		writeError(w, http.StatusBadRequest, errors.New(resp.GetErrorMessage()))
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) confirmManualAddBalance(w http.ResponseWriter, r *http.Request, jobID string) {
	resp, err := s.gopayAppClient.ConfirmManualAddBalance(r.Context(), &pb.ConfirmManualAddBalanceRequest{
		JobId: jobID,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if resp.GetErrorMessage() != "" {
		writeError(w, http.StatusBadRequest, errors.New(resp.GetErrorMessage()))
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) selectGoPayAddBalance(w http.ResponseWriter, r *http.Request, jobID string) {
	var req pb.ConfirmManualAddBalanceRequest
	if err := readProtoJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.JobId = jobID
	resp, err := s.gopayAppClient.ConfirmManualAddBalance(r.Context(), &req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if resp.GetErrorMessage() != "" {
		writeError(w, http.StatusBadRequest, errors.New(resp.GetErrorMessage()))
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req pb.RegisterAccountRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.accountWorkflowClient.RegisterAccount(r.Context(), &req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req pb.ActivateAccountRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.paymentWorkflowClient.ActivateAccount(r.Context(), &req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleAutopay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req pb.ActivateAccountRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.paymentWorkflowClient.AutopayAccount(r.Context(), &req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req pb.LoginAccountRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.accountWorkflowClient.LoginAccount(r.Context(), &req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	statusCode := http.StatusAccepted
	if !resp.GetStarted() || resp.GetErrorMessage() != "" {
		statusCode = http.StatusBadGateway
	}
	writeJSON(w, statusCode, resp)
}

func (s *server) handleProbeAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req pb.ProbeAccountRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.paymentWorkflowClient.ProbeAccount(r.Context(), &req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	statusCode := http.StatusAccepted
	if !resp.GetStarted() || resp.GetErrorMessage() != "" {
		statusCode = http.StatusBadGateway
	}
	writeJSON(w, statusCode, resp)
}

func (s *server) handleGoPayApp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req pb.GoPayAppRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.gopayAppClient.RunGoPayApp(r.Context(), &req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	statusCode := http.StatusAccepted
	if !resp.GetStarted() || resp.GetErrorMessage() != "" {
		statusCode = http.StatusBadGateway
	}
	writeJSON(w, statusCode, resp)
}

func (s *server) handleGoPayState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	if userID == "" {
		userID = "local"
	}
	resp, err := s.gopayAppClient.GoPayUserStatus(r.Context(), &pb.GoPayUserStatusRequest{UserId: userID})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	waPhone, err := s.gopayAppClient.GoPayUserGetWAPhone(r.Context(), &pb.GoPayUserGetWAPhoneRequest{UserId: userID})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":                resp.GetSuccess(),
		"error_message":          resp.GetErrorMessage(),
		"user_id":                userID,
		"wa_phone":               waPhone.GetWaPhone(),
		"wa_phone_error_message": waPhone.GetErrorMessage(),
		"status":                 resp.GetStatus(),
	})
}

func (s *server) handleGoPayPayment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req pb.GoPayPaymentRequest
	if err := readProtoJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.gopayAppClient.RunGoPayPayment(r.Context(), &req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	statusCode := http.StatusAccepted
	if !resp.GetStarted() || resp.GetErrorMessage() != "" {
		statusCode = http.StatusBadGateway
	}
	writeJSON(w, statusCode, resp)
}

func (s *server) handleGoPayPaymentRebind(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req pb.GoPayPaymentRebindRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.gopayAppClient.RetryGoPayPaymentRebind(r.Context(), &req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	statusCode := http.StatusAccepted
	if !resp.GetStarted() || resp.GetErrorMessage() != "" {
		statusCode = http.StatusBadGateway
	}
	writeJSON(w, statusCode, resp)
}

func (s *server) handleRegisterAndActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req pb.RegisterAndActivateAccountRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.accountWorkflowClient.RegisterAndActivateAccount(r.Context(), &req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(s.staticDir, filepath.Clean(r.URL.Path))
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		http.ServeFile(w, r, path)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.staticDir, "index.html"))
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,PUT,DELETE,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func readJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(dst)
}

func readProtoJSON(r *http.Request, dst protojsonUnmarshaler) error {
	defer r.Body.Close()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		raw = []byte("{}")
	}
	return protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(raw, dst)
}

type protojsonUnmarshaler interface {
	ProtoReflect() protoreflect.Message
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func queryInt(r *http.Request, key string, fallback int) int {
	value := strings.TrimSpace(r.URL.Query().Get(key))
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func newGRPCClient(addr string) (*grpc.ClientConn, error) {
	return grpc.NewClient(grpcDialTarget(addr), grpc.WithTransportCredentials(insecure.NewCredentials()))
}

func grpcDialTarget(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" || strings.Contains(addr, "://") || strings.HasPrefix(addr, "passthrough:") {
		return addr
	}
	// Let Docker DNS resolve the service name on each TCP reconnect instead of
	// caching a container IP inside gRPC's DNS resolver.
	return "passthrough:///" + addr
}

func envDefault(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func gptAllocationStatusFromMailboxInput(statusValue string, authStatusValue string, refreshToken string) string {
	statusValue = strings.TrimSpace(statusValue)
	switch statusValue {
	case "ASSIGNED", "REGISTERED", "USER_ALREADY_EXISTS", "REGISTRATION_FAILED", "BLOCKED":
		return statusValue
	}
	authStatusValue = strings.TrimSpace(authStatusValue)
	switch authStatusValue {
	case "AUTH_FAILED", "NEEDS_MANUAL_VERIFICATION":
		return authStatusValue
	case "AUTHORIZED":
		return "AVAILABLE"
	}
	if strings.TrimSpace(refreshToken) != "" {
		return "AVAILABLE"
	}
	return "OAUTH_PENDING"
}

func tailString(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
