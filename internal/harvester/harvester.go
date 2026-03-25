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

	mu                sync.Mutex
	tw                TwitchConn
	refreshUpdate     func(string)
	switchTwChannel   func(string) (bool, error)
	switchYouTubeLive func(string) (bool, error)
	applyTwRuntime    func(TwitchRuntimeSettings) (bool, error)
	applyYTRuntime    func(YouTubeRuntimeSettings) (bool, error)
	applySinkRuntime  func(SinkRuntimeSettings) (bool, error)

	runtimeMu sync.RWMutex
	runtime   RuntimeSettings
}

func New(tokens twitchauth.TokenFiles, tw TwitchConn, refreshUpdate func(string), runtime RuntimeSettings) *Harvester {
	return &Harvester{
		tokens:        tokens,
		tw:            tw,
		refreshUpdate: refreshUpdate,
		runtime:       runtime,
	}
}

func (h *Harvester) SetTwitchConn(tw TwitchConn) {
	h.mu.Lock()
	h.tw = tw
	h.mu.Unlock()
}

func (h *Harvester) SetRefreshUpdater(update func(string)) {
	h.mu.Lock()
	h.refreshUpdate = update
	h.mu.Unlock()
}

func (h *Harvester) SetTwitchChannelSwitcher(switcher func(string) (bool, error)) {
	h.mu.Lock()
	h.switchTwChannel = switcher
	h.mu.Unlock()
}

func (h *Harvester) SetYouTubeURLSwitcher(switcher func(string) (bool, error)) {
	h.mu.Lock()
	h.switchYouTubeLive = switcher
	h.mu.Unlock()
}

func (h *Harvester) SetTwitchRuntimeApplier(apply func(TwitchRuntimeSettings) (bool, error)) {
	h.mu.Lock()
	h.applyTwRuntime = apply
	h.mu.Unlock()
}

func (h *Harvester) SetYouTubeRuntimeApplier(apply func(YouTubeRuntimeSettings) (bool, error)) {
	h.mu.Lock()
	h.applyYTRuntime = apply
	h.mu.Unlock()
}

func (h *Harvester) SetSinkRuntimeApplier(apply func(SinkRuntimeSettings) (bool, error)) {
	h.mu.Lock()
	h.applySinkRuntime = apply
	h.mu.Unlock()
}

func (h *Harvester) RuntimeSettings() RuntimeSettings {
	h.runtimeMu.RLock()
	defer h.runtimeMu.RUnlock()
	return h.runtime
}

func (h *Harvester) ApplyRuntimeConfig(patch RuntimeConfigPatch) (RuntimeApplyResult, RuntimeSettings, error) {
	h.runtimeMu.RLock()
	current := h.runtime
	h.runtimeMu.RUnlock()
	next, result, err := current.ApplyConfig(patch)
	if err != nil {
		return RuntimeApplyResult{}, current, err
	}

	h.mu.Lock()
	applyTw := h.applyTwRuntime
	applyYT := h.applyYTRuntime
	applySink := h.applySinkRuntime
	h.mu.Unlock()

	if result.Sinks && applySink != nil {
		applied, err := applySink(next.Sinks)
		if err != nil {
			return RuntimeApplyResult{}, current, err
		}
		result.Sinks = applied
	}
	if result.Twitch && applyTw != nil {
		applied, err := applyTw(next.Twitch)
		if err != nil {
			return RuntimeApplyResult{}, current, err
		}
		result.Twitch = applied
	}
	if result.YouTube && applyYT != nil {
		applied, err := applyYT(next.YouTube)
		if err != nil {
			return RuntimeApplyResult{}, current, err
		}
		result.YouTube = applied
	}
	result.Changed = result.Sinks || result.Twitch || result.Kick || result.YouTube

	h.runtimeMu.Lock()
	h.runtime = next
	h.runtimeMu.Unlock()

	return result, next, nil
}

func (h *Harvester) SwitchTwitchChannel(channel string) (bool, error) {
	res, _, err := h.ApplyRuntimeConfig(RuntimeConfigPatch{
		Twitch: TwitchRuntimePatch{Channel: &channel},
	})
	return res.Twitch, err
}

func (h *Harvester) SwitchYouTubeURL(url string) (bool, error) {
	res, _, err := h.ApplyRuntimeConfig(RuntimeConfigPatch{
		YouTube: YouTubeRuntimePatch{URL: &url},
	})
	return res.YouTube, err
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
	if strings.TrimSpace(h.tokens.RefreshPath) != "" {
		refresh, err := h.tokens.ReadRefresh()
		if err != nil {
			return "", fmt.Errorf("read refresh: %w", err)
		}
		if refresh != "" && h.refreshUpdate != nil {
			h.refreshUpdate(refresh)
		}
	}
	if err := h.tw.Reconnect(token); err != nil {
		return "", fmt.Errorf("reconnect: %w", err)
	}
	login := h.tw.JoinedNick()
	slog.Info("twitchirc: reloaded token and rejoined", "as", login)
	return login, nil
}
