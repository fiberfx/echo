// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/fiberfx/mx-echo/v6/server"
)

func TestPlainHandlerLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	address := make(chan net.Addr, 1)
	done := make(chan error, 1)
	config := server.StartConfig{
		Address:          "127.0.0.1:0",
		HidePort:         true,
		ListenerAddrFunc: func(addr net.Addr) { address <- addr },
	}
	go func() {
		done <- config.Start(ctx, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		}))
	}()
	var addr net.Addr
	select {
	case addr = <-address:
	case err := <-done:
		t.Fatalf("server returned before listening: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop after cancellation")
	}
}

func TestBeforeServeFailureClosesSuppliedListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	rejected := errors.New("configuration rejected")
	config := server.StartConfig{
		Listener:        ln,
		BeforeServeFunc: func(*http.Server) error { return rejected },
	}
	if err := config.Start(t.Context(), http.NotFoundHandler()); err != rejected {
		t.Fatalf("Start error = %v, want hook error", err)
	}
	if err := ln.Close(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("listener close = %v, want already closed", err)
	}
}

func TestInvalidCertificateInput(t *testing.T) {
	config := server.StartConfig{}
	if err := config.StartTLS(t.Context(), http.NotFoundHandler(), 42, nil); err != server.ErrInvalidCertOrKeyType {
		t.Fatalf("StartTLS error = %v, want ErrInvalidCertOrKeyType", err)
	}
}
