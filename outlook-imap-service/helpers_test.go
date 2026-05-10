package main

import "testing"

func TestCanonicalEmail(t *testing.T) {
	got := canonicalEmail(" User.Name+abc@Example.com ")
	if got != "user.name@example.com" {
		t.Fatalf("canonicalEmail mismatch: %s", got)
	}
}

func TestExtractOTP(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{`<html>Your code&nbsp;is <b>123456</b>.</html>`, "123456"},
		{`abc 012345 def`, "012345"},
		{`no otp 12345`, ""},
		{`token 1234567`, ""},
	}
	for _, tc := range cases {
		if got := extractOTP(tc.body); got != tc.want {
			t.Fatalf("extractOTP(%q)=%q want %q", tc.body, got, tc.want)
		}
	}
}

func TestParseGraphTime(t *testing.T) {
	got := parseGraphTime("2026-05-10T08:37:39Z")
	if got <= 0 {
		t.Fatalf("parseGraphTime returned %v", got)
	}
	if parseGraphTime("bad") != 0 {
		t.Fatalf("bad graph time should return 0")
	}
}
