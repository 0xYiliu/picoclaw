package externalservice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/identity"
	"github.com/sipeed/picoclaw/pkg/logger"
)

type externalConn struct {
	id          string
	conn        *websocket.Conn
	writeMu     sync.Mutex
	closed      atomic.Bool
	writeJSONFn func(v any) error
	closeFn     func() error
}

func (pc *externalConn) writeJSON(v any) error {
	if pc.closed.Load() {
		return fmt.Errorf("connection closed")
	}
	pc.writeMu.Lock()
	defer pc.writeMu.Unlock()
	if pc.writeJSONFn != nil {
		return pc.writeJSONFn(v)
	}
	if pc.conn == nil {
		return fmt.Errorf("no websocket connection")
	}
	return pc.conn.WriteJSON(v)
}

func (pc *externalConn) close() {
	if !pc.closed.CompareAndSwap(false, true) {
		return
	}
	if pc.closeFn != nil {
		_ = pc.closeFn()
		return
	}
	if pc.conn != nil {
		_ = pc.conn.Close()
	}
}

type ExternalServiceChannel struct {
	*channels.BaseChannel
	config        config.ExternalServiceConfig
	upgrader      websocket.Upgrader
	connections   sync.Map
	connCount     atomic.Int32
	ctx           context.Context
	cancel        context.CancelFunc
	chatIDMapping sync.Map // maps custom chatID -> {sessionID, connID}
}

func NewExternalServiceChannel(
	cfg config.ExternalServiceConfig,
	messageBus *bus.MessageBus,
) (*ExternalServiceChannel, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("external_service token is required")
	}

	base := channels.NewBaseChannel(
		"external_service",
		cfg,
		messageBus,
		cfg.AllowFrom,
		channels.WithReasoningChannelID(cfg.ReasoningChannelID),
	)

	allowOrigins := cfg.AllowOrigins
	checkOrigin := func(r *http.Request) bool {
		if len(allowOrigins) == 0 {
			return true
		}
		origin := r.Header.Get("Origin")
		for _, allowed := range allowOrigins {
			if allowed == "*" || allowed == origin {
				return true
			}
		}
		return false
	}

	return &ExternalServiceChannel{
		BaseChannel: base,
		config:      cfg,
		upgrader: websocket.Upgrader{
			CheckOrigin:     checkOrigin,
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
	}, nil
}

func (c *ExternalServiceChannel) Start(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.SetRunning(true)
	return nil
}

func (c *ExternalServiceChannel) Stop(ctx context.Context) error {
	c.SetRunning(false)
	c.connections.Range(func(key, value any) bool {
		if conn, ok := value.(*externalConn); ok {
			conn.close()
		}
		c.connections.Delete(key)
		return true
	})
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}

func (c *ExternalServiceChannel) WebhookPath() string { return "/external-service/" }

func (c *ExternalServiceChannel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/external-service")
	switch {
	case path == "/ws" || path == "/ws/":
		c.handleWebSocket(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (c *ExternalServiceChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return channels.ErrNotRunning
	}
	outMsg := newMessage(TypeMessageCreate, map[string]any{
		"content": msg.Content,
		"chat_id": msg.ChatID,
	})
	outMsg.SessionID = c.getSessionID(msg.ChatID)
	return c.sendToConnectionForChatID(msg.ChatID, outMsg)
}

func (c *ExternalServiceChannel) EditMessage(
	ctx context.Context,
	chatID string,
	messageID string,
	content string,
) error {
	outMsg := newMessage(TypeMessageUpdate, map[string]any{
		"message_id": messageID,
		"content":    content,
		"chat_id":    chatID,
	})
	outMsg.SessionID = c.getSessionID(chatID)
	return c.sendToConnectionForChatID(chatID, outMsg)
}

func (c *ExternalServiceChannel) StartTyping(ctx context.Context, chatID string) (func(), error) {
	startMsg := newMessage(TypeTypingStart, map[string]any{"chat_id": chatID})
	startMsg.SessionID = c.getSessionID(chatID)
	if err := c.sendToConnectionForChatID(chatID, startMsg); err != nil {
		return func() {}, err
	}
	return func() {
		stopMsg := newMessage(TypeTypingStop, map[string]any{"chat_id": chatID})
		stopMsg.SessionID = c.getSessionID(chatID)
		_ = c.sendToConnectionForChatID(chatID, stopMsg)
	}, nil
}

func (c *ExternalServiceChannel) SendPlaceholder(ctx context.Context, chatID string) (string, error) {
	if !c.config.Placeholder.Enabled {
		return "", nil
	}
	text := c.config.Placeholder.Text
	if text == "" {
		text = "Thinking..."
	}
	msgID := uuid.New().String()
	outMsg := newMessage(TypeMessageCreate, map[string]any{
		"message_id": msgID,
		"content":    text,
		"chat_id":    chatID,
	})
	outMsg.SessionID = c.getSessionID(chatID)
	if err := c.sendToConnectionForChatID(chatID, outMsg); err != nil {
		return "", err
	}
	return msgID, nil
}

func (c *ExternalServiceChannel) sendToConnection(msg ExternalServiceMessage) error {
	var sendErr error
	var sent bool
	c.connections.Range(func(key, value any) bool {
		pc, ok := value.(*externalConn)
		if !ok {
			return true
		}
		if err := pc.writeJSON(msg); err != nil {
			sendErr = err
			return true
		}
		sent = true
		return false
	})
	if sent {
		return nil
	}
	if sendErr != nil {
		return fmt.Errorf("send external_service message: %w", channels.ErrTemporary)
	}
	return fmt.Errorf("no active external_service connection: %w", channels.ErrSendFailed)
}

func (c *ExternalServiceChannel) getSessionID(chatID string) string {
	if mapping, ok := c.chatIDMapping.Load(chatID); ok {
		if m, ok := mapping.(map[string]string); ok {
			return m["sessionID"]
		}
	}
	return sessionIDFromChatID(chatID)
}

func (c *ExternalServiceChannel) sendToConnectionForChatID(chatID string, msg ExternalServiceMessage) error {
	var targetConnID string
	if mapping, ok := c.chatIDMapping.Load(chatID); ok {
		if m, ok := mapping.(map[string]string); ok {
			targetConnID = m["connID"]
		}
	}

	var sendErr error
	var sent bool

	if targetConnID != "" {
		if connVal, ok := c.connections.Load(targetConnID); ok {
			if pc, ok := connVal.(*externalConn); ok {
				if err := pc.writeJSON(msg); err != nil {
					sendErr = err
				} else {
					sent = true
				}
			}
		}
	}

	if sent {
		return nil
	}

	if sendErr != nil {
		return fmt.Errorf("send external_service message: %w", channels.ErrTemporary)
	}
	return fmt.Errorf("no active external_service connection: %w", channels.ErrSendFailed)
}

func (c *ExternalServiceChannel) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !c.IsRunning() {
		http.Error(w, "channel not running", http.StatusServiceUnavailable)
		return
	}
	if !c.authenticate(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	maxConns := c.config.MaxConnections
	if maxConns <= 0 {
		maxConns = 1
	}
	if int(c.connCount.Load()) >= maxConns {
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	conn, err := c.upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.ErrorCF("external_service", "WebSocket upgrade failed", map[string]any{"error": err.Error()})
		return
	}
	pc := &externalConn{id: uuid.New().String(), conn: conn}
	c.connections.Store(pc.id, pc)
	c.connCount.Add(1)
	go c.readLoop(pc)
}

func (c *ExternalServiceChannel) authenticate(r *http.Request) bool {
	token := c.config.Token
	if token == "" {
		return false
	}
	auth := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(auth, "Bearer "); ok && after == token {
		return true
	}
	if c.config.AllowTokenQuery && r.URL.Query().Get("token") == token {
		return true
	}
	return false
}

func (c *ExternalServiceChannel) readLoop(pc *externalConn) {
	defer func() {
		pc.close()
		c.connections.Delete(pc.id)
		c.connCount.Add(-1)
	}()

	readTimeout := time.Duration(c.config.ReadTimeout) * time.Second
	if readTimeout <= 0 {
		readTimeout = 60 * time.Second
	}
	writeTimeout := time.Duration(c.config.WriteTimeout) * time.Second
	if writeTimeout <= 0 {
		writeTimeout = 10 * time.Second
	}
	pingInterval := time.Duration(c.config.PingInterval) * time.Second
	if pingInterval <= 0 {
		pingInterval = 30 * time.Second
	}

	if pc.conn != nil {
		_ = pc.conn.SetReadDeadline(time.Now().Add(readTimeout))
		pc.conn.SetPongHandler(func(appData string) error {
			_ = pc.conn.SetReadDeadline(time.Now().Add(readTimeout))
			return nil
		})
	}

	go c.pingLoop(pc, pingInterval, writeTimeout)

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		if pc.conn == nil {
			return
		}
		_, rawMsg, err := pc.conn.ReadMessage()
		if err != nil {
			return
		}
		_ = pc.conn.SetReadDeadline(time.Now().Add(readTimeout))

		var msg ExternalServiceMessage
		if err := json.Unmarshal(rawMsg, &msg); err != nil {
			_ = pc.writeJSON(newError("invalid_message", "failed to parse message"))
			continue
		}
		c.handleMessage(pc, msg)
	}
}

func (c *ExternalServiceChannel) pingLoop(pc *externalConn, interval, writeTimeout time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			if pc.closed.Load() || pc.conn == nil {
				return
			}
			pc.writeMu.Lock()
			_ = pc.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			err := pc.conn.WriteMessage(websocket.PingMessage, nil)
			pc.writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

func (c *ExternalServiceChannel) handleMessage(pc *externalConn, msg ExternalServiceMessage) {
	switch msg.Type {
	case TypePing:
		pong := newMessage(TypePong, nil)
		pong.ID = msg.ID
		_ = pc.writeJSON(pong)
	case TypeMessageSend:
		c.handleMessageSend(pc, msg)
	default:
		_ = pc.writeJSON(newError("unknown_type", fmt.Sprintf("unknown message type: %s", msg.Type)))
	}
}

func (c *ExternalServiceChannel) handleMessageSend(pc *externalConn, msg ExternalServiceMessage) {
	content := getPayloadString(msg.Payload, "content")
	if strings.TrimSpace(content) == "" {
		_ = pc.writeJSON(newError("empty_content", "message content is empty"))
		return
	}

	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(getPayloadString(msg.Payload, "session_id"))
	}
	if sessionID == "" {
		_ = pc.writeJSON(newError("missing_session_id", "session_id is required"))
		return
	}

	chatID := strings.TrimSpace(getPayloadString(msg.Payload, "chat_id"))
	if chatID == "" {
		chatID = buildChatID(sessionID)
	}

	peerKind := strings.TrimSpace(getPayloadString(msg.Payload, "peer_kind"))
	if peerKind == "" {
		peerKind = "direct"
	}
	peerID := strings.TrimSpace(getPayloadString(msg.Payload, "peer_id"))
	if peerID == "" {
		peerID = chatID
	}

	senderID := strings.TrimSpace(getPayloadString(msg.Payload, "sender_id"))
	if senderID == "" {
		senderID = "external-service-user"
	}

	c.chatIDMapping.Store(chatID, map[string]string{
		"sessionID": sessionID,
		"connID":    pc.id,
	})

	peer := bus.Peer{Kind: peerKind, ID: peerID}
	metadata := map[string]string{
		"platform":   "external_service",
		"session_id": sessionID,
		"conn_id":    pc.id,
	}
	sender := bus.SenderInfo{
		Platform:    "external_service",
		PlatformID:  senderID,
		CanonicalID: identity.BuildCanonicalID("external_service", senderID),
	}
	if !c.IsAllowedSender(sender) {
		return
	}
	c.HandleMessage(c.ctx, peer, msg.ID, senderID, chatID, content, nil, metadata, sender)
}

func buildChatID(sessionID string) string {
	return "external_service:" + sessionID
}

func sessionIDFromChatID(chatID string) string {
	if after, ok := strings.CutPrefix(chatID, "external_service:"); ok {
		return after
	}
	return chatID
}

func getPayloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	if v, ok := payload[key].(string); ok {
		return v
	}
	return ""
}
