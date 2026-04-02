package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/lavr/express-botx/internal/apm"
	"github.com/lavr/express-botx/internal/botapi"
	"github.com/lavr/express-botx/internal/config"
	vlog "github.com/lavr/express-botx/internal/log"
	"github.com/lavr/express-botx/internal/mentions"
	"github.com/lavr/express-botx/internal/queue"
)

func runWorker(args []string, deps Deps) error {
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags
	var noCatalogPublish bool
	var healthListen string
	var dryRun bool

	globalFlags(fs, &flags)
	fs.BoolVar(&noCatalogPublish, "no-catalog-publish", false, "disable catalog snapshot publishing")
	fs.StringVar(&healthListen, "health-listen", "", "health check listen address (e.g. :8081)")
	fs.BoolVar(&dryRun, "dry-run", false, "process messages without sending to BotX API")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, `Usage: express-botx worker [options]

Consume messages from the work queue and send them via BotX API.

The worker reads queued messages, resolves bot credentials by bot_id from its
local config, and sends each message to the BotX API. Results are optionally
published to a reply queue.

By default, the worker also publishes routing catalog snapshots for producers.
Use --no-catalog-publish to disable this behavior.

Options:
`)
		fs.PrintDefaults()
	}

	if hasHelpFlag(args) {
		fs.Usage()
		return nil
	}

	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	applyVerboseFlag(flags)

	cfg, err := config.LoadForWorker(flags)
	if err != nil {
		return err
	}

	// Apply CLI overrides for catalog publishing
	if noCatalogPublish {
		f := false
		cfg.Catalog.Publish = &f
	}
	if healthListen != "" {
		cfg.Worker.HealthListen = healthListen
	}

	// Create queue consumer and publisher
	consumer, err := queue.NewConsumer(cfg.Queue.Driver, cfg.Queue.URL, cfg.Queue.Name, cfg.Queue.Group)
	if err != nil {
		return fmt.Errorf("creating queue consumer: %w", err)
	}
	defer consumer.Close()

	publisher, err := queue.NewPublisher(cfg.Queue.Driver, cfg.Queue.URL, cfg.Queue.Name)
	if err != nil {
		return fmt.Errorf("creating queue publisher: %w", err)
	}
	defer publisher.Close()

	// APM
	provider := apm.New()
	defer provider.Shutdown()

	// Build worker
	w, err := newWorkerRunner(cfg, publisher, provider, dryRun)
	if err != nil {
		return err
	}

	// Signal handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start health check server
	var healthSrv *http.Server
	if cfg.Worker.HealthListen != "" {
		healthSrv = w.startHealthServer(cfg.Worker.HealthListen)
	}

	w.ready.Store(true)
	w.healthy.Store(true)

	// Start catalog publishing if enabled
	catalogEnabled := cfg.Catalog.Publish == nil || *cfg.Catalog.Publish // default: true
	if catalogEnabled && cfg.Catalog.QueueName != "" {
		w.startCatalogPublisher(ctx, cfg.Catalog.QueueName, cfg.Catalog.PublishInterval)
	}

	vlog.Info("worker: starting (retry=%d, backoff=%s, shutdown_timeout=%s)",
		w.retryCount, w.retryBackoff, w.shutdownTimeout)

	// Consume work messages
	err = consumer.ConsumeWork(ctx, w.handleMessage)
	if err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("consuming work queue: %w", err)
	}

	// If the consumer returned but context is still active, the connection was
	// lost (e.g. RabbitMQ channel closed). Exit with error so the process
	// restarts instead of hanging forever.
	if ctx.Err() == nil {
		return fmt.Errorf("work consumer exited unexpectedly (connection lost?)")
	}

	// Graceful shutdown
	w.ready.Store(false)
	vlog.Info("worker: shutting down, waiting for in-flight messages (timeout=%s)...", w.shutdownTimeout)

	// Wait for in-flight to complete with timeout
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		vlog.Info("worker: all in-flight messages processed")
	case <-time.After(w.shutdownTimeout):
		vlog.Info("worker: shutdown timeout, %d messages still in-flight", w.inflight.Load())
	}

	w.healthy.Store(false)

	if healthSrv != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		healthSrv.Shutdown(shutCtx)
	}

	return nil
}

// workerRunner holds worker state and configuration.
type workerRunner struct {
	cfg             *config.Config
	publisher       queue.Publisher
	apm             apm.Provider
	retryCount      int
	retryBackoff    time.Duration
	shutdownTimeout time.Duration
	dryRun          bool

	healthy  atomic.Bool
	ready    atomic.Bool
	inflight atomic.Int64
	wg       sync.WaitGroup

	// Per-bot client cache (protected by mu)
	mu      sync.Mutex
	clients map[string]*workerBotClient
}

type workerBotClient struct {
	client *botapi.Client
	cfg    *config.Config // bot-specific config for auth
}

func newWorkerRunner(cfg *config.Config, pub queue.Publisher, provider apm.Provider, dryRun bool) (*workerRunner, error) {
	retryCount := 3
	if cfg.Worker.RetryCount > 0 {
		retryCount = cfg.Worker.RetryCount
	}

	retryBackoff := time.Second
	if cfg.Worker.RetryBackoff != "" {
		d, err := time.ParseDuration(cfg.Worker.RetryBackoff)
		if err != nil {
			return nil, fmt.Errorf("invalid worker.retry_backoff %q: %w", cfg.Worker.RetryBackoff, err)
		}
		retryBackoff = d
	}

	shutdownTimeout := 30 * time.Second
	if cfg.Worker.ShutdownTimeout != "" {
		d, err := time.ParseDuration(cfg.Worker.ShutdownTimeout)
		if err != nil {
			return nil, fmt.Errorf("invalid worker.shutdown_timeout %q: %w", cfg.Worker.ShutdownTimeout, err)
		}
		shutdownTimeout = d
	}

	return &workerRunner{
		cfg:             cfg,
		publisher:       pub,
		apm:             provider,
		retryCount:      retryCount,
		retryBackoff:    retryBackoff,
		shutdownTimeout: shutdownTimeout,
		dryRun:          dryRun,
		clients:         make(map[string]*workerBotClient),
	}, nil
}

// handleMessage processes a single work message: looks up bot, authenticates, sends, publishes result.
func (w *workerRunner) handleMessage(ctx context.Context, msg *queue.WorkMessage) error {
	w.wg.Add(1)
	w.inflight.Add(1)
	defer w.wg.Done()
	defer w.inflight.Add(-1)

	txn := w.apm.StartTransaction("worker.process")
	defer txn.End()

	start := time.Now()

	// Look up bot by bot_id
	botName, botCfg, err := w.cfg.BotByID(msg.Routing.BotID)
	if err != nil {
		elapsed := time.Since(start)
		vlog.Info("worker: request_id=%s bot_id=%s chat_id=%s duration=%dms status=failed error=%q",
			msg.RequestID, msg.Routing.BotID, msg.Routing.ChatID, elapsed.Milliseconds(), err.Error())
		w.publishResult(ctx, msg, "failed", "", err.Error())
		return nil // ack the message, validation error → no retry
	}

	// Get or create bot client
	bc := w.getOrCreateClient(botName, botCfg)

	// Parse inline mentions using the resolved bot's host for email lookups.
	if !msg.Payload.Opts.NoParse {
		resolver := &refreshableClientResolver{
			client: botapi.NewClient(bc.cfg.Host, bc.client.Token, bc.cfg.HTTPTimeout()),
			cfg:    bc.cfg,
		}
		parseResult := mentions.Parse(ctx, msg.Payload.Message, msg.Payload.Mentions, true, resolver)
		msg.Payload.Message = parseResult.Message
		msg.Payload.Mentions = parseResult.Mentions
		for _, e := range parseResult.Errors {
			vlog.V2("worker: request_id=%s mentions parse: %s: %s", msg.RequestID, e.Kind, e.Cause)
		}
	}

	// Build send request from work message
	sr := buildSendRequestFromWork(msg)

	// Dry-run: skip authentication and sending
	if w.dryRun {
		elapsed := time.Since(start)
		vlog.Info("worker: request_id=%s bot_id=%s chat_id=%s duration=%dms status=dry-run",
			msg.RequestID, msg.Routing.BotID, msg.Routing.ChatID, elapsed.Milliseconds())
		w.publishResult(ctx, msg, "dry-run", "", "")
		return nil
	}

	// Send with retry
	var syncID string
	var lastErr error
	for attempt := 0; attempt <= w.retryCount; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(float64(w.retryBackoff) * math.Pow(5, float64(attempt-1)))
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
			vlog.Info("worker: request_id=%s bot_id=%s attempt=%d backoff=%s",
				msg.RequestID, msg.Routing.BotID, attempt+1, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		// Ensure client has a token
		if bc.client.Token == "" {
			tok, _, authErr := authenticate(bc.cfg)
			if authErr != nil {
				lastErr = fmt.Errorf("authenticating bot %s: %w", botName, authErr)
				continue
			}
			bc.client.Token = tok
		}

		syncID, lastErr = bc.client.SendWithSyncID(ctx, sr)
		if lastErr == nil {
			break
		}

		if errors.Is(lastErr, botapi.ErrUnauthorized) {
			if bc.cfg.BotToken != "" {
				// Static token rejected — permanent failure
				lastErr = fmt.Errorf("bot token rejected (401) for bot %q", botName)
				break
			}
			// Try token refresh
			newTok, _, refreshErr := authenticate(bc.cfg)
			if refreshErr != nil {
				lastErr = fmt.Errorf("token refresh failed for bot %q: %w", botName, refreshErr)
				break
			}
			bc.client.Token = newTok
			// Retry with new token
			syncID, lastErr = bc.client.SendWithSyncID(ctx, sr)
			if lastErr == nil {
				break
			}
			if errors.Is(lastErr, botapi.ErrUnauthorized) {
				lastErr = fmt.Errorf("persistent 401 after token refresh for bot %q", botName)
				break
			}
		}
	}

	elapsed := time.Since(start)

	if lastErr != nil {
		vlog.Info("worker: request_id=%s bot_id=%s chat_id=%s duration=%dms status=failed error=%q",
			msg.RequestID, msg.Routing.BotID, msg.Routing.ChatID, elapsed.Milliseconds(), lastErr.Error())
		w.publishResult(ctx, msg, "failed", "", lastErr.Error())
		return lastErr // return error so driver can nack (enables DLQ routing)
	}

	vlog.Info("worker: request_id=%s bot_id=%s chat_id=%s duration=%dms status=sent sync_id=%s",
		msg.RequestID, msg.Routing.BotID, msg.Routing.ChatID, elapsed.Milliseconds(), syncID)
	w.publishResult(ctx, msg, "sent", syncID, "")
	return nil
}

// getOrCreateClient returns a cached bot client or creates a new one.
func (w *workerRunner) getOrCreateClient(botName string, botCfg config.BotConfig) *workerBotClient {
	w.mu.Lock()
	defer w.mu.Unlock()

	if bc, ok := w.clients[botCfg.ID]; ok {
		return bc
	}

	// Build a bot-specific config for authentication
	botSpecificCfg := &config.Config{
		Host:       botCfg.Host,
		BotID:      botCfg.ID,
		BotSecret:  botCfg.Secret,
		BotToken:   botCfg.Token,
		BotName:    botName,
		BotTimeout: botCfg.Timeout,
		Cache:      w.cfg.Cache,
	}

	bc := &workerBotClient{
		client: botapi.NewClient(botCfg.Host, "", botSpecificCfg.HTTPTimeout()),
		cfg:    botSpecificCfg,
	}
	w.clients[botCfg.ID] = bc
	return bc
}

// publishResult publishes a work result to the reply queue (if configured).
func (w *workerRunner) publishResult(ctx context.Context, msg *queue.WorkMessage, status, syncID, errMsg string) {
	if msg.ReplyTo == "" {
		return
	}
	result := &queue.WorkResult{
		RequestID: msg.RequestID,
		Status:    status,
		SyncID:    syncID,
		Error:     errMsg,
		SentAt:    time.Now().UTC(),
	}
	if err := w.publisher.PublishResult(ctx, msg.ReplyTo, result); err != nil {
		vlog.Info("worker: failed to publish result for request_id=%s: %v", msg.RequestID, err)
	}
}

// buildSendRequestFromWork converts a WorkMessage into a BotX API SendRequest.
func buildSendRequestFromWork(msg *queue.WorkMessage) *botapi.SendRequest {
	params := &botapi.SendParams{
		ChatID:   msg.Routing.ChatID,
		Message:  msg.Payload.Message,
		Status:   msg.Payload.Status,
		Metadata: msg.Payload.Metadata,
		Mentions: msg.Payload.Mentions,
		Silent:   msg.Payload.Opts.Silent,
		Stealth:  msg.Payload.Opts.Stealth,
		ForceDND: msg.Payload.Opts.ForceDND,
		NoNotify: msg.Payload.Opts.NoNotify,
	}

	if msg.Payload.File != nil {
		params.File = &botapi.SendFile{
			FileName: msg.Payload.File.FileName,
			Data:     msg.Payload.File.Data,
		}
	}

	if params.Status == "" {
		params.Status = "ok"
	}

	return botapi.BuildSendRequest(params)
}

// startHealthServer starts a minimal HTTP server for health and readiness checks.
func (w *workerRunner) startHealthServer(addr string) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		if w.healthy.Load() {
			rw.WriteHeader(http.StatusOK)
			rw.Write([]byte(`{"ok":true}` + "\n"))
		} else {
			rw.WriteHeader(http.StatusServiceUnavailable)
			rw.Write([]byte(`{"ok":false}` + "\n"))
		}
	})

	mux.HandleFunc("GET /readyz", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		if w.ready.Load() {
			rw.WriteHeader(http.StatusOK)
			rw.Write([]byte(`{"ok":true}` + "\n"))
		} else {
			rw.WriteHeader(http.StatusServiceUnavailable)
			rw.Write([]byte(`{"ok":false}` + "\n"))
		}
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		vlog.Info("worker: health server listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			vlog.Info("worker: health server error: %v", err)
		}
	}()

	return srv
}

// startCatalogPublisher publishes a catalog snapshot on startup and then
// periodically at the configured interval. Stops when ctx is cancelled.
func (w *workerRunner) startCatalogPublisher(ctx context.Context, catalogTopic string, intervalStr string) {
	interval := 30 * time.Second
	if intervalStr != "" {
		d, err := time.ParseDuration(intervalStr)
		if err != nil {
			vlog.Info("worker: invalid catalog.publish_interval %q, using default %s", intervalStr, interval)
		} else {
			interval = d
		}
	}

	// Publish immediately on startup
	w.publishCatalogSnapshot(ctx, catalogTopic)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.publishCatalogSnapshot(ctx, catalogTopic)
			}
		}
	}()

	vlog.Info("worker: catalog publishing enabled (topic=%s, interval=%s)", catalogTopic, interval)
}

// publishCatalogSnapshot builds a snapshot from config and publishes it.
func (w *workerRunner) publishCatalogSnapshot(ctx context.Context, catalogTopic string) {
	snap := queue.BuildCatalogSnapshot(w.cfg)
	if err := w.publisher.PublishCatalog(ctx, catalogTopic, snap); err != nil {
		vlog.Info("worker: failed to publish catalog snapshot: %v", err)
		return
	}
	vlog.V1("worker: published catalog snapshot (revision=%s, bots=%d, chats=%d)",
		snap.Revision, len(snap.Bots), len(snap.Chats))
}
