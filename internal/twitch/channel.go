package twitch

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// NormalizeChannelLogin converts a Twitch login or channel URL to a canonical
// lowercase login name.
func NormalizeChannelLogin(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("twitch: empty channel")
	}

	candidate := trimmed
	if strings.HasPrefix(candidate, "@") {
		candidate = strings.TrimSpace(candidate[1:])
	}

	if strings.Contains(candidate, "://") || strings.Contains(strings.ToLower(candidate), "twitch.tv/") {
		withScheme := candidate
		if !strings.Contains(withScheme, "://") {
			withScheme = "https://" + withScheme
		}
		u, err := url.Parse(withScheme)
		if err != nil {
			return "", fmt.Errorf("twitch: parse channel url: %w", err)
		}
		host := strings.ToLower(strings.TrimSpace(u.Host))
		if host != "twitch.tv" && host != "www.twitch.tv" {
			return "", fmt.Errorf("twitch: unsupported host %q", u.Host)
		}
		pathParts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(pathParts) == 0 || strings.TrimSpace(pathParts[0]) == "" {
			return "", errors.New("twitch: missing channel login")
		}
		candidate = pathParts[0]
		if strings.HasPrefix(candidate, "@") {
			candidate = strings.TrimSpace(candidate[1:])
		}
	}

	login := strings.ToLower(strings.TrimSpace(candidate))
	if err := validateChannelLogin(login); err != nil {
		return "", err
	}
	return login, nil
}

func validateChannelLogin(login string) error {
	if login == "" {
		return errors.New("twitch: empty channel login")
	}
	if len(login) > 25 {
		return errors.New("twitch: channel login too long")
	}
	for _, r := range login {
		if unicode.IsLower(r) || unicode.IsDigit(r) || r == '_' {
			continue
		}
		return fmt.Errorf("twitch: invalid channel login %q", login)
	}
	return nil
}
