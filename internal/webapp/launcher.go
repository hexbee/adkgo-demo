// Package webapp serves the project's fixed web interface and ADK REST API.
package webapp

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"google.golang.org/adk/v2/cmd/launcher"
	"google.golang.org/adk/v2/server/adkrest"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/telemetry"
)

//go:embed static/*
var staticFiles embed.FS

type config struct {
	port            int
	readTimeout     time.Duration
	idleTimeout     time.Duration
	shutdownTimeout time.Duration
	sseWriteTimeout time.Duration
	traceCapacity   int
}

type webLauncher struct {
	flags  *flag.FlagSet
	config *config
}

// NewLauncher returns a launcher that serves the custom UI at / and ADK at /api.
func NewLauncher() launcher.SubLauncher {
	cfg := &config{}
	flags := flag.NewFlagSet("web", flag.ContinueOnError)
	flags.IntVar(&cfg.port, "port", 8080, "Localhost port for the web app")
	flags.DurationVar(&cfg.readTimeout, "read-timeout", 15*time.Second, "Maximum time to read an HTTP request")
	flags.DurationVar(&cfg.idleTimeout, "idle-timeout", 60*time.Second, "Maximum keep-alive idle time")
	flags.DurationVar(&cfg.shutdownTimeout, "shutdown-timeout", 15*time.Second, "Graceful shutdown timeout")
	flags.DurationVar(&cfg.sseWriteTimeout, "sse-write-timeout", 120*time.Second, "Maximum duration of one streamed agent run")
	flags.IntVar(&cfg.traceCapacity, "trace-capacity", 10000, "Maximum number of in-memory ADK traces")
	return &webLauncher{flags: flags, config: cfg}
}

func (w *webLauncher) Keyword() string { return "web" }

func (w *webLauncher) Parse(args []string) ([]string, error) {
	if err := w.flags.Parse(args); err != nil {
		return nil, fmt.Errorf("parse web flags: %w", err)
	}
	return w.flags.Args(), nil
}

func (w *webLauncher) Run(ctx context.Context, cfg *launcher.Config) error {
	handler, err := newHandler(cfg, w.config)
	if err != nil {
		return err
	}
	telemetryProviders, err := telemetry.New(ctx, cfg.TelemetryOptions...)
	if err != nil {
		return fmt.Errorf("initialize telemetry: %w", err)
	}
	telemetryProviders.SetGlobalOtelProviders()

	server := &http.Server{
		Addr:        fmt.Sprintf(":%d", w.config.port),
		Handler:     handler,
		ReadTimeout: w.config.readTimeout,
		IdleTimeout: w.config.idleTimeout,
	}
	errCh := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	log.Printf("Web workbench: http://localhost:%d", w.config.port)
	log.Printf("ADK REST API: http://localhost:%d/api", w.config.port)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), w.config.shutdownTimeout)
		defer cancel()
		return errors.Join(server.Shutdown(shutdownCtx), telemetryProviders.Shutdown(shutdownCtx))
	case err, ok := <-errCh:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), w.config.shutdownTimeout)
		defer cancel()
		telemetryErr := telemetryProviders.Shutdown(shutdownCtx)
		if !ok {
			return telemetryErr
		}
		return errors.Join(fmt.Errorf("web server failed: %w", err), telemetryErr)
	}
}

func (w *webLauncher) CommandLineSyntax() string {
	var out strings.Builder
	w.flags.SetOutput(&out)
	w.flags.PrintDefaults()
	return out.String()
}

func (w *webLauncher) SimpleDescription() string {
	return "starts the fixed project web workbench and ADK REST API"
}

func newHandler(cfg *launcher.Config, webCfg *config) (http.Handler, error) {
	if cfg.SessionService == nil {
		cfg.SessionService = session.InMemoryService()
	}
	restServer, err := adkrest.NewServer(adkrest.ServerConfig{
		SessionService:  cfg.SessionService,
		MemoryService:   cfg.MemoryService,
		AgentLoader:     cfg.AgentLoader,
		ArtifactService: cfg.ArtifactService,
		SSEWriteTimeout: webCfg.sseWriteTimeout,
		PluginConfig:    cfg.PluginConfig,
		DebugConfig: adkrest.DebugTelemetryConfig{
			TraceCapacity: webCfg.traceCapacity,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create ADK REST server: %w", err)
	}
	cfg.TelemetryOptions = append(cfg.TelemetryOptions,
		telemetry.WithSpanProcessors(restServer.SpanProcessor()),
		telemetry.WithLogRecordProcessors(restServer.LogProcessor()),
	)

	assets, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, fmt.Errorf("prepare embedded web assets: %w", err)
	}

	router := mux.NewRouter()
	router.PathPrefix("/api/").Handler(http.StripPrefix("/api", restServer))
	router.Handle("/api", http.StripPrefix("/api", restServer))
	router.PathPrefix("/assets/").Handler(http.StripPrefix("/assets/", http.FileServer(http.FS(assets)))).Methods(http.MethodGet)
	router.HandleFunc("/favicon.svg", serveAsset(assets, "favicon.svg", "image/svg+xml")).Methods(http.MethodGet)
	router.HandleFunc("/", serveAsset(assets, "index.html", "text/html; charset=utf-8")).Methods(http.MethodGet)
	return securityHeaders(router), nil
}

func serveAsset(assets fs.FS, name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		content, err := fs.ReadFile(assets, name)
		if err != nil {
			http.Error(w, "asset unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
