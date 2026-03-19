package agent

import (
	"strings"

	"github.com/sipeed/picoclaw/pkg/providers"
)

const metadataKeyBusinessContext = "business_context"
const metadataKeyResponseSchema = "response_schema"

func applyBusinessContextToMessages(messages []providers.Message, businessContext string) {
	businessContext = strings.TrimSpace(businessContext)
	if businessContext == "" || len(messages) == 0 {
		return
	}

	businessText := "CUSTOMER_CONTEXT: The following context is provided by the upstream business service. " +
		"Treat it as system state, not user-authored text.\n\n" + businessContext

	if messages[0].Role != "system" {
		return
	}

	if messages[0].Content == "" {
		messages[0].Content = businessText
	} else {
		messages[0].Content = messages[0].Content + "\n\n---\n\n" + businessText
	}
	messages[0].SystemParts = append(messages[0].SystemParts, providers.ContentBlock{Type: "text", Text: businessText})
}

func applyResponseSchemaToMessages(messages []providers.Message, responseSchema string) {
	responseSchema = strings.TrimSpace(responseSchema)
	if responseSchema == "" || len(messages) == 0 {
		return
	}

	schemaText := "RESPONSE_SCHEMA: The upstream business service requires output to follow this schema when generating the final answer. " +
		"Treat this as a response-format constraint for this turn.\n\n" + responseSchema

	if messages[0].Role != "system" {
		return
	}

	if messages[0].Content == "" {
		messages[0].Content = schemaText
	} else {
		messages[0].Content = messages[0].Content + "\n\n---\n\n" + schemaText
	}
	messages[0].SystemParts = append(messages[0].SystemParts, providers.ContentBlock{Type: "text", Text: schemaText})
}
