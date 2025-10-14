package twitch

import (
	"errors"
	"os"
	"strings"
	"sync"
)

var ErrEmptyToken = errors.New("twitch: empty token")

// NormalizeToken trims the token and ensures it is prefixed with "oauth:".
// If the input is empty after trimming, an empty string is returned.
func NormalizeToken(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "oauth:") {
		return trimmed
	}
	return "oauth:" + trimmed
}

// FileTokenLoader reads a token from disk and caches the last normalized value.
// It returns the cached value if the file content is unchanged.
type FileTokenLoader struct {
	path   string
	mu     sync.Mutex
	cached string
}

func NewFileTokenLoader(path string) *FileTokenLoader {
	return &FileTokenLoader{path: path}
}

// Load reads and normalizes the token from the loader's file.
// The returned boolean indicates whether the value differs from the cached one.
func (l *FileTokenLoader) Load() (string, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	data, err := os.ReadFile(l.path)
	if err != nil {
		return "", false, err
	}

	token := NormalizeToken(string(data))
	if token == "" {
		l.cached = ""
		return "", false, ErrEmptyToken
	}

	if token == l.cached {
		return l.cached, false, nil
	}

	l.cached = token
	return token, true, nil
}

// SetCached allows callers to pre-populate the cached value. This is useful
// when falling back to a static token while still monitoring the file for
// future rotations.
func (l *FileTokenLoader) SetCached(token string) {
	l.mu.Lock()
	l.cached = NormalizeToken(token)
	l.mu.Unlock()
}
