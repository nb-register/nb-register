package runtime

import (
	"reflect"
	"testing"

	"orchestrator/pb"
)

func TestIsOpenAIDeactivationNotice(t *testing.T) {
	msg := &pb.EmailInboxMessage{
		FromAddress: "Trust and Safety <trustandsafety@tm.openai.com>",
		Subject:     "OpenAI - Access Deactivated  [C-CmgSPYQ3gSfU]",
	}
	if !isOpenAIDeactivationNotice(msg) {
		t.Fatal("expected deactivation notice")
	}
	msg.Subject = "Your code"
	if isOpenAIDeactivationNotice(msg) {
		t.Fatal("unexpected deactivation notice")
	}
}

func TestDeactivationRecipientsExtractsFullMailbox(t *testing.T) {
	msg := &pb.EmailInboxMessage{Recipients: []string{
		"OpenAI User <User+split-token@outlook.com>",
		"USER+split-token@outlook.com",
		"x-original-to: another.alias+full@hotmail.com",
	}}
	got := deactivationRecipients(msg)
	want := []string{"user+split-token@outlook.com", "another.alias+full@hotmail.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recipients=%v want %v", got, want)
	}
}
