package harvester

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/you/gnasty-chat/internal/twitch"
	"github.com/you/gnasty-chat/internal/twitchauth"
)

type TwitchConn interface {
	Reconnect(access string) error
	JoinedNick() string
}

type Harvester struct {
	tokens twitchauth.TokenFiles

	mu sync.Mutex
	tw TwitchConn
}

func New(tokens twitchauth.TokenFiles, tw TwitchConn) *Harvester {
	return &Harvester{tokens: tokens, tw: tw}
}

func (h *Harvester) SetTwitchConn(tw TwitchConn) {
	h.mu.Lock()
	h.tw = tw
	h.mu.Unlock()
}

func (h *Harvester) ReloadTwitch() (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.tw == nil {
		return "", fmt.Errorf("twitch connection unavailable")
	}
	if strings.TrimSpace(h.tokens.AccessPath) == "" {
		return "", fmt.Errorf("access token file not configured")
	}
	access, err := h.tokens.ReadAccess()
	if err != nil {
		return "", fmt.Errorf("read access: %w", err)
	}
	token := twitch.NormalizeToken(access)
	if token == "" {
		return "", fmt.Errorf("access token empty")
	}
	if err := h.tw.Reconnect(token); err != nil {
		return "", fmt.Errorf("reconnect: %w", err)
	}
	login := h.tw.JoinedNick()
	slog.Info("twitchirc: reloaded token and rejoined", "as", login)
	return login, nil
}
