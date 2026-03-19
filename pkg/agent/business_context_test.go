package agent

import (
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestApplyResponseSchemaToMessages(t *testing.T) {
	messages := []providers.Message{
		{Role: "system", Content: "base system"},
		{Role: "user", Content: "hello"},
	}

	applyResponseSchemaToMessages(messages, "{\"type\":\"object\",\"properties\":{\"reply\":{\"type\":\"string\"}}}")

	if !strings.Contains(messages[0].Content, "RESPONSE_SCHEMA") {
		t.Fatal("expected system message to contain RESPONSE_SCHEMA block")
	}
	if strings.Contains(messages[1].Content, "RESPONSE_SCHEMA") {
		t.Fatal("expected user message to remain untouched")
	}
}

func TestApplyResponseSchemaToMessagesSkipsBlank(t *testing.T) {
	messages := []providers.Message{{Role: "system", Content: "base system"}}
	applyResponseSchemaToMessages(messages, "   ")
	if messages[0].Content != "base system" {
		t.Fatal("expected blank schema to be ignored")
	}
}
