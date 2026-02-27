package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/you/gnasty-chat/internal/config"
	"github.com/you/gnasty-chat/internal/core"
	"github.com/you/gnasty-chat/internal/harvester"
	httpadmin "github.com/you/gnasty-chat/internal/http"
	"github.com/you/gnasty-chat/internal/httpapi"
	"github.com/you/gnasty-chat/internal/ingesttrace"
	"github.com/you/gnasty-chat/internal/sink"
	"github.com/you/gnasty-chat/internal/twitch"
	"github.com/you/gnasty-chat/internal/twitchauth"
	"github.com/you/gnasty-chat/internal/twitchbadges"
	"github.com/you/gnasty-chat/internal/twitchirc"
	"github.com/you/gnasty-chat/internal/version"
	"github.com/you/gnasty-chat/internal/ytlive"
)

type noopWriter struct{}

func (noopWriter) Write(core.ChatMessage, *ingesttrace.MessageTrace) error {
	return errors.New("no sink configured")
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	var (
		versionFlag     bool
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

	flag.BoolVar(&versionFlag, "version", false, "Print build version and exit")
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

	if versionFlag {
		fmt.Printf(
			"harvester version: %s (commit %s, built %s)\n",
			version.Version,
			version.Commit,
			version.BuildTime,
		)
		os.Exit(0)
	}

	overrides := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) {
		overrides[f.Name] = true
	})

	cfg := config.Load()

	addSink := func(name string) {
		if !cfg.HasSink(name) {
			cfg.Sinks = append(cfg.Sinks, name)
		}
	}

	if overrides["sqlite"] {
		dbPath = strings.TrimSpace(dbPath)
		cfg.Sink.SQLite.Path = dbPath
		addSink("sqlite")
	}
	if overrides["twitch-channel"] {
		trimmed := strings.TrimSpace(twChannel)
		if trimmed != "" {
			cfg.Twitch.Channels = []string{trimmed}
			cfg.Twitch.Enabled = true
		} else {
			cfg.Twitch.Channels = nil
		}
	}
	if overrides["twitch-nick"] {
		cfg.Twitch.Nick = strings.TrimSpace(twNick)
	}
	if overrides["twitch-token"] {
		cfg.Twitch.Token = strings.TrimSpace(twToken)
	}
	if overrides["twitch-token-file"] {
		cfg.Twitch.TokenFile = strings.TrimSpace(twTokenFile)
	}
	if overrides["twitch-client-id"] {
		cfg.Twitch.ClientID = strings.TrimSpace(twClientID)
	}
	if overrides["twitch-client-secret"] {
		cfg.Twitch.ClientSecret = strings.TrimSpace(twClientSecret)
	}
	if overrides["twitch-refresh-token"] {
		cfg.Twitch.RefreshToken = strings.TrimSpace(twRefreshToken)
	}
	if overrides["twitch-refresh-token-file"] {
		cfg.Twitch.RefreshTokenFile = strings.TrimSpace(twRefreshFile)
	}
	if overrides["twitch-tls"] {
		cfg.Twitch.TLS = twTLS
	}
	if overrides["youtube-url"] {
		cfg.YouTube.LiveURL = strings.TrimSpace(ytURL)
		cfg.YouTube.Enabled = cfg.YouTube.LiveURL != ""
	}

	if len(cfg.Twitch.Channels) > 0 {
		cfg.Twitch.Enabled = true
	}
	runtimeSettings := harvester.NewRuntimeSettingsFromConfig(cfg)

	dbPath = cfg.Sink.SQLite.Path
	if len(cfg.Sinks) == 0 {
		log.Printf("harvester: no sinks configured; supported sinks: sqlite")
	}

	if runtimeSettings.Twitch.Channel != "" {
		twChannel = runtimeSettings.Twitch.Channel
		if len(cfg.Twitch.Channels) > 1 {
			log.Printf("harvester: twitch: multiple channels configured; using %s", twChannel)
		}
	} else {
		twChannel = ""
	}
	twNick = runtimeSettings.Twitch.Nick
	twToken = cfg.Twitch.Token
	twTokenFile = cfg.Twitch.TokenFile
	twClientID = cfg.Twitch.ClientID
	twClientSecret = cfg.Twitch.ClientSecret
	twRefreshToken = cfg.Twitch.RefreshToken
	twRefreshFile = cfg.Twitch.RefreshTokenFile
	if strings.TrimSpace(twRefreshFile) != "" {
		data, err := os.ReadFile(twRefreshFile)
		if err != nil {
			log.Printf("harvester: twitch refresh token file: %v", err)
		} else {
			twRefreshToken = strings.TrimSpace(string(data))
		}
	}
	twTLS = runtimeSettings.Twitch.TLS
	ytURL = runtimeSettings.YouTube.URL
	log.Printf(
		"harvester: youtube settings url=%s dump_unhandled=%t poll_timeout_secs=%d poll_interval_ms=%d",
		ytURL,
		runtimeSettings.YouTube.DumpUnhandled,
		runtimeSettings.YouTube.PollTimeoutSecs,
		runtimeSettings.YouTube.PollIntervalMS,
	)

	log.Printf("%s", cfg.SummaryJSON())

	tokenFiles := twitchauth.TokenFiles{
		AccessPath:   twTokenFile,
		RefreshPath:  twRefreshFile,
		ClientID:     twClientID,
		ClientSecret: twClientSecret,
	}
	var refreshMgr *twitch.RefreshManager
	if strings.TrimSpace(twChannel) != "" &&
		strings.TrimSpace(twClientID) != "" &&
		strings.TrimSpace(twClientSecret) != "" &&
		strings.TrimSpace(twRefreshToken) != "" {
		if strings.TrimSpace(twTokenFile) == "" {
			log.Fatal("harvester: twitch-token-file is required when refresh inputs provided")
		}
		refreshMgr = &twitch.RefreshManager{
			ClientID:     twClientID,
			ClientSecret: twClientSecret,
			RefreshToken: twRefreshToken,
			TokenFile:    twTokenFile,
		}
	}
	var refreshUpdater func(string)
	if refreshMgr != nil {
		refreshUpdater = refreshMgr.SetRefreshToken
	}
	har := harvester.New(tokenFiles, nil, refreshUpdater, runtimeSettings)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("harvester: received %s, shutting down", sig)
		cancel()
	}()

	var (
		sinkDB   *sink.SQLiteSink
		api      *httpapi.Server
		writer   sink.Writer = noopWriter{}
		alerts   sink.AlertWriter
		buffered *sink.BufferedWriter
	)

	if cfg.HasSink("sqlite") {
		db, err := sink.OpenSQLite(dbPath)
		if err != nil {
			log.Fatalf("harvester: open sqlite: %v", err)
		}
		sinkDB = db
		if err := sinkDB.Ping(); err != nil {
			log.Fatalf("harvester: ping sqlite: %v", err)
		}
		if err := migrateSQLite(ctx, sinkDB.RawDB()); err != nil {
			log.Fatalf("harvester: sqlite migrate: %v", err)
		}
		writer = sinkDB
		alerts = sinkDB
	} else {
		log.Printf("harvester: sqlite sink disabled (configured sinks=%v)", cfg.Sinks)
	}

	if sinkDB != nil {
		defer func() {
			if err := sinkDB.Close(); err != nil {
				log.Printf("harvester: closing sink: %v", err)
			}
		}()
	}

	var corsOrigins []string
	if strings.TrimSpace(httpCorsOrigins) != "" {
		for _, origin := range strings.Split(httpCorsOrigins, ",") {
			origin = strings.TrimSpace(origin)
			if origin != "" {
				corsOrigins = append(corsOrigins, origin)
			}
		}
	}

	build := httpapi.BuildInfo{Version: version.Version, Revision: version.Commit}
	if version.BuildTime != "" && version.BuildTime != "unknown" {
		if t, err := time.Parse(time.RFC3339, version.BuildTime); err == nil {
			build.BuiltAt = t
		}
	}

	if httpAddr != "" {
		if sinkDB == nil {
			log.Printf("harvester: http api requested but sqlite sink is disabled; skipping listener")
		} else {
			api = httpapi.New(sinkDB, httpapi.Options{
				Addr:            httpAddr,
				CORSOrigins:     corsOrigins,
				RateLimitRPS:    httpRateRPS,
				RateLimitBurst:  httpRateBurst,
				EnableMetrics:   httpMetrics,
				EnableAccessLog: httpAccessLog,
				EnablePprof:     httpPprof,
				Build:           build,
				ConfigSnapshot:  cfg.Redacted(),
				ConfigSnapshotFunc: func() map[string]any {
					snapshot := cfg.Redacted()
					runtime := har.RuntimeSettings()
					snapshot["sinks"] = append([]string(nil), runtime.Sinks.Enabled...)
					if sinkCfg, ok := snapshot["sink"].(map[string]any); ok {
						sinkCfg["batch_size"] = runtime.Sinks.BatchSize
						sinkCfg["flush_ms"] = runtime.Sinks.FlushMaxMS
					}
					if twCfg, ok := snapshot["twitch"].(map[string]any); ok {
						if runtime.Twitch.Channel == "" {
							twCfg["channels"] = []string{}
						} else {
							twCfg["channels"] = []string{runtime.Twitch.Channel}
						}
						twCfg["nick"] = runtime.Twitch.Nick
						twCfg["tls"] = runtime.Twitch.TLS
						twCfg["debug_drops"] = runtime.Twitch.DebugDrops
						twCfg["backoff_min_ms"] = runtime.Twitch.BackoffMinMS
						twCfg["backoff_max_ms"] = runtime.Twitch.BackoffMaxMS
						twCfg["refresh_backoff_min_ms"] = runtime.Twitch.RefreshBackoffMinMS
						twCfg["refresh_backoff_max_ms"] = runtime.Twitch.RefreshBackoffMaxMS
					}
					if ytCfg, ok := snapshot["youtube"].(map[string]any); ok {
						ytCfg["live_url"] = runtime.YouTube.URL
						ytCfg["enabled"] = strings.TrimSpace(runtime.YouTube.URL) != ""
						ytCfg["retry_seconds"] = runtime.YouTube.RetrySeconds
						ytCfg["dump_unhandled"] = runtime.YouTube.DumpUnhandled
						ytCfg["poll_timeout_secs"] = runtime.YouTube.PollTimeoutSecs
						ytCfg["poll_interval_ms"] = runtime.YouTube.PollIntervalMS
						ytCfg["debug"] = runtime.YouTube.Debug
					}
					return snapshot
				},
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
			if aw, ok := writer.(sink.AlertWriter); ok {
				alerts = aw
			}
			log.Printf("harvester: http api ready on %s", httpAddr)
		}
	}

	if sinkDB != nil {
		buffered = sink.NewBufferedWriter(writer, sink.BufferedOptions{
			BatchSize:     runtimeSettings.Sinks.BatchSize,
			FlushInterval: time.Duration(runtimeSettings.Sinks.FlushMaxMS) * time.Millisecond,
		})
		writer = buffered
		alerts = buffered
		har.SetSinkRuntimeApplier(func(settings harvester.SinkRuntimeSettings) (bool, error) {
			changed := buffered.UpdateOptions(sink.BufferedOptions{
				BatchSize:     settings.BatchSize,
				FlushInterval: time.Duration(settings.FlushMaxMS) * time.Millisecond,
			})
			return changed, nil
		})
	}

	if buffered != nil {
		defer func() {
			if err := buffered.Close(); err != nil {
				log.Printf("harvester: flush buffered sink: %v", err)
			}
		}()
	}

	started := 0

	channel := strings.TrimSpace(twChannel)
	if channel != "" {
		nick := strings.TrimSpace(twNick)
		if nick == "" {
			log.Fatal("harvester: twitch-nick is required when twitch-channel/token provided")
		}

		handler := func(msg core.ChatMessage, trace *ingesttrace.MessageTrace) {
			if trace != nil {
				trace.IncCounter(ingesttrace.StageNormalizedOK)
				trace.LogTrace(slog.Default(), "normalized_ok")
			}

			if err := writer.Write(msg, trace); err != nil {
				log.Printf("harvester: write twitch message: %v", err)
				if api != nil {
					api.ReportDBWriteError()
				}
			}
		}
		alertHandler := func(alert core.AlertEvent) {
			if alerts == nil {
				return
			}
			if err := alerts.WriteAlert(alert); err != nil {
				log.Printf("harvester: write twitch alert: %v", err)
				if api != nil {
					api.ReportDBWriteError()
				}
			}
		}

		tokenFilePath := twTokenFile

		var (
			token  string
			loader *twitch.FileTokenLoader
		)
		twitchUpdates := make(chan twitchUpdate, 4)

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

		if refreshMgr != nil {
			if tokenFilePath == "" {
				log.Fatal("harvester: twitch-token-file is required when refresh inputs provided")
			}
			refreshMgr.TokenFile = tokenFilePath
			refreshMgr.SetRefreshToken(twRefreshToken)

			refreshFilePath := strings.TrimSpace(twRefreshFile)
			if refreshFilePath == "" {
				accessToken, _, err := refreshMgr.Refresh(ctx)
				if err != nil {
					log.Fatalf("harvester: twitch refresh: %v", err)
				}
				token = twitch.NormalizeToken(accessToken)
				if token == "" {
					log.Fatal("harvester: received empty twitch token after refresh")
				}
			} else {
				if err := twitch.Refresh(twClientID, twClientSecret, refreshFilePath, tokenFilePath); err != nil {
					log.Fatalf("harvester: twitch refresh: %v", err)
				}

				if loader != nil {
					loaded, _, err := loader.Load()
					if err != nil {
						log.Fatalf("harvester: twitch refresh load token: %v", err)
					}
					token = loaded
				} else {
					data, err := os.ReadFile(tokenFilePath)
					if err != nil {
						log.Fatalf("harvester: twitch refresh read token: %v", err)
					}
					token = twitch.NormalizeToken(string(data))
				}

				refreshData, err := os.ReadFile(refreshFilePath)
				if err != nil {
					log.Fatalf("harvester: twitch refresh read refresh token: %v", err)
				}
				trimmedRefresh := strings.TrimSpace(string(refreshData))
				if trimmedRefresh == "" {
					log.Fatal("harvester: received empty twitch refresh token after refresh")
				}
				refreshMgr.SetRefreshToken(trimmedRefresh)
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

			var badgeResolver twitchirc.BadgeResolver
			if twClientID != "" && twClientSecret != "" {
				badgeResolver = twitchbadges.NewResolver(twClientID, twClientSecret)
				log.Printf("harvester: twitch badge resolver enabled")
			}

			cfg := twitchirc.Config{
				Channel:       channel,
				Nick:          nick,
				Token:         token,
				UseTLS:        twTLS,
				TokenProvider: state.Current,
				Badges:        badgeResolver,
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
					sendTwitchUpdate(twitchUpdates, twitchUpdate{Token: normalized, Force: true, Reason: "refresh"})
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
					sendTwitchUpdate(twitchUpdates, twitchUpdate{Token: normalized, Force: true, Reason: "refresh"})
				})
			}

			reloader := &twitchReloader{
				updates:             twitchUpdates,
				nick:                nick,
				channel:             channel,
				useTLS:              twTLS,
				debugDrops:          runtimeSettings.Twitch.DebugDrops,
				backoffMinMS:        runtimeSettings.Twitch.BackoffMinMS,
				backoffMaxMS:        runtimeSettings.Twitch.BackoffMaxMS,
				refreshBackoffMinMS: runtimeSettings.Twitch.RefreshBackoffMinMS,
				refreshBackoffMaxMS: runtimeSettings.Twitch.RefreshBackoffMaxMS,
			}
			cfg.Runtime = reloader.RuntimeOptions
			har.SetTwitchConn(reloader)
			har.SetTwitchRuntimeApplier(reloader.ApplyRuntime)

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
			go runTwitchWithReload(ctx, cancel, cfg, handler, alertHandler, loader, state, twitchUpdates)
			log.Printf("harvester: twitch receiver started for #%s", channel)
		}
	}

	{
		handler := func(msg core.ChatMessage) {
			if err := writer.Write(msg, nil); err != nil {
				log.Printf("harvester: write youtube message: %v", err)
				if api != nil {
					api.ReportDBWriteError()
				}
			}
		}
		alertHandler := func(alert core.AlertEvent) {
			if alerts == nil {
				return
			}
			if err := alerts.WriteAlert(alert); err != nil {
				log.Printf("harvester: write youtube alert: %v", err)
				if api != nil {
					api.ReportDBWriteError()
				}
			}
		}

		resolver := ytlive.NewResolver(nil)
		ytSwitcher := newYouTubeSwitcher(runtimeSettings.YouTube)
		har.SetYouTubeRuntimeApplier(ytSwitcher.ApplyRuntime)
		if strings.TrimSpace(runtimeSettings.YouTube.URL) != "" {
			started++
		}
		go func() {
			var (
				currentCancel context.CancelFunc
				currentDone   <-chan struct{}
				currentWatch  string
				target        = runtimeSettings.YouTube
			)

			retryDelay := time.Duration(target.RetrySeconds) * time.Second
			if retryDelay <= 0 {
				retryDelay = 30 * time.Second
			}

			stopPoller := func() {
				if currentCancel == nil {
					return
				}
				currentCancel()
				if currentDone != nil {
					<-currentDone
				}
				currentCancel = nil
				currentDone = nil
				currentWatch = ""
			}
			defer stopPoller()

			startPoller := func(watchURL string) {
				stopPoller()
				pollCtx, pollCancel := context.WithCancel(ctx)
				done := make(chan struct{})
				client := ytlive.New(ytlive.Config{
					LiveURL:         watchURL,
					DumpUnhandled:   target.DumpUnhandled,
					PollTimeoutSecs: target.PollTimeoutSecs,
					PollIntervalMS:  target.PollIntervalMS,
					Debug:           target.Debug,
				}, handler, alertHandler)
				go func() {
					defer close(done)
					if err := client.Run(pollCtx); err != nil && !errors.Is(err, context.Canceled) {
						log.Printf("harvester: youtube client exited: %v", err)
						cancel()
					}
				}()
				currentCancel = pollCancel
				currentDone = done
				currentWatch = watchURL
			}

			for {
				if ctx.Err() != nil {
					return
				}

				if strings.TrimSpace(target.URL) == "" {
					stopPoller()
					timer := time.NewTimer(time.Second)
					select {
					case <-ctx.Done():
						timer.Stop()
						return
					case next := <-ytSwitcher.Updates():
						timer.Stop()
						if ytSwitcher.Apply(next) {
							target = next
							retryDelay = time.Duration(target.RetrySeconds) * time.Second
							if retryDelay <= 0 {
								retryDelay = 30 * time.Second
							}
						}
						continue
					case <-timer.C:
						continue
					}
				}

				resolveCtx, resolveCancel := context.WithCancel(ctx)
				resultCh := make(chan struct {
					result ytlive.ResolveResult
					err    error
				}, 1)
				go func(url string) {
					res, err := resolver.Resolve(resolveCtx, url)
					resultCh <- struct {
						result ytlive.ResolveResult
						err    error
					}{result: res, err: err}
				}(target.URL)

				var (
					res ytlive.ResolveResult
					err error
				)
				select {
				case <-ctx.Done():
					resolveCancel()
					return
				case next := <-ytSwitcher.Updates():
					resolveCancel()
					if ytSwitcher.Apply(next) {
						oldURL := target.URL
						target = next
						retryDelay = time.Duration(target.RetrySeconds) * time.Second
						if retryDelay <= 0 {
							retryDelay = 30 * time.Second
						}
						if oldURL != target.URL {
							stopPoller()
							log.Printf("youtube: switch url %q -> %q", oldURL, target.URL)
						} else if currentWatch != "" {
							startPoller(currentWatch)
							log.Printf("youtube: polling settings updated at runtime")
						}
					}
					continue
				case resolved := <-resultCh:
					resolveCancel()
					res = resolved.result
					err = resolved.err
				}

				if err != nil {
					log.Printf("ytlive: resolve error: %v", err)
				} else {
					log.Printf("ytlive: resolved watch=%s chat=%s live=%t", res.WatchURL, res.ChatURL, res.Live)
					if !res.Live {
						stopPoller()
						log.Printf("ytlive: channel %s not live, backing off %s", target.URL, retryDelay)
					} else if res.WatchURL != "" {
						if currentWatch != res.WatchURL {
							if currentWatch == "" {
								log.Printf("ytlive: live stream changed to %s", res.WatchURL)
							} else {
								log.Printf("ytlive: live stream changed from %s to %s", currentWatch, res.WatchURL)
							}
							startPoller(res.WatchURL)
						} else if currentCancel == nil {
							startPoller(res.WatchURL)
						}
					} else {
						log.Printf("ytlive: resolved live stream without watch url, backing off %s", retryDelay)
					}
				}

				timer := time.NewTimer(retryDelay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case next := <-ytSwitcher.Updates():
					timer.Stop()
					if ytSwitcher.Apply(next) {
						oldURL := target.URL
						target = next
						retryDelay = time.Duration(target.RetrySeconds) * time.Second
						if retryDelay <= 0 {
							retryDelay = 30 * time.Second
						}
						if oldURL != target.URL {
							stopPoller()
							log.Printf("youtube: switch url %q -> %q", oldURL, target.URL)
						} else if currentWatch != "" {
							startPoller(currentWatch)
							log.Printf("youtube: polling settings updated at runtime")
						}
					}
					continue
				case <-timer.C:
				}
			}
		}()
		log.Printf("harvester: youtube resolver supervisor started (url=%s)", ytURL)
	}

	if started == 0 {
		log.Printf("harvester: ERROR: No receivers configured. Set GNASTY_SINKS=sqlite and GNASTY_SINK_SQLITE_PATH=/data/elora.db (shared with elora-chat).")
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
	handler twitchirc.Handler,
	alertHandler twitchirc.AlertHandler,
	loader *twitch.FileTokenLoader,
	state *tokenState,
	updates <-chan twitchUpdate,
) {
	startClient := func(cfg twitchirc.Config) (context.CancelFunc, <-chan struct{}) {
		runCtx, runCancel := context.WithCancel(ctx)
		done := make(chan struct{})
		client := twitchirc.New(cfg, handler, alertHandler)
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

			applyTwitchUpdate(twitchUpdate{Token: token, Force: true, Reason: "file"}, loader, state, &cfg, &cancelCurrent, &doneCurrent, startClient)
		case upd := <-updates:
			if updates == nil {
				continue
			}
			applyTwitchUpdate(upd, loader, state, &cfg, &cancelCurrent, &doneCurrent, startClient)
		}
	}
}

type twitchReloader struct {
	mu                  sync.RWMutex
	updates             chan twitchUpdate
	nick                string
	channel             string
	useTLS              bool
	debugDrops          bool
	backoffMinMS        int
	backoffMaxMS        int
	refreshBackoffMinMS int
	refreshBackoffMaxMS int
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
	sendTwitchUpdate(r.updates, twitchUpdate{Token: token, Force: true, Reason: "manual"})
	return nil
}

func (r *twitchReloader) ApplyRuntime(settings harvester.TwitchRuntimeSettings) (bool, error) {
	if r == nil {
		return false, errors.New("twitch reloader unavailable")
	}
	if r.updates == nil {
		return false, errors.New("twitch reload channel unavailable")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	changed := false
	upd := twitchUpdate{Force: true, Reason: "runtime_config"}

	if r.channel != settings.Channel {
		old := r.channel
		r.channel = settings.Channel
		upd.Channel = settings.Channel
		changed = true
		log.Printf("twitch: switch channel %q -> %q", old, settings.Channel)
	}
	if settings.Nick != "" && r.nick != settings.Nick {
		r.nick = settings.Nick
		upd.Nick = settings.Nick
		changed = true
	}
	if r.useTLS != settings.TLS {
		r.useTLS = settings.TLS
		upd.UseTLS = &settings.TLS
		changed = true
	}
	if r.debugDrops != settings.DebugDrops {
		r.debugDrops = settings.DebugDrops
		upd.DebugDrops = &settings.DebugDrops
		changed = true
	}
	if r.backoffMinMS != settings.BackoffMinMS {
		r.backoffMinMS = settings.BackoffMinMS
		changed = true
	}
	if r.backoffMaxMS != settings.BackoffMaxMS {
		r.backoffMaxMS = settings.BackoffMaxMS
		changed = true
	}
	if r.refreshBackoffMinMS != settings.RefreshBackoffMinMS {
		r.refreshBackoffMinMS = settings.RefreshBackoffMinMS
		changed = true
	}
	if r.refreshBackoffMaxMS != settings.RefreshBackoffMaxMS {
		r.refreshBackoffMaxMS = settings.RefreshBackoffMaxMS
		changed = true
	}
	if changed && (upd.Channel != "" || upd.Nick != "" || upd.UseTLS != nil || upd.DebugDrops != nil) {
		sendTwitchUpdate(r.updates, upd)
	}
	return changed, nil
}

func (r *twitchReloader) RuntimeOptions() twitchirc.RuntimeOptions {
	if r == nil {
		return twitchirc.RuntimeOptions{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return twitchirc.RuntimeOptions{
		DebugDrops:        r.debugDrops,
		BackoffMin:        time.Duration(r.backoffMinMS) * time.Millisecond,
		BackoffMax:        time.Duration(r.backoffMaxMS) * time.Millisecond,
		RefreshBackoffMin: time.Duration(r.refreshBackoffMinMS) * time.Millisecond,
		RefreshBackoffMax: time.Duration(r.refreshBackoffMaxMS) * time.Millisecond,
	}
}

func (r *twitchReloader) JoinedNick() string {
	if r == nil {
		return ""
	}
	return r.nick
}

type twitchUpdate struct {
	Token      string
	Channel    string
	Nick       string
	UseTLS     *bool
	DebugDrops *bool
	Force      bool
	Reason     string
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

func sendTwitchUpdate(ch chan twitchUpdate, upd twitchUpdate) {
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

func applyTwitchUpdate(
	upd twitchUpdate,
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

	restart := false
	tokenReason := false
	channelReason := false
	identityReason := false

	if strings.TrimSpace(upd.Token) != "" {
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
		if upd.Force || changed {
			restart = true
			tokenReason = true
		}
	}

	if strings.TrimSpace(upd.Channel) != "" {
		channel := strings.ToLower(strings.TrimSpace(upd.Channel))
		if channel != "" && channel != cfg.Channel {
			cfg.Channel = channel
			restart = true
			channelReason = true
		}
	}
	if strings.TrimSpace(upd.Nick) != "" {
		nick := strings.TrimSpace(upd.Nick)
		if nick != cfg.Nick {
			cfg.Nick = nick
			restart = true
			identityReason = true
		}
	}
	if upd.UseTLS != nil && cfg.UseTLS != *upd.UseTLS {
		cfg.UseTLS = *upd.UseTLS
		restart = true
		identityReason = true
	}
	if upd.DebugDrops != nil {
		// drop-logging verbosity is bound at IRC connection start; reconnect to apply immediately
		restart = true
		identityReason = true
	}

	if !restart {
		return
	}

	if channelReason {
		log.Printf("twitch: channel switch applied; reconnecting")
	} else if identityReason {
		log.Printf("twitch: runtime settings applied; reconnecting")
	} else if tokenReason {
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
	}

	(*cancelCurrent)()
	<-*doneCurrent
	*cancelCurrent, *doneCurrent = start(*cfg)
}

type youtubeSwitcher struct {
	mu      sync.Mutex
	current harvester.YouTubeRuntimeSettings
	applied harvester.YouTubeRuntimeSettings
	updates chan harvester.YouTubeRuntimeSettings
}

func newYouTubeSwitcher(initial harvester.YouTubeRuntimeSettings) *youtubeSwitcher {
	return &youtubeSwitcher{
		current: initial,
		applied: initial,
		updates: make(chan harvester.YouTubeRuntimeSettings, 4),
	}
}

func (s *youtubeSwitcher) ApplyRuntime(next harvester.YouTubeRuntimeSettings) (bool, error) {
	if s == nil {
		return false, errors.New("youtube switcher unavailable")
	}

	s.mu.Lock()
	if s.current == next {
		s.mu.Unlock()
		return false, nil
	}
	s.current = next
	s.mu.Unlock()

	sendYouTubeUpdate(s.updates, next)
	return true, nil
}

func (s *youtubeSwitcher) Updates() <-chan harvester.YouTubeRuntimeSettings {
	if s == nil {
		return nil
	}
	return s.updates
}

func (s *youtubeSwitcher) Apply(next harvester.YouTubeRuntimeSettings) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.applied == next {
		return false
	}
	s.applied = next
	return true
}

func sendYouTubeUpdate(ch chan harvester.YouTubeRuntimeSettings, value harvester.YouTubeRuntimeSettings) {
	if ch == nil {
		return
	}
	select {
	case ch <- value:
		return
	default:
	}

	select {
	case <-ch:
	default:
	}

	select {
	case ch <- value:
	default:
	}
}
