package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/you/gnasty-chat/internal/core"
	"github.com/you/gnasty-chat/internal/httpapi"
	"github.com/you/gnasty-chat/internal/sink"
	"github.com/you/gnasty-chat/internal/twitch"
	"github.com/you/gnasty-chat/internal/twitchirc"
	"github.com/you/gnasty-chat/internal/ytlive"
)

var (
	version = "dev"
	gitSHA  = "unknown"
	builtAt = ""
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	var (
		dbPath          string
		twChannel       string
		twNick          string
		twToken         string
		twTokenFile     string
		twTLS           bool
		ytURL           string
		httpAddr        string
		httpCorsOrigins string
		httpRateRPS     int
		httpRateBurst   int
		httpMetrics     bool
		httpAccessLog   bool
		httpPprof       bool
	)

	flag.StringVar(&dbPath, "sqlite", "chat.db", "Path to SQLite database file")
	flag.StringVar(&twChannel, "twitch-channel", "", "Twitch channel to join (without #)")
	flag.StringVar(&twNick, "twitch-nick", "", "Twitch nickname to login as")
	flag.StringVar(&twToken, "twitch-token", "", "Twitch OAuth token (format: oauth:xxxxx)")
	flag.StringVar(&twTokenFile, "twitch-token-file", "", "Path to file containing the Twitch OAuth token")
	flag.BoolVar(&twTLS, "twitch-tls", true, "Use TLS (port 6697) for Twitch IRC connection")
	flag.StringVar(&ytURL, "youtube-url", "", "YouTube live/watch URL")
	flag.StringVar(&httpAddr, "http-addr", "", "HTTP status/stream address (e.g., :8765)")
	flag.StringVar(&httpCorsOrigins, "http-cors-origins", "", "Comma-separated list of allowed CORS origins")
	flag.IntVar(&httpRateRPS, "http-rate-rps", 20, "Maximum HTTP requests per second per client")
	flag.IntVar(&httpRateBurst, "http-rate-burst", 40, "Burst size for HTTP rate limiter")
	flag.BoolVar(&httpMetrics, "http-metrics", true, "Expose Prometheus metrics endpoint")
	flag.BoolVar(&httpAccessLog, "http-access-log", true, "Log HTTP access records")
	flag.BoolVar(&httpPprof, "http-pprof", false, "Expose pprof handlers under /debug/pprof")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("harvester: received %s, shutting down", sig)
		cancel()
	}()

	sinkDB, err := sink.OpenSQLite(dbPath)
	if err != nil {
		log.Fatalf("harvester: open sqlite: %v", err)
	}
	defer func() {
		if err := sinkDB.Close(); err != nil {
			log.Printf("harvester: closing sink: %v", err)
		}
	}()

	if err := sinkDB.Ping(); err != nil {
		log.Fatalf("harvester: ping sqlite: %v", err)
	}

	type messageWriter interface {
		Write(core.ChatMessage) error
	}

	var (
		writer messageWriter = sinkDB
		api    *httpapi.Server
	)

	var corsOrigins []string
	if strings.TrimSpace(httpCorsOrigins) != "" {
		for _, origin := range strings.Split(httpCorsOrigins, ",") {
			origin = strings.TrimSpace(origin)
			if origin != "" {
				corsOrigins = append(corsOrigins, origin)
			}
		}
	}

	build := httpapi.BuildInfo{Version: version, Revision: gitSHA}
	if builtAt != "" {
		if t, err := time.Parse(time.RFC3339, builtAt); err == nil {
			build.BuiltAt = t
		}
	}

	if httpAddr != "" {
		api = httpapi.New(sinkDB, httpapi.Options{
			Addr:            httpAddr,
			CORSOrigins:     corsOrigins,
			RateLimitRPS:    httpRateRPS,
			RateLimitBurst:  httpRateBurst,
			EnableMetrics:   httpMetrics,
			EnableAccessLog: httpAccessLog,
			EnablePprof:     httpPprof,
			Build:           build,
		})
		go func() {
			if err := api.Start(); err != nil {
				log.Fatalf("harvester: http api: %v", err)
			}
		}()
		writer = sink.WithAPI(sinkDB, api)
		log.Printf("harvester: http api ready on %s", httpAddr)
	}

	started := 0

	if strings.TrimSpace(twChannel) != "" {
		if strings.TrimSpace(twNick) == "" {
			log.Fatal("harvester: twitch-nick is required when twitch-channel/token provided")
		}

		handler := func(msg core.ChatMessage) {
			if err := writer.Write(msg); err != nil {
				log.Printf("harvester: write twitch message: %v", err)
				if api != nil {
					api.ReportDBWriteError()
				}
			}
		}

		var (
			token  string
			loader *twitch.FileTokenLoader
		)

		if strings.TrimSpace(twTokenFile) != "" {
			loader = twitch.NewFileTokenLoader(twTokenFile)
			if loaded, _, err := loader.Load(); err == nil {
				if loaded != "" {
					token = loaded
				}
			} else if !errors.Is(err, twitch.ErrEmptyToken) {
				log.Printf("harvester: twitch token file: %v", err)
			}
		}

		if token == "" {
			token = twitch.NormalizeToken(twToken)
		}

		if token == "" {
			log.Printf("harvester: twitch token not provided; skipping twitch receiver")
		} else {
			if loader != nil {
				loader.SetCached(token)
			}

			cfg := twitchirc.Config{
				Channel: twChannel,
				Nick:    twNick,
				Token:   token,
				UseTLS:  twTLS,
			}

			started++
			go runTwitchWithReload(ctx, cancel, cfg, handler, loader)
			log.Printf("harvester: twitch receiver started for #%s", twChannel)
		}
	}

	if ytURL != "" {
		handler := func(msg core.ChatMessage) {
			if err := writer.Write(msg); err != nil {
				log.Printf("harvester: write youtube message: %v", err)
				if api != nil {
					api.ReportDBWriteError()
				}
			}
		}
		client := ytlive.New(ytlive.Config{LiveURL: ytURL}, handler)
		started++
		go func() {
			if err := client.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("harvester: youtube client exited: %v", err)
				cancel()
			}
		}()
		log.Printf("harvester: youtube receiver started for %s", ytURL)
	}

	if started == 0 {
		log.Printf("harvester: no receivers configured")
	}

	<-ctx.Done()

	if api != nil {
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
		if err := api.Shutdown(shutdownCtx); err != nil {
			log.Printf("harvester: http api shutdown: %v", err)
		}
		cancelShutdown()
	}

	// allow receiver goroutines to finish cleanly
	time.Sleep(100 * time.Millisecond)
	log.Printf("harvester: shutdown complete")
}

func runTwitchWithReload(
	ctx context.Context,
	cancel context.CancelFunc,
	baseCfg twitchirc.Config,
	handler func(core.ChatMessage),
	loader *twitch.FileTokenLoader,
) {
	startClient := func(cfg twitchirc.Config) (context.CancelFunc, <-chan struct{}) {
		runCtx, runCancel := context.WithCancel(ctx)
		done := make(chan struct{})
		client := twitchirc.New(cfg, handler)
		go func() {
			defer close(done)
			if err := client.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("harvester: twitch client exited: %v", err)
				cancel()
			}
		}()
		return runCancel, done
	}

	cfg := baseCfg
	cancelCurrent, doneCurrent := startClient(cfg)

	if loader == nil {
		<-ctx.Done()
		cancelCurrent()
		<-doneCurrent
		return
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			cancelCurrent()
			<-doneCurrent
			return
		case <-ticker.C:
			token, changed, err := loader.Load()
			if err != nil {
				if !errors.Is(err, twitch.ErrEmptyToken) {
					log.Printf("harvester: twitch token file: %v", err)
				}
				continue
			}
			if !changed {
				continue
			}
			if token == "" {
				continue
			}

			log.Printf("twitch: token reload detected; reconnecting")

			cancelCurrent()
			<-doneCurrent

			cfg.Token = token
			cancelCurrent, doneCurrent = startClient(cfg)
		}
	}
}
