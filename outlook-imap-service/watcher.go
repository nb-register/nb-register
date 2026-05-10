package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"outlookimapservice/pb"
)

type cachedOTP struct {
	OTP         string
	Subject     string
	SourceEmail string
	ReceivedAt  float64
}

type oauthEntry struct {
	refreshToken string
	manager      *OAuthManager
}

type MailWatcher struct {
	store        *MailboxStore
	graphURL     string
	messageLimit int
	pollInterval int
	httpClient   *http.Client

	mu            sync.Mutex
	cachedOTPs    map[string]cachedOTP
	seenMessages  map[string]float64
	oauthManagers map[string]oauthEntry
}

type GraphFetchError struct {
	StatusCode int
	Body       string
	RetryAfter time.Duration
}

func (e *GraphFetchError) Error() string {
	body := e.Body
	if len(body) > 500 {
		body = body[:500]
	}
	return fmt.Sprintf("status=%d body=%s", e.StatusCode, body)
}

func (e *GraphFetchError) IsAuth() bool {
	return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
}

func (e *GraphFetchError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= http.StatusInternalServerError
}

func NewMailWatcher(store *MailboxStore) *MailWatcher {
	messageLimit := envInt("OUTLOOK_MESSAGE_LIMIT", defaultMessageLimit)
	if messageLimit < 1 {
		messageLimit = 1
	}
	if messageLimit > 100 {
		messageLimit = 100
	}
	pollInterval := envInt("OUTLOOK_POLL_INTERVAL_SECONDS", defaultPollIntervalSeconds)
	if pollInterval < 1 {
		pollInterval = 1
	}
	timeout := envInt("OUTLOOK_HTTP_TIMEOUT_SECONDS", defaultHTTPTimeoutSeconds)
	if timeout <= 0 {
		timeout = defaultHTTPTimeoutSeconds
	}
	return &MailWatcher{
		store:         store,
		graphURL:      envStr("OUTLOOK_GRAPH_MESSAGES_URL", defaultGraphMessagesURL),
		messageLimit:  messageLimit,
		pollInterval:  pollInterval,
		httpClient:    &http.Client{Timeout: time.Duration(timeout) * time.Second},
		cachedOTPs:    map[string]cachedOTP{},
		seenMessages:  map[string]float64{},
		oauthManagers: map[string]oauthEntry{},
	}
}

func (w *MailWatcher) ConsumeCachedOTP(email string, subjectKeyword string, issuedAfter float64) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cleanupLocked()

	key := normalizeEmail(email)
	cached, ok := w.cachedOTPs[key]
	if !ok {
		key = canonicalEmail(email)
		cached, ok = w.cachedOTPs[key]
	}
	if !ok {
		return "", false
	}
	if !containsFold(cached.Subject, subjectKeyword) {
		return "", false
	}
	if issuedAfter > 0 && cached.ReceivedAt < issuedAfter {
		return "", false
	}
	delete(w.cachedOTPs, key)
	logInfo("served cached OTP for %s", redactEmail(email))
	return cached.OTP, true
}

func (w *MailWatcher) PollForEmail(ctx context.Context, email string) error {
	mailbox, err := w.store.PollMailboxForEmail(ctx, email)
	if err != nil {
		return err
	}
	return w.pollMailbox(ctx, mailbox)
}

func (w *MailWatcher) pollMailbox(ctx context.Context, mailbox *pb.EmailMailbox) error {
	manager := w.oauthManagerForMailbox(mailbox)
	accessToken, err := manager.GetAccessToken(ctx)
	if err != nil {
		w.store.MarkAuthFailed(ctx, mailbox.GetEmailAddress(), err)
		return err
	}
	if err := w.persistTokens(ctx, mailbox, manager); err != nil {
		w.store.MarkAuthFailed(ctx, mailbox.GetEmailAddress(), err)
		return err
	}
	messages, err := w.fetchRecentMessages(ctx, accessToken)
	if err != nil {
		var graphErr *GraphFetchError
		if !errors.As(err, &graphErr) {
			w.store.MarkAuthFailed(ctx, mailbox.GetEmailAddress(), err)
			return err
		}
		if !graphErr.IsAuth() {
			return err
		}
		logInfo("Graph auth error for %s; refreshing token and retrying", redactEmail(mailbox.GetEmailAddress()))
		accessToken, err = manager.RefreshAccessToken(ctx)
		if err == nil {
			err = w.persistTokens(ctx, mailbox, manager)
		}
		if err == nil {
			messages, err = w.fetchRecentMessages(ctx, accessToken)
		}
		if err != nil {
			w.store.MarkAuthFailed(ctx, mailbox.GetEmailAddress(), err)
			return err
		}
	}
	w.processMessages(mailbox.GetEmailAddress(), messages)
	return nil
}

func (w *MailWatcher) oauthManagerForMailbox(mailbox *pb.EmailMailbox) *OAuthManager {
	key := normalizeEmail(mailbox.GetEmailAddress())
	refreshToken := strings.TrimSpace(mailbox.GetRefreshToken())
	w.mu.Lock()
	defer w.mu.Unlock()
	entry, ok := w.oauthManagers[key]
	if !ok || entry.refreshToken != refreshToken {
		entry = oauthEntry{refreshToken: refreshToken, manager: NewOAuthManager(refreshToken)}
		w.oauthManagers[key] = entry
	}
	return entry.manager
}

func (w *MailWatcher) persistTokens(ctx context.Context, mailbox *pb.EmailMailbox, manager *OAuthManager) error {
	refreshToken, accessToken := manager.CurrentTokens()
	if refreshToken != mailbox.GetRefreshToken() || accessToken != mailbox.GetAccessToken() {
		return w.store.UpdateMailboxTokens(ctx, mailbox.GetEmailAddress(), refreshToken, accessToken)
	}
	return nil
}

func (w *MailWatcher) fetchRecentMessages(ctx context.Context, accessToken string) ([]graphMessage, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		messages, err := w.fetchOnce(ctx, accessToken)
		if err == nil {
			return messages, nil
		}
		lastErr = err
		var graphErr *GraphFetchError
		if !errors.As(err, &graphErr) || attempt == 2 || !graphErr.Retryable() {
			break
		}
		delay := graphErr.RetryAfter
		if delay <= 0 {
			delay = time.Duration(attempt+1) * 500 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func (w *MailWatcher) fetchOnce(ctx context.Context, accessToken string) ([]graphMessage, error) {
	u, err := url.Parse(w.graphURL)
	if err != nil {
		return nil, err
	}
	query := u.Query()
	query.Set("$top", strconv.Itoa(w.messageLimit))
	query.Set("$orderby", "receivedDateTime desc")
	query.Set("$select", "id,subject,bodyPreview,body,toRecipients,ccRecipients,bccRecipients,internetMessageHeaders,receivedDateTime")
	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Prefer", `outlook.body-content-type="text"`)

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &GraphFetchError{
			StatusCode: resp.StatusCode,
			Body:       string(raw),
			RetryAfter: retryAfter(resp.Header.Get("Retry-After")),
		}
	}
	var decoded graphMessagesResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return decoded.Value, nil
}

func (w *MailWatcher) processMessages(sourceEmail string, messages []graphMessage) {
	for _, msg := range messages {
		msgKey := sourceEmail + ":" + messageKey(msg)
		w.mu.Lock()
		if _, ok := w.seenMessages[msgKey]; ok {
			w.mu.Unlock()
			continue
		}
		w.seenMessages[msgKey] = float64(time.Now().Unix())
		w.mu.Unlock()

		receivedAt := parseGraphTime(msg.ReceivedDateTime)
		recipients := messageAddresses(msg)
		if len(recipients) == 0 {
			continue
		}
		body := msg.BodyPreview + "\n" + msg.Body.Content
		otp := extractOTP(body)
		if otp == "" {
			continue
		}
		if receivedAt == 0 {
			receivedAt = float64(time.Now().Unix())
		}
		w.cacheOTP(msg.Subject, otp, recipients, receivedAt)
	}
}

func (w *MailWatcher) cacheOTP(subject string, otp string, recipients []string, receivedAt float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, recipient := range recipients {
		key := normalizeEmail(recipient)
		if key != "" {
			w.cachedOTPs[key] = cachedOTP{
				OTP:         otp,
				Subject:     subject,
				SourceEmail: recipient,
				ReceivedAt:  receivedAt,
			}
		}
	}
	logInfo("cached OTP for %d recipient(s)", len(recipients))
}

func (w *MailWatcher) cleanupLocked() {
	now := float64(time.Now().Unix())
	for key, cached := range w.cachedOTPs {
		if now-cached.ReceivedAt > 600 {
			delete(w.cachedOTPs, key)
		}
	}
	for key, seenAt := range w.seenMessages {
		if now-seenAt > 3600 {
			delete(w.seenMessages, key)
		}
	}
}

func retryAfter(value string) time.Duration {
	if strings.TrimSpace(value) == "" {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n <= 0 {
		return 0
	}
	if n > 10 {
		n = 10
	}
	return time.Duration(n) * time.Second
}

func messageKey(msg graphMessage) string {
	if msg.ID != "" {
		return msg.ID
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func messageAddresses(msg graphMessage) []string {
	out := []string{}
	for _, list := range [][]graphRecipient{msg.ToRecipients, msg.CcRecipients, msg.BccRecipients} {
		for _, recipient := range list {
			if address := strings.TrimSpace(recipient.EmailAddress.Address); address != "" {
				out = append(out, address)
			}
		}
	}
	for _, header := range msg.InternetMessageHeaders {
		name := strings.ToLower(strings.TrimSpace(header.Name))
		value := header.Value
		if recipientHeaders[name] {
			out = append(out, emailPattern.FindAllString(value, -1)...)
			continue
		}
		if name == "received" {
			idx := strings.LastIndex(strings.ToLower(value), " for ")
			if idx >= 0 {
				out = append(out, emailPattern.FindAllString(value[idx+5:], -1)...)
			}
		}
	}
	return out
}

var recipientHeaders = map[string]bool{
	"to":                   true,
	"cc":                   true,
	"bcc":                  true,
	"delivered-to":         true,
	"envelope-to":          true,
	"x-envelope-to":        true,
	"x-original-to":        true,
	"x-original-recipient": true,
	"resent-to":            true,
	"apparently-to":        true,
	"x-forwarded-to":       true,
	"x-ms-exchange-organization-originalrecipient":          true,
	"x-ms-exchange-organization-originalenveloperecipients": true,
}

type graphMessagesResponse struct {
	Value []graphMessage `json:"value"`
}

type graphMessage struct {
	ID                     string           `json:"id"`
	Subject                string           `json:"subject"`
	BodyPreview            string           `json:"bodyPreview"`
	Body                   graphBody        `json:"body"`
	ToRecipients           []graphRecipient `json:"toRecipients"`
	CcRecipients           []graphRecipient `json:"ccRecipients"`
	BccRecipients          []graphRecipient `json:"bccRecipients"`
	InternetMessageHeaders []graphHeader    `json:"internetMessageHeaders"`
	ReceivedDateTime       string           `json:"receivedDateTime"`
}

type graphBody struct {
	Content string `json:"content"`
}

type graphRecipient struct {
	EmailAddress graphEmailAddress `json:"emailAddress"`
}

type graphEmailAddress struct {
	Address string `json:"address"`
}

type graphHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
