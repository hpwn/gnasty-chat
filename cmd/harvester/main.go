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
	"github.com/you/gnasty-chat/internal/twitchirc"
	"github.com/you/gnasty-chat/internal/ytlive"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	var (
		dbPath    string
		twChannel string
		twNick    string
		twToken   string
		twTLS     bool
		ytURL     string
		httpAddr  string
	)

	flag.StringVar(&dbPath, "sqlite", "chat.db", "Path to SQLite database file")
	flag.StringVar(&twChannel, "twitch-channel", "", "Twitch channel to join (without #)")
	flag.StringVar(&twNick, "twitch-nick", "", "Twitch nickname to login as")
	flag.StringVar(&twToken, "twitch-token", "", "Twitch OAuth token (format: oauth:xxxxx)")
	flag.BoolVar(&twTLS, "twitch-tls", true, "Use TLS (port 6697) for Twitch IRC connection")
	flag.StringVar(&ytURL, "youtube-url", "", "YouTube live/watch URL")
	flag.StringVar(&httpAddr, "http-addr", "", "HTTP status/stream address (e.g., :8765)")
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

	if httpAddr != "" {
		api = httpapi.New(sinkDB, httpapi.Options{Addr: httpAddr})
		go func() {
			if err := api.Start(); err != nil {
				log.Fatalf("harvester: http api: %v", err)
			}
		}()
		writer = sink.WithAPI(sinkDB, api)
		log.Printf("harvester: http api ready on %s", httpAddr)
	}

	started := 0

	if twChannel != "" && twToken != "" {
		if strings.TrimSpace(twNick) == "" {
			log.Fatal("harvester: twitch-nick is required when twitch-channel/token provided")
		}
		handler := func(msg core.ChatMessage) {
			if err := writer.Write(msg); err != nil {
				log.Printf("harvester: write twitch message: %v", err)
			}
		}
		client := twitchirc.New(twitchirc.Config{
			Channel: twChannel,
			Nick:    twNick,
			Token:   twToken,
			UseTLS:  twTLS,
		}, handler)
		started++
		go func() {
			if err := client.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("harvester: twitch client exited: %v", err)
				cancel()
			}
		}()
		log.Printf("harvester: twitch receiver started for #%s", twChannel)
	}

	if ytURL != "" {
		handler := func(msg core.ChatMessage) {
			if err := writer.Write(msg); err != nil {
				log.Printf("harvester: write youtube message: %v", err)
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
