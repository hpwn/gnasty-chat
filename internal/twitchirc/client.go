package twitchirc

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/you/gnasty-chat/internal/core"
)

type Config struct {
	Channel       string
	Nick          string
	Token         string
	UseTLS        bool
	TokenProvider func() string
	RefreshNow    func(context.Context) (string, error)
	Addr          string
}

type Handler func(core.ChatMessage)

type Client struct {
	cfg    Config
	handle Handler
}

var errAuthFailed = errors.New("twitchirc: authentication failed")

func New(cfg Config, h Handler) *Client {
	return &Client{cfg: cfg, handle: h}
}

func (c *Client) Run(ctx context.Context) error {
	if strings.TrimSpace(c.cfg.Channel) == "" || strings.TrimSpace(c.cfg.Nick) == "" {
		return errors.New("twitchirc: channel and nick are required")
	}

	backoff := time.Second
	refreshBackoff := time.Second
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err := c.runOnce(ctx); err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return ctx.Err()
			}

			if errors.Is(err, errAuthFailed) {
				if c.cfg.RefreshNow == nil {
					log.Printf("twitchirc: authentication failed; retrying in %s", backoff)
					timer := time.NewTimer(backoff)
					select {
					case <-ctx.Done():
						timer.Stop()
						return ctx.Err()
					case <-timer.C:
					}
					if backoff < 60*time.Second {
						backoff *= 2
						if backoff > 60*time.Second {
							backoff = 60 * time.Second
						}
					}
					continue
				}

				log.Printf("twitchirc: authentication failed; refreshing token")
				for {
					if ctx.Err() != nil {
						return ctx.Err()
					}

					_, refreshErr := c.cfg.RefreshNow(ctx)
					if refreshErr == nil {
						refreshBackoff = time.Second
						backoff = time.Second
						break
					}

					if ctx.Err() != nil {
						return ctx.Err()
					}

					log.Printf("twitchirc: refresh failed: %v; retrying in %s", refreshErr, refreshBackoff)
					timer := time.NewTimer(refreshBackoff)
					select {
					case <-ctx.Done():
						timer.Stop()
						return ctx.Err()
					case <-timer.C:
					}

					if refreshBackoff < time.Minute {
						refreshBackoff *= 2
						if refreshBackoff > time.Minute {
							refreshBackoff = time.Minute
						}
					}
				}

				continue
			}

			log.Printf("twitchirc: disconnected: %v; reconnecting in %s", err, backoff)

			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}

			if backoff < 60*time.Second {
				backoff *= 2
				if backoff > 60*time.Second {
					backoff = 60 * time.Second
				}
			}
			continue
		}

		backoff = time.Second
		refreshBackoff = time.Second
	}
}

func (c *Client) runOnce(ctx context.Context) error {
	token := strings.TrimSpace(c.cfg.Token)
	if c.cfg.TokenProvider != nil {
		if provided := strings.TrimSpace(c.cfg.TokenProvider()); provided != "" {
			token = provided
		}
	}
	if token == "" {
		return errors.New("twitchirc: token is required")
	}

	host := "irc.chat.twitch.tv"
	addr := host + ":6667"
	if c.cfg.UseTLS {
		addr = host + ":6697"
	}
	if strings.TrimSpace(c.cfg.Addr) != "" {
		addr = strings.TrimSpace(c.cfg.Addr)
	}

	log.Printf("twitchirc: connecting to %s (tls=%v)", addr, c.cfg.UseTLS)

	d := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	if c.cfg.UseTLS {
		conn, err = tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: host})
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	// write one IRC line and flush
	send := func(s string) error {
		_, err := rw.WriteString(s + "\r\n")
		if err != nil {
			return err
		}
		return rw.Flush()
	}

	// ensure the per-connection closer goroutine exits when this runOnce returns
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close() // unblock reader
		case <-done:
			// this connection ended normally; nothing to do
		}
	}()

	if err := send("PASS " + token); err != nil {
		return fmt.Errorf("send PASS: %w", err)
	}
	if err := send("NICK " + c.cfg.Nick); err != nil {
		return fmt.Errorf("send NICK: %w", err)
	}
	if err := send("CAP REQ :twitch.tv/tags twitch.tv/commands twitch.tv/membership"); err != nil {
		return fmt.Errorf("send CAP REQ: %w", err)
	}
	if err := send("JOIN #" + c.cfg.Channel); err != nil {
		return fmt.Errorf("send JOIN: %w", err)
	}
	log.Printf("twitchirc: joined #%s as %s", c.cfg.Channel, c.cfg.Nick)

	reader := rw.Reader
	var (
		total        int
		window       int
		nextTick     = time.Now().Add(10 * time.Second)
		readDeadline = 2 * time.Minute
		nextPing     = time.Now().Add(4 * time.Minute)
	)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err := conn.SetReadDeadline(time.Now().Add(readDeadline)); err != nil {
			return fmt.Errorf("set deadline: %w", err)
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				now := time.Now()
				if now.After(nextPing) || now.Equal(nextPing) {
					if err := send("PING :keepalive"); err != nil {
						return fmt.Errorf("send PING: %w", err)
					}
					nextPing = now.Add(4 * time.Minute)
				}
				if now.After(nextTick) || now.Equal(nextTick) {
					log.Printf("twitchirc: recv %d msgs (total %d)", window, total)
					window = 0
					nextTick = now.Add(10 * time.Second)
				}
				continue
			}
			return fmt.Errorf("read: %w", err)
		}

		now := time.Now()
		if now.After(nextTick) || now.Equal(nextTick) {
			log.Printf("twitchirc: recv %d msgs (total %d)", window, total)
			window = 0
			nextTick = now.Add(10 * time.Second)
		}
		nextPing = now.Add(4 * time.Minute)

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}

		if authFailure(line) {
			log.Printf("twitchirc: authentication failed per server NOTICE")
			return errAuthFailed
		}

		if strings.HasPrefix(line, "PING ") {
			if err := send("PONG " + strings.TrimPrefix(line, "PING ")); err != nil {
				return fmt.Errorf("send PONG: %w", err)
			}
			nextPing = time.Now().Add(4 * time.Minute)
			continue
		}

		if strings.Contains(line, " RECONNECT") {
			return fmt.Errorf("server requested reconnect")
		}

		if msg, ok := parsePrivmsg(line, c.cfg.Channel); ok {
			total++
			window++
			if c.handle != nil {
				c.handle(msg)
			}
		}
	}
}

func parsePrivmsg(line, channel string) (core.ChatMessage, bool) {
	original := line
	rest := line
	tags := map[string]string{}

	if strings.HasPrefix(rest, "@") {
		idx := strings.Index(rest, " ")
		if idx == -1 {
			return core.ChatMessage{}, false
		}
		tagPart := rest[1:idx]
		rest = strings.TrimSpace(rest[idx+1:])
		for _, kv := range strings.Split(tagPart, ";") {
			if kv == "" {
				continue
			}
			parts := strings.SplitN(kv, "=", 2)
			key := parts[0]
			val := ""
			if len(parts) == 2 {
				val = unescapeIRC(parts[1])
			}
			tags[key] = val
		}
	}

	if !strings.HasPrefix(rest, ":") {
		return core.ChatMessage{}, false
	}
	rest = rest[1:]

	idx := strings.Index(rest, " ")
	if idx == -1 {
		return core.ChatMessage{}, false
	}
	prefix := rest[:idx]
	rest = strings.TrimSpace(rest[idx+1:])

	if !strings.HasPrefix(strings.ToUpper(rest), "PRIVMSG #") {
		return core.ChatMessage{}, false
	}
	rest = rest[len("PRIVMSG #"):]

	idx = strings.Index(rest, " ")
	if idx == -1 {
		return core.ChatMessage{}, false
	}
	chanName := rest[:idx]
	rest = strings.TrimSpace(rest[idx+1:])
	if !strings.EqualFold(chanName, channel) {
		return core.ChatMessage{}, false
	}

	if !strings.HasPrefix(rest, ":") {
		return core.ChatMessage{}, false
	}
	text := rest[1:]

	user := extractUser(prefix)
	if display := tags["display-name"]; display != "" {
		user = display
	}

	ts := time.Now().UTC()
	if tsStr := tags["tmi-sent-ts"]; tsStr != "" {
		if ms, err := strconv.ParseInt(tsStr, 10, 64); err == nil {
			ts = time.Unix(0, ms*int64(time.Millisecond)).UTC()
		}
	}

	id := tags["id"]
	if id == "" {
		id = fmt.Sprintf("%s-%d", user, ts.UnixNano())
	}

	badges := combineLists(tags["badges"], tags["badge-info"], ",")
	emotes := splitList(tags["emotes"], "/")

	rawMap := map[string]any{
		"tags":   tags,
		"prefix": prefix,
		"line":   original,
	}
	rawJSON, _ := json.Marshal(rawMap)

	return core.ChatMessage{
		ID:            id,
		PlatformMsgID: id,
		Ts:            ts,
		Username:      user,
		Platform:      "Twitch",
		Text:          text,
		EmotesJSON:    encodeList(emotes),
		RawJSON:       string(rawJSON),
		BadgesJSON:    encodeList(badges),
		Colour:        tags["color"],
	}, true
}

func authFailure(line string) bool {
	lower := strings.ToLower(line)
	if strings.Contains(lower, "login authentication failed") {
		return true
	}
	if strings.Contains(lower, "improperly formatted auth") {
		return true
	}
	if strings.Contains(lower, "authentication failed") {
		return true
	}
	return false
}

func extractUser(prefix string) string {
	if strings.HasPrefix(prefix, ":") {
		prefix = prefix[1:]
	}
	if idx := strings.Index(prefix, "!"); idx != -1 {
		return prefix[:idx]
	}
	return prefix
}

func unescapeIRC(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 's':
			b.WriteByte(' ')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case ':':
			b.WriteByte(';')
		case '\\':
			b.WriteByte('\\')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func splitList(s, sep string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func combineLists(a, b, sep string) []string {
	list := splitList(a, sep)
	list = append(list, splitList(b, sep)...)
	return list
}

func encodeList(items []string) string {
	if len(items) == 0 {
		return ""
	}
	b, err := json.Marshal(items)
	if err != nil {
		return ""
	}
	return string(b)
}
