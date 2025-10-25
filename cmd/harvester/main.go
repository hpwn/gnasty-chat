package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/you/gnasty-chat/internal/core"
	"github.com/you/gnasty-chat/internal/harvester"
	httpadmin "github.com/you/gnasty-chat/internal/http"
	"github.com/you/gnasty-chat/internal/httpapi"
	"github.com/you/gnasty-chat/internal/sink"
	"github.com/you/gnasty-chat/internal/twitch"
	"github.com/you/gnasty-chat/internal/twitchauth"
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
		twClientID      string
		twClientSecret  string
		twRefreshToken  string
		twRefreshFile   string
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
	flag.StringVar(&twClientID, "twitch-client-id", "", "Twitch application client ID")
	flag.StringVar(&twClientSecret, "twitch-client-secret", "", "Twitch application client secret")
	flag.StringVar(&twRefreshToken, "twitch-refresh-token", "", "Twitch OAuth refresh token")
	flag.StringVar(&twRefreshFile, "twitch-refresh-token-file", "", "Path to file containing the Twitch refresh token")
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

	if twClientID == "" {
		twClientID = strings.TrimSpace(os.Getenv("TWITCH_CLIENT_ID"))
	}
	if twClientSecret == "" {
		twClientSecret = strings.TrimSpace(os.Getenv("TWITCH_CLIENT_SECRET"))
	}
	if twRefreshToken == "" {
		twRefreshToken = strings.TrimSpace(os.Getenv("TWITCH_REFRESH_TOKEN"))
	}
	if twRefreshFile == "" {
		twRefreshFile = strings.TrimSpace(os.Getenv("TWITCH_REFRESH_TOKEN_FILE"))
	}
	if twTokenFile == "" {
		if env := strings.TrimSpace(os.Getenv("TWITCH_TOKEN_FILE")); env != "" {
			twTokenFile = env
		}
	}

	twClientID = strings.TrimSpace(twClientID)
	twClientSecret = strings.TrimSpace(twClientSecret)
	twRefreshToken = strings.TrimSpace(twRefreshToken)
	twRefreshFile = strings.TrimSpace(twRefreshFile)
	twTokenFile = strings.TrimSpace(twTokenFile)

	tokenFiles := twitchauth.TokenFiles{
		AccessPath:   twTokenFile,
		RefreshPath:  twRefreshFile,
		ClientID:     twClientID,
		ClientSecret: twClientSecret,
	}
	har := harvester.New(tokenFiles, nil)

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
		if har != nil {
			admin := httpadmin.New(har)
			admin.Register(api.Mux())
		}
		go func() {
			if err := api.Start(); err != nil {
				log.Fatalf("harvester: http api: %v", err)
			}
		}()
		writer = sink.WithAPI(sinkDB, api)
		log.Printf("harvester: http api ready on %s", httpAddr)
	}

	started := 0

	channel := strings.TrimSpace(twChannel)
	if channel != "" {
		nick := strings.TrimSpace(twNick)
		if nick == "" {
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

		tokenFilePath := twTokenFile

		if twRefreshFile != "" {
			data, err := os.ReadFile(twRefreshFile)
			if err != nil {
				log.Printf("harvester: twitch refresh token file: %v", err)
			} else {
				twRefreshToken = strings.TrimSpace(string(data))
			}
		}

		var (
			token      string
			loader     *twitch.FileTokenLoader
			refreshMgr *twitch.RefreshManager
		)
		tokenUpdates := make(chan tokenUpdate, 4)

		if tokenFilePath != "" {
			loader = twitch.NewFileTokenLoader(tokenFilePath)
			if loaded, _, err := loader.Load(); err == nil {
				if loaded != "" {
					token = loaded
				}
			} else if !errors.Is(err, twitch.ErrEmptyToken) {
				log.Printf("harvester: twitch token file: %v", err)
			}
		}

		if twClientID != "" && twClientSecret != "" && twRefreshToken != "" {
			if tokenFilePath == "" {
				log.Fatal("harvester: twitch-token-file is required when refresh inputs provided")
			}

			refreshMgr = &twitch.RefreshManager{
				ClientID:     twClientID,
				ClientSecret: twClientSecret,
				RefreshToken: twRefreshToken,
				TokenFile:    tokenFilePath,
			}

			accessToken, _, err := refreshMgr.Refresh(ctx)
			if err != nil {
				log.Fatalf("harvester: twitch refresh: %v", err)
			}
			token = twitch.NormalizeToken(accessToken)
			if token == "" {
				log.Fatal("harvester: received empty twitch token after refresh")
			}
		}

		if token == "" {
			token = twitch.NormalizeToken(twToken)
		}

		if token == "" {
			log.Printf("harvester: twitch token not provided; skipping twitch receiver")
			if refreshMgr != nil {
				log.Printf("harvester: twitch refresh inputs ignored due to missing token")
			}
		} else {
			if loader != nil {
				loader.SetCached(token)
			}

			state := newTokenState(token)

			cfg := twitchirc.Config{
				Channel:       channel,
				Nick:          nick,
				Token:         token,
				UseTLS:        twTLS,
				TokenProvider: state.Current,
			}

			if refreshMgr != nil {
				cfg.RefreshNow = func(refreshCtx context.Context) (string, error) {
					accessToken, _, err := refreshMgr.Refresh(refreshCtx)
					if err != nil {
						return "", err
					}
					normalized := twitch.NormalizeToken(accessToken)
					if normalized == "" {
						return "", errors.New("twitch: refresh returned empty token")
					}
					state.Set(normalized)
					if loader != nil {
						loader.SetCached(normalized)
					}
					sendTokenUpdate(tokenUpdates, tokenUpdate{Token: normalized, Force: true, Reason: "refresh"})
					return normalized, nil
				}

				go refreshMgr.StartAuto(ctx, func(t string) {
					normalized := twitch.NormalizeToken(t)
					if normalized == "" {
						return
					}
					state.Set(normalized)
					if loader != nil {
						loader.SetCached(normalized)
					}
					sendTokenUpdate(tokenUpdates, tokenUpdate{Token: normalized, Force: true, Reason: "refresh"})
				})
			}

			reloader := &twitchReloader{updates: tokenUpdates, nick: nick}
			har.SetTwitchConn(reloader)

			if tokenFilePath != "" {
				watchPaths := []string{tokenFilePath}
				if twRefreshFile != "" {
					watchPaths = append(watchPaths, twRefreshFile)
				}
				if err := har.WatchTokenFiles(watchPaths...); err != nil {
					slog.Error("harvester: watch token files", "err", err)
				}
			}

			started++
			go runTwitchWithReload(ctx, cancel, cfg, handler, loader, state, tokenUpdates)
			log.Printf("harvester: twitch receiver started for #%s", channel)
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
	state *tokenState,
	updates <-chan tokenUpdate,
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
	if cfg.TokenProvider == nil && state != nil {
		cfg.TokenProvider = state.Current
	}
	if cfg.Token == "" && state != nil {
		cfg.Token = state.Current()
	}

	cancelCurrent, doneCurrent := startClient(cfg)

	var ticker *time.Ticker
	if loader != nil {
		ticker = time.NewTicker(10 * time.Second)
		defer ticker.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			cancelCurrent()
			<-doneCurrent
			return
		case <-func() <-chan time.Time {
			if ticker == nil {
				return nil
			}
			return ticker.C
		}():
			if loader == nil {
				continue
			}
			token, changed, err := loader.Load()
			if err != nil {
				if !errors.Is(err, twitch.ErrEmptyToken) {
					log.Printf("harvester: twitch token file: %v", err)
				}
				continue
			}
			if token == "" {
				continue
			}
			if !changed {
				continue
			}

			applyTokenUpdate(tokenUpdate{Token: token, Force: true, Reason: "file"}, loader, state, &cfg, &cancelCurrent, &doneCurrent, startClient)
		case upd := <-updates:
			if updates == nil {
				continue
			}
			applyTokenUpdate(upd, loader, state, &cfg, &cancelCurrent, &doneCurrent, startClient)
		}
	}
}

type twitchReloader struct {
	updates chan tokenUpdate
	nick    string
}

func (r *twitchReloader) Reconnect(access string) error {
	if r == nil {
		return errors.New("twitch reloader unavailable")
	}
	if r.updates == nil {
		return errors.New("twitch reload channel unavailable")
	}
	token := twitch.NormalizeToken(access)
	if token == "" {
		return errors.New("twitch: empty token")
	}
	sendTokenUpdate(r.updates, tokenUpdate{Token: token, Force: true, Reason: "manual"})
	return nil
}

func (r *twitchReloader) JoinedNick() string {
	if r == nil {
		return ""
	}
	return r.nick
}

type tokenUpdate struct {
	Token  string
	Force  bool
	Reason string
}

type tokenState struct {
	mu    sync.RWMutex
	token string
}

func newTokenState(initial string) *tokenState {
	return &tokenState{token: twitch.NormalizeToken(initial)}
}

func (s *tokenState) Current() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.token
}

func (s *tokenState) Set(token string) bool {
	normalized := twitch.NormalizeToken(token)
	if normalized == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token == normalized {
		return false
	}
	s.token = normalized
	return true
}

func sendTokenUpdate(ch chan tokenUpdate, upd tokenUpdate) {
	if ch == nil {
		return
	}
	select {
	case ch <- upd:
		return
	default:
	}

	select {
	case <-ch:
	default:
	}

	select {
	case ch <- upd:
	default:
	}
}

func applyTokenUpdate(
	upd tokenUpdate,
	loader *twitch.FileTokenLoader,
	state *tokenState,
	cfg *twitchirc.Config,
	cancelCurrent *context.CancelFunc,
	doneCurrent *<-chan struct{},
	start func(twitchirc.Config) (context.CancelFunc, <-chan struct{}),
) {
	if cfg == nil || cancelCurrent == nil || doneCurrent == nil {
		return
	}

	token := twitch.NormalizeToken(upd.Token)
	if token == "" {
		return
	}

	changed := false
	if state != nil {
		changed = state.Set(token) || changed
	}

	if loader != nil {
		loader.SetCached(token)
	}

	cfg.Token = token

	if !upd.Force && !changed {
		return
	}

	switch upd.Reason {
	case "file":
		log.Printf("twitch: token reload detected; reconnecting")
	case "refresh":
		log.Printf("twitch: refreshed token; reconnecting")
	case "manual":
		log.Printf("twitch: manual token reload requested; reconnecting")
	default:
		log.Printf("twitch: token update detected; reconnecting")
	}

	(*cancelCurrent)()
	<-*doneCurrent
	*cancelCurrent, *doneCurrent = start(*cfg)
}
