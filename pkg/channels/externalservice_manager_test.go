package channels_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	_ "github.com/sipeed/picoclaw/pkg/channels/externalservice"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestRegisterFactory(t *testing.T) {
	cfg := &config.Config{}
	cfg.Channels.ExternalService.Enabled = true
	cfg.Channels.ExternalService.Token = "secret"
	mgr, err := channels.NewManager(cfg, bus.NewMessageBus(), nil)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if _, ok := mgr.GetChannel("external_service"); !ok {
		t.Fatal("expected external_service to be initialized via registered factory")
	}
}

func TestManagerInitExternalService(t *testing.T) {
	t.Run("enabled with token initializes", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Channels.ExternalService.Enabled = true
		cfg.Channels.ExternalService.Token = "secret"
		mgr, err := channels.NewManager(cfg, bus.NewMessageBus(), nil)
		if err != nil {
			t.Fatalf("NewManager() error = %v", err)
		}
		if _, ok := mgr.GetChannel("external_service"); !ok {
			t.Fatal("expected external_service channel to be enabled")
		}
	})

	t.Run("missing token does not initialize", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Channels.ExternalService.Enabled = true
		mgr, err := channels.NewManager(cfg, bus.NewMessageBus(), nil)
		if err != nil {
			t.Fatalf("NewManager() error = %v", err)
		}
		if _, ok := mgr.GetChannel("external_service"); ok {
			t.Fatal("did not expect external_service channel without token")
		}
	})
}

func TestSetupHTTPServerRegistersExternalService(t *testing.T) {
	cfg := &config.Config{}
	cfg.Channels.ExternalService.Enabled = true
	cfg.Channels.ExternalService.Token = "secret"
	mgr, err := channels.NewManager(cfg, bus.NewMessageBus(), nil)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	mgr.SetupHTTPServer(addr, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := mgr.StartAll(ctx); err != nil {
		t.Fatalf("StartAll() error = %v", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = mgr.StopAll(shutdownCtx)
	}()

	var resp *http.Response
	for range 20 {
		resp, err = http.Get(fmt.Sprintf("http://%s/external-service/ws", addr))
		if err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("http.Get() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("StatusCode = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}
