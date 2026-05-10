package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMessageAddresses(t *testing.T) {
	msg := graphMessage{
		ToRecipients: []graphRecipient{{EmailAddress: graphEmailAddress{Address: "alias@example.com"}}},
		InternetMessageHeaders: []graphHeader{
			{Name: "Delivered-To", Value: "delivered@example.com"},
			{Name: "Received", Value: "from mx by host for received@example.com; Sun, 10 May 2026 08:37:39 +0000"},
		},
	}
	got := messageAddresses(msg)
	want := map[string]bool{
		"alias@example.com":     true,
		"delivered@example.com": true,
		"received@example.com":  true,
	}
	if len(got) != len(want) {
		t.Fatalf("addresses=%v", got)
	}
	for _, address := range got {
		if !want[address] {
			t.Fatalf("unexpected address %s in %v", address, got)
		}
	}
}

func TestProcessMessagesCachesOTP(t *testing.T) {
	watcher := &MailWatcher{
		cachedOTPs:   map[string]cachedOTP{},
		seenMessages: map[string]float64{},
	}
	watcher.processMessages("primary@example.com", []graphMessage{{
		ID:               "msg-1",
		Subject:          "OTP",
		BodyPreview:      "code 654321",
		ReceivedDateTime: time.Now().UTC().Format(time.RFC3339),
		ToRecipients:     []graphRecipient{{EmailAddress: graphEmailAddress{Address: "alias@example.com"}}},
	}})
	otp, ok := watcher.ConsumeCachedOTP("alias@example.com", "otp", 0)
	if !ok || otp != "654321" {
		t.Fatalf("cached otp=%q ok=%v", otp, ok)
	}
}

func TestOAuthRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("refresh_token") != "old-refresh" {
			t.Fatalf("refresh_token=%q", r.Form.Get("refresh_token"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-token",
			"refresh_token": "new-refresh",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	t.Setenv("OUTLOOK_OAUTH_TOKEN_URL", server.URL)
	manager := NewOAuthManager("old-refresh")
	token, err := manager.GetAccessToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token != "access-token" {
		t.Fatalf("token=%q", token)
	}
	refresh, access := manager.CurrentTokens()
	if refresh != "new-refresh" || access != "access-token" {
		t.Fatalf("refresh=%q access=%q", refresh, access)
	}
}

func TestFetchRecentMessages(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer access" {
			t.Fatalf("bad auth header: %s", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(graphMessagesResponse{Value: []graphMessage{{ID: "msg-1", Subject: "OTP"}}})
	}))
	defer server.Close()

	watcher := &MailWatcher{
		graphURL:     server.URL,
		messageLimit: 25,
		httpClient:   server.Client(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	messages, err := watcher.fetchRecentMessages(ctx, "access")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].ID != "msg-1" || calls != 1 {
		t.Fatalf("messages=%v calls=%d", messages, calls)
	}
}
