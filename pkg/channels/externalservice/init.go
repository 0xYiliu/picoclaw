package externalservice

import (
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
)

func init() {
	channels.RegisterFactory("external_service", func(cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
		return NewExternalServiceChannel(cfg.Channels.ExternalService, b)
	})
}
