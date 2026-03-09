package externalservice

import "time"

const (
	TypeMessageSend   = "message.send"
	TypePing          = "ping"
	TypeMessageCreate = "message.create"
	TypeMessageUpdate = "message.update"
	TypeTypingStart   = "typing.start"
	TypeTypingStop    = "typing.stop"
	TypeError         = "error"
	TypePong          = "pong"
)

type ExternalServiceMessage struct {
	Type      string         `json:"type"`
	ID        string         `json:"id,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	Timestamp int64          `json:"timestamp,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
}

func newMessage(msgType string, payload map[string]any) ExternalServiceMessage {
	return ExternalServiceMessage{
		Type:      msgType,
		Timestamp: time.Now().UnixMilli(),
		Payload:   payload,
	}
}

func newError(code, message string) ExternalServiceMessage {
	return newMessage(TypeError, map[string]any{
		"code":    code,
		"message": message,
	})
}
