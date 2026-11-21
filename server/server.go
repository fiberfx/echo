// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

// Package server runs HTTP handlers with listener, TLS and graceful shutdown support.
package server

import (
	"cmp"
	stdContext "context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// ErrInvalidCertOrKeyType indicates a certificate or key that is neither a path nor PEM bytes.
var ErrInvalidCertOrKeyType = errors.New("invalid cert or key type, must be string or []byte")

const (
	banner = "MX Echo (v%s). High performance, minimalist Go web framework https://github.com/fiberfx/mx-echo"
)

// StartConfig configures an HTTP or HTTPS server for an http.Handler.
type StartConfig struct {
	// Logger receives startup, HTTP server and shutdown messages. By default they go to stdout as JSON.
	Logger *slog.Logger
	// BannerVersion enables the MX Echo startup banner with the given version when HideBanner is false.
	BannerVersion string

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
	// OnStop is called once, after the listener closed and the stop ended, before Start returns.
	// Setting it also makes Start close the remaining connections when the grace expires; with it nil, Start
	// behaves as at v6.1.0 (it returns while those requests still run).
	OnStop func(StopReport)
	// Owner is cancelled when the stop begins and waited for within GracefulTimeout.
	Owner *Owner

	// BeforeServeFunc is callback that is called just before server starts to serve HTTP request.
	// Use this callback when you want to configure http.Server different timeouts/limits/etc
	BeforeServeFunc func(s *http.Server) error
}

// Start starts given Handler with HTTP(s) server.
func (sc StartConfig) Start(ctx stdContext.Context, h http.Handler) error {
	return sc.start(ctx, h)
}

// StartTLS starts given Handler with HTTPS server.
// If `certFile` or `keyFile` is `string` the values are treated as file paths.
// If `certFile` or `keyFile` is `[]byte` the values are treated as the certificate or key as-is.
func (sc StartConfig) StartTLS(ctx stdContext.Context, h http.Handler, certFile, keyFile any) error {
	certFs := sc.CertFilesystem
	if certFs == nil {
		certFs = os.DirFS(".")
	}
	cert, err := filepathOrContent(certFile, certFs)
	if err != nil {
		return err
	}
	key, err := filepathOrContent(keyFile, certFs)
	if err != nil {
		return err
	}
	cer, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return err
	}
	if sc.TLSConfig == nil {
		sc.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			NextProtos: []string{"h2"},
			//NextProtos: []string{"http/1.1"}, // Disallow "h2", allow http
		}
	}
	sc.TLSConfig.Certificates = []tls.Certificate{cer}
	return sc.start(ctx, h)
}

// start starts handler with HTTP(s) server.
func (sc StartConfig) start(ctx stdContext.Context, h http.Handler) error {
	logger := sc.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	server := http.Server{
		Handler:  h,
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
		// defaults for GoSec rule G112 // https://github.com/securego/gosec
		// G112 (CWE-400): Potential Slowloris Attack because ReadHeaderTimeout is not configured in the http.Server
		ReadTimeout: 30 * time.Second,
		// WriteTimeout is a max time allowed to write the response
		// IMPORTANT: set this to 0 when using Server-Sent-Events (SSE) or some larger duration when serving static files
		// WriteTimeout: 30 * time.Second,
	}

	listener := sc.Listener
	if listener == nil {
		listenerNetwork := cmp.Or(sc.ListenerNetwork, "tcp")

		ln, err := (&net.ListenConfig{}).Listen(ctx, listenerNetwork, sc.Address)
		if err != nil {
			return err
		}
		listener = ln

		if sc.TLSConfig != nil {
			listener = tls.NewListener(listener, sc.TLSConfig)
		}
	}

	if sc.ListenerAddrFunc != nil {
		sc.ListenerAddrFunc(listener.Addr())
	}

	if sc.BeforeServeFunc != nil {
		if err := sc.BeforeServeFunc(&server); err != nil {
			_ = listener.Close()
			return err
		}
	}
	if !sc.HideBanner && sc.BannerVersion != "" {
		bannerText := fmt.Sprintf(banner, sc.BannerVersion)
		logger.Info(bannerText, "version", sc.BannerVersion)
	}
	if !sc.HidePort {
		logger.Info("http(s) server started", "address", listener.Addr().String())
	}

	var report StopReport
	defer sc.stopped(&report, logger) // runs after wg.Wait: OnStop sees a stop that has ended

	wg := sync.WaitGroup{}
	defer wg.Wait() // wait for graceful shutdown goroutine to finish

	gCtx, cancel := stdContext.WithCancelCause(ctx) // end graceful goroutine when Serve returns early
	defer cancel(errServeEnded)

	if sc.GracefulTimeout >= 0 {
		wg.Go(func() {
			report = gracefulShutdown(gCtx, &sc, &server, logger)
		})
	}

	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func filepathOrContent(fileOrContent any, certFilesystem fs.FS) (content []byte, err error) {
	switch v := fileOrContent.(type) {
	case string:
		return fs.ReadFile(certFilesystem, v)
	case []byte:
		return v, nil
	default:
		return nil, ErrInvalidCertOrKeyType
	}
}

func gracefulShutdown(shutdownCtx stdContext.Context, sc *StartConfig, server *http.Server, logger *slog.Logger) StopReport {
	<-shutdownCtx.Done() // wait until shutdown context is closed.
	// note: is server if closed by other means this method is still run but is good as no-op
	report := sc.beginStop(shutdownCtx)

	timeout := sc.GracefulTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	waitShutdownCtx, cancel := stdContext.WithTimeout(stdContext.Background(), timeout)
	defer cancel()

	err := server.Shutdown(waitShutdownCtx)
	sc.endGrace(waitShutdownCtx, server, err, &report)
	if err != nil {
		// we end up here when listeners are not shut down within given timeout
		if sc.OnShutdownError != nil {
			sc.OnShutdownError(err)
			return report
		}
		logger.Error("failed to shut down server within given timeout", "error", err)
	}
	return report
}
