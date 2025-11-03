package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var tokenEndpoint = "https://id.twitch.tv/oauth2/token"

const defaultRefreshTimeout = 15 * time.Second

type RefreshManager struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
	TokenFile    string
	HTTP         *http.Client

	refreshMu   sync.RWMutex
	mu          sync.Mutex
	lastExpires time.Duration
}

type refreshResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	Status      int    `json:"status"`
	Message     string `json:"message"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

func (m *RefreshManager) SetRefreshToken(token string) {
	if m == nil {
		return
	}
	trimmed := strings.TrimSpace(token)
	m.refreshMu.Lock()
	m.RefreshToken = trimmed
	m.refreshMu.Unlock()
}

func (m *RefreshManager) Refresh(ctx context.Context) (string, time.Duration, error) {
	reqCtx := ctx
	cancel := func() {}
	if ctx != nil {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			reqCtx, cancel = context.WithTimeout(ctx, defaultRefreshTimeout)
		}
	} else {
		reqCtx, cancel = context.WithTimeout(context.Background(), defaultRefreshTimeout)
	}
	defer cancel()

	clientID := strings.TrimSpace(m.ClientID)
	clientSecret := strings.TrimSpace(m.ClientSecret)

	m.refreshMu.RLock()
	refreshTokenRaw := m.RefreshToken
	tokenFileRaw := m.TokenFile
	m.refreshMu.RUnlock()

	refreshToken := strings.TrimSpace(refreshTokenRaw)
	tokenFile := strings.TrimSpace(tokenFileRaw)

	if clientID == "" || clientSecret == "" || refreshToken == "" {
		return "", 0, errors.New("twitch: refresh requires client credentials and refresh token")
	}
	if tokenFile == "" {
		return "", 0, errors.New("twitch: token file is required for refresh")
	}

	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("twitch: create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := m.HTTP
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("twitch: refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", 0, fmt.Errorf("twitch: read refresh response: %w", err)
	}

	var parsed refreshResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", 0, fmt.Errorf("twitch: decode refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(parsed.Message)
		if msg == "" {
			msg = strings.TrimSpace(parsed.ErrorDesc)
		}
		if msg == "" {
			msg = strings.TrimSpace(parsed.Error)
		}
		if msg == "" {
			msg = fmt.Sprintf("unexpected status %d", resp.StatusCode)
		}
		return "", 0, errors.New(msg)
	}

	token := strings.TrimSpace(parsed.AccessToken)
	if token == "" {
		return "", 0, errors.New("twitch: refresh returned empty token")
	}

	expiresIn := time.Duration(parsed.ExpiresIn) * time.Second
	if parsed.ExpiresIn <= 0 {
		expiresIn = time.Hour
	}

	if err := writeTokenFile(tokenFile, NormalizeToken(token)); err != nil {
		return "", 0, err
	}

	m.mu.Lock()
	m.lastExpires = expiresIn
	m.mu.Unlock()

	expiresAt := time.Now().Add(expiresIn).UTC()
	log.Printf("twitch: refreshed token; expires at %s", expiresAt.Format(time.RFC3339))

	return token, expiresIn, nil
}

func (m *RefreshManager) StartAuto(ctx context.Context, onUpdate func(token string)) {
	if onUpdate == nil {
		onUpdate = func(string) {}
	}

	go func() {
		wait := m.nextInterval()
		if wait <= 0 {
			wait = time.Minute
		}
		timer := time.NewTimer(wait)
		defer timer.Stop()

		backoff := time.Second

		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}

			token, expires, err := m.Refresh(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("twitch: auto-refresh failed: %v", err)
				timer.Reset(backoff)
				if backoff < time.Minute {
					backoff *= 2
					if backoff > time.Minute {
						backoff = time.Minute
					}
				}
				continue
			}

			backoff = time.Second
			onUpdate(NormalizeToken(token))

			wait = m.intervalFrom(expires)
			timer.Reset(wait)
		}
	}()
}

func (m *RefreshManager) nextInterval() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastExpires <= 0 {
		return 0
	}
	return m.intervalFrom(m.lastExpires)
}

func (m *RefreshManager) intervalFrom(exp time.Duration) time.Duration {
	if exp <= 0 {
		return time.Minute
	}
	next := time.Duration(float64(exp) * 0.85)
	if next <= 0 {
		next = time.Minute
	}
	if next < time.Minute {
		next = time.Minute
	}
	return next
}

func writeTokenFile(path, token string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("twitch: token file path is empty")
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("twitch: open token file: %w", err)
	}
	defer f.Close()

	if err := f.Chmod(0o600); err != nil {
		return fmt.Errorf("twitch: chmod token file: %w", err)
	}

	if _, err := f.WriteString(token + "\n"); err != nil {
		return fmt.Errorf("twitch: write token file: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("twitch: sync token file: %w", err)
	}
	return nil
}
