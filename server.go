// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"context"
	"crypto/tls"
	"io/fs"
	"net"
	"net/http"
	"time"

	"github.com/fiberfx/mx-echo/v6/server"
)

// StartConfig is for creating configured http.Server instance to start serve http(s) requests with given Echo instance
type StartConfig struct {
	// Address specifies the address where listener will start listening on to serve HTTP(s) requests
	Address string

	// HideBanner instructs Start* method not to print banner when starting the Server.
	HideBanner bool
	// HidePort instructs Start* method not to print port when starting the Server.
	HidePort bool

	// CertFilesystem is filesystem is used to read `certFile` and `keyFile` when StartTLS method is called.
	CertFilesystem fs.FS

	// TLSConfig is used to configure TLS. If Listener is set, TLSConfig is not used to create the listener.
	TLSConfig *tls.Config

	// Listener is used to start server with the custom listener.
	Listener net.Listener
	// ListenerNetwork is used configure on which Network listener will use.
	// If Listener is set, ListenerNetwork is not used.
	ListenerNetwork string
	// ListenerAddrFunc will be called after listener is created and started to listen for connections. This is useful in
	// testing situations when server is started on random port `address = ":0"` in that case you can get actual port where
	// listener is listening on.
	ListenerAddrFunc func(addr net.Addr)

	// GracefulTimeout is timeout value (defaults to 10sec) graceful shutdown will wait for server to handle ongoing requests
	// before shutting down the server.
	GracefulTimeout time.Duration
	// OnShutdownError is called when graceful shutdown results an error. for example when listeners are not shut down within
	// given timeout
	OnShutdownError func(err error)

	// BeforeServeFunc is callback that is called just before server starts to serve HTTP request.
	// Use this callback when you want to configure http.Server different timeouts/limits/etc
	BeforeServeFunc func(s *http.Server) error
}

// Start starts the given handler with an HTTP or HTTPS server.
func (sc StartConfig) Start(ctx context.Context, h http.Handler) error {
	return sc.serverConfig(h).Start(ctx, h)
}

// StartTLS serves the handler with a certificate and key supplied as file paths or PEM bytes.
func (sc StartConfig) StartTLS(ctx context.Context, h http.Handler, certFile, keyFile any) error {
	return sc.serverConfig(h).StartTLS(ctx, h, certFile, keyFile)
}

func (sc StartConfig) serverConfig(h http.Handler) server.StartConfig {
	config := server.StartConfig{
		Address:          sc.Address,
		HideBanner:       sc.HideBanner,
		HidePort:         sc.HidePort,
		CertFilesystem:   sc.CertFilesystem,
		TLSConfig:        sc.TLSConfig,
		Listener:         sc.Listener,
		ListenerNetwork:  sc.ListenerNetwork,
		ListenerAddrFunc: sc.ListenerAddrFunc,
		GracefulTimeout:  sc.GracefulTimeout,
		OnShutdownError:  sc.OnShutdownError,
		BeforeServeFunc:  sc.BeforeServeFunc,
		BannerVersion:    Version,
	}
	if e, ok := h.(*Echo); ok && e != nil {
		config.Logger = e.Logger
	}
	return config
}
