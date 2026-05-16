package runtime

import "testing"

func TestNormalizeGoPayUserStateKeyUsesSingleSource(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty local", input: "", want: "local"},
		{name: "local", input: "local", want: "local"},
		{name: "telegram user", input: "tg:200", want: "tg:200"},
		{name: "telegram negative id rejected", input: "tg:-1001234567890", wantErr: true},
		{name: "old user key rejected", input: "user:tg:100", wantErr: true},
		{name: "arbitrary key rejected", input: "seed", wantErr: true},
		{name: "telegram user id text rejected", input: "tg:abc", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeGoPayUserStateKey(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("normalizeGoPayUserStateKey(%q) error = nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeGoPayUserStateKey(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("normalizeGoPayUserStateKey(%q) = %q; want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGoPayOTPQueueKeyUsesSource(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		purpose string
		want    string
		wantErr bool
	}{
		{name: "local", source: "local", purpose: "gopay", want: "local/gopay"},
		{name: "empty source defaults local", source: "", purpose: "gopay", want: "local/gopay"},
		{name: "telegram source", source: "tg:200", purpose: "gopay", want: "tg:200/gopay"},
		{name: "invalid source", source: "seed", purpose: "gopay", wantErr: true},
		{name: "invalid purpose", source: "local", purpose: "gopay/payment", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := goPayOTPQueueKey(tt.source, tt.purpose)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("goPayOTPQueueKey(%q, %q) error = nil", tt.source, tt.purpose)
				}
				return
			}
			if err != nil {
				t.Fatalf("goPayOTPQueueKey(%q, %q) error = %v", tt.source, tt.purpose, err)
			}
			if got != tt.want {
				t.Fatalf("goPayOTPQueueKey(%q, %q) = %q; want %q", tt.source, tt.purpose, got, tt.want)
			}
		})
	}
}
