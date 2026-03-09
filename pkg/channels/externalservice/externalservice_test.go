package externalservice

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestSingleConnectionMultiSession(t *testing.T) {
	msgBus := bus.NewMessageBus()
	ch, err := NewExternalServiceChannel(config.ExternalServiceConfig{Token: "secret"}, msgBus)
	if err != nil {
		t.Fatalf("NewExternalServiceChannel() error = %v", err)
	}
	if err := ch.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	pc := &externalConn{id: "conn-1"}
	ch.handleMessageSend(
		pc,
		ExternalServiceMessage{Type: TypeMessageSend, SessionID: "s1", Payload: map[string]any{"content": "hello 1"}},
	)
	ch.handleMessageSend(
		pc,
		ExternalServiceMessage{Type: TypeMessageSend, SessionID: "s2", Payload: map[string]any{"content": "hello 2"}},
	)

	msg1, ok := msgBus.ConsumeInbound(context.Background())
	if !ok {
		t.Fatal("expected first inbound message")
	}
	msg2, ok := msgBus.ConsumeInbound(context.Background())
	if !ok {
		t.Fatal("expected second inbound message")
	}

	if msg1.ChatID != "external_service:s1" || msg2.ChatID != "external_service:s2" {
		t.Fatalf("unexpected chatIDs: %q, %q", msg1.ChatID, msg2.ChatID)
	}
	if msg1.Peer.ID == msg2.Peer.ID {
		t.Fatalf("expected different peer IDs for different sessions, got %q and %q", msg1.Peer.ID, msg2.Peer.ID)
	}
}

func TestRouteBySessionID(t *testing.T) {
	msgBus := bus.NewMessageBus()
	ch, err := NewExternalServiceChannel(config.ExternalServiceConfig{Token: "secret"}, msgBus)
	if err != nil {
		t.Fatalf("NewExternalServiceChannel() error = %v", err)
	}
	if err := ch.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	pc := &externalConn{id: "conn-1"}
	msg := ExternalServiceMessage{
		Type:      TypeMessageSend,
		SessionID: "stable",
		Payload: map[string]any{
			"content":   "hello",
			"chat_id":   "custom-chat",
			"peer_kind": "direct",
			"peer_id":   "peer-1",
		},
	}
	ch.handleMessageSend(pc, msg)

	inbound, ok := msgBus.ConsumeInbound(context.Background())
	if !ok {
		t.Fatal("expected inbound message")
	}
	if inbound.ChatID != "custom-chat" {
		t.Fatalf("ChatID = %q, want %q", inbound.ChatID, "custom-chat")
	}
	if inbound.Peer.Kind != "direct" || inbound.Peer.ID != "peer-1" {
		t.Fatalf("unexpected peer = %+v", inbound.Peer)
	}
	if inbound.Metadata["session_id"] != "stable" {
		t.Fatalf("session_id metadata = %q, want stable", inbound.Metadata["session_id"])
	}
}

func TestMissingSessionID(t *testing.T) {
	msgBus := bus.NewMessageBus()
	ch, err := NewExternalServiceChannel(config.ExternalServiceConfig{Token: "secret"}, msgBus)
	if err != nil {
		t.Fatalf("NewExternalServiceChannel() error = %v", err)
	}
	if err := ch.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	var seenErr ExternalServiceMessage
	pc := &externalConn{
		id: "conn-1",
		writeJSONFn: func(v any) error {
			msg, ok := v.(ExternalServiceMessage)
			if !ok {
				return errors.New("unexpected message type")
			}
			seenErr = msg
			return nil
		},
	}

	ch.handleMessageSend(pc, ExternalServiceMessage{Type: TypeMessageSend, Payload: map[string]any{"content": "hello"}})

	if seenErr.Type != TypeError {
		t.Fatalf("expected error response, got %+v", seenErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, ok := msgBus.ConsumeInbound(ctx); ok {
		t.Fatal("expected no inbound message for missing session_id")
	}
}

func TestReconnectDoesNotMergeSessions(t *testing.T) {
	msgBus := bus.NewMessageBus()
	ch, err := NewExternalServiceChannel(config.ExternalServiceConfig{Token: "secret"}, msgBus)
	if err != nil {
		t.Fatalf("NewExternalServiceChannel() error = %v", err)
	}
	if err := ch.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	ch.handleMessageSend(
		&externalConn{id: "conn-old"},
		ExternalServiceMessage{Type: TypeMessageSend, SessionID: "alpha", Payload: map[string]any{"content": "a"}},
	)
	ch.handleMessageSend(
		&externalConn{id: "conn-new"},
		ExternalServiceMessage{Type: TypeMessageSend, SessionID: "beta", Payload: map[string]any{"content": "b"}},
	)

	msg1, _ := msgBus.ConsumeInbound(context.Background())
	msg2, _ := msgBus.ConsumeInbound(context.Background())
	if msg1.ChatID == msg2.ChatID {
		t.Fatalf("expected reconnect sessions to remain distinct, got %q and %q", msg1.ChatID, msg2.ChatID)
	}
}

func TestStopClosesConnection(t *testing.T) {
	msgBus := bus.NewMessageBus()
	ch, err := NewExternalServiceChannel(config.ExternalServiceConfig{Token: "secret"}, msgBus)
	if err != nil {
		t.Fatalf("NewExternalServiceChannel() error = %v", err)
	}
	if err := ch.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	closed := 0
	pc := &externalConn{id: "conn-1", closeFn: func() error { closed++; return nil }}
	ch.connections.Store(pc.id, pc)
	ch.connCount.Store(1)

	if err := ch.Stop(t.Context()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if closed != 1 {
		t.Fatalf("expected one close, got %d", closed)
	}
}

func TestCustomChatIDPreservesSessionID(t *testing.T) {
	msgBus := bus.NewMessageBus()
	ch, err := NewExternalServiceChannel(config.ExternalServiceConfig{Token: "secret"}, msgBus)
	if err != nil {
		t.Fatalf("NewExternalServiceChannel() error = %v", err)
	}
	if err := ch.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	pc := &externalConn{id: "conn-1"}
	msg := ExternalServiceMessage{
		Type:      TypeMessageSend,
		SessionID: "original-session",
		Payload: map[string]any{
			"content": "hello",
			"chat_id": "custom-chat-id",
		},
	}
	ch.handleMessageSend(pc, msg)

	inbound, ok := msgBus.ConsumeInbound(context.Background())
	if !ok {
		t.Fatal("expected inbound message")
	}
	if inbound.Metadata["session_id"] != "original-session" {
		t.Fatalf("session_id metadata = %q, want %q", inbound.Metadata["session_id"], "original-session")
	}

	sessionID := ch.getSessionID("custom-chat-id")
	if sessionID != "original-session" {
		t.Fatalf("getSessionID for custom-chat-id = %q, want %q", sessionID, "original-session")
	}
}

func TestOutboundRoutesToMappedConnection(t *testing.T) {
	msgBus := bus.NewMessageBus()
	ch, err := NewExternalServiceChannel(config.ExternalServiceConfig{Token: "secret"}, msgBus)
	if err != nil {
		t.Fatalf("NewExternalServiceChannel() error = %v", err)
	}
	if err := ch.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	var conn1Messages []ExternalServiceMessage
	var conn2Messages []ExternalServiceMessage

	pc1 := &externalConn{
		id: "conn-1",
		writeJSONFn: func(v any) error {
			if msg, ok := v.(ExternalServiceMessage); ok {
				conn1Messages = append(conn1Messages, msg)
			}
			return nil
		},
	}
	pc2 := &externalConn{
		id: "conn-2",
		writeJSONFn: func(v any) error {
			if msg, ok := v.(ExternalServiceMessage); ok {
				conn2Messages = append(conn2Messages, msg)
			}
			return nil
		},
	}

	ch.connections.Store("conn-1", pc1)
	ch.connections.Store("conn-2", pc2)

	msg1 := ExternalServiceMessage{
		Type:      TypeMessageSend,
		SessionID: "sess-1",
		Payload: map[string]any{
			"content": "from session 1",
			"chat_id": "custom-chat-1",
		},
	}
	ch.handleMessageSend(pc1, msg1)

	msg2 := ExternalServiceMessage{
		Type:      TypeMessageSend,
		SessionID: "sess-2",
		Payload: map[string]any{
			"content": "from session 2",
			"chat_id": "custom-chat-2",
		},
	}
	ch.handleMessageSend(pc2, msg2)

	msgBus.ConsumeInbound(context.Background())
	msgBus.ConsumeInbound(context.Background())

	outMsg1 := ExternalServiceMessage{
		Type: TypeMessageCreate,
		Payload: map[string]any{
			"content": "reply to session 1",
			"chat_id": "custom-chat-1",
		},
	}
	_ = ch.sendToConnectionForChatID("custom-chat-1", outMsg1)

	outMsg2 := ExternalServiceMessage{
		Type: TypeMessageCreate,
		Payload: map[string]any{
			"content": "reply to session 2",
			"chat_id": "custom-chat-2",
		},
	}
	_ = ch.sendToConnectionForChatID("custom-chat-2", outMsg2)

	if len(conn1Messages) != 1 {
		t.Fatalf("expected 1 message to conn1, got %d", len(conn1Messages))
	}
	if len(conn2Messages) != 1 {
		t.Fatalf("expected 1 message to conn2, got %d", len(conn2Messages))
	}
}
