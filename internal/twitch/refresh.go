package twitch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type refreshResp struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	ExpiresIn    int      `json:"expires_in"`
	Scope        []string `json:"scope"`
	TokenType    string   `json:"token_type"`
}

// Refresh exchanges the refresh token in refreshFile for a new access + refresh
// token pair and atomically writes them to ircFile ("oauth:<access>") and
// refreshFile ("<refresh>").
func Refresh(clientID, clientSecret, refreshFile, ircFile string) error {
	refreshPath := strings.TrimSpace(refreshFile)
	if refreshPath == "" {
		return errors.New("twitch: refresh file path is empty")
	}
	ircPath := strings.TrimSpace(ircFile)
	if ircPath == "" {
		return errors.New("twitch: irc token file path is empty")
	}

	refreshBytes, err := os.ReadFile(refreshPath)
	if err != nil {
		return fmt.Errorf("twitch: read refresh token: %w", err)
	}
	refresh := strings.TrimSpace(string(refreshBytes))
	if refresh == "" {
		return errors.New("twitch: empty refresh token")
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refresh)
	form.Set("client_id", strings.TrimSpace(clientID))
	form.Set("client_secret", strings.TrimSpace(clientSecret))

	req, err := http.NewRequest(http.MethodPost, "https://id.twitch.tv/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("twitch: create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("twitch: refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("twitch: read refresh response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("twitch: refresh status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var rr refreshResp
	if err := json.Unmarshal(body, &rr); err != nil {
		return fmt.Errorf("twitch: decode refresh response: %w", err)
	}
	if rr.AccessToken == "" || rr.RefreshToken == "" {
		return errors.New("twitch: refresh returned empty tokens")
	}

       if err := atomicWrite(ircPath, []byte("oauth:"+strings.TrimSpace(rr.AccessToken)), 0o600); err != nil {
               return fmt.Errorf("twitch: write irc token: %w", err)
       }
       if err := atomicWrite(refreshPath, []byte(strings.TrimSpace(rr.RefreshToken)), 0o600); err != nil {
               return fmt.Errorf("twitch: write refresh token: %w", err)
       }

	return nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil && !os.IsExist(err) {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(path, mode)
}
