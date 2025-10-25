package twitchauth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type TokenFiles struct {
	AccessPath   string
	RefreshPath  string
	ClientID     string
	ClientSecret string
}

func (t TokenFiles) ReadAccess() (string, error) {
	b, err := os.ReadFile(t.AccessPath)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(b))
	return strings.TrimPrefix(line, "oauth:"), nil
}

func (t TokenFiles) ReadRefresh() (string, error) {
	b, err := os.ReadFile(t.RefreshPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func ValidateLogin(access string) (string, error) {
	req, _ := http.NewRequest(http.MethodGet, "https://id.twitch.tv/oauth2/validate", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("validate status %d", resp.StatusCode)
	}
	var v struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	if v.Login == "" {
		return "", fmt.Errorf("no login")
	}
	return v.Login, nil
}

func RefreshAccess(clientID, clientSecret, refresh string) (string, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refresh)
	req, _ := http.NewRequest(http.MethodPost, "https://id.twitch.tv/oauth2/token", bytes.NewBufferString(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("refresh status %d: %s", resp.StatusCode, string(body))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("no access_token in refresh")
	}
	return tok.AccessToken, nil
}
