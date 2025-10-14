package twitch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeToken(t *testing.T) {
	cases := []struct {
		in  string
		out string
	}{
		{"", ""},
		{"   ", ""},
		{"oauth:abc", "oauth:abc"},
		{"abc", "oauth:abc"},
		{"  abc\n", "oauth:abc"},
	}

	for _, c := range cases {
		got := NormalizeToken(c.in)
		if got != c.out {
			t.Fatalf("NormalizeToken(%q) = %q; want %q", c.in, got, c.out)
		}
	}
}

func TestFileTokenLoader_Load(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")

	if err := os.WriteFile(path, []byte("oauth:first"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	loader := NewFileTokenLoader(path)

	token, changed, err := loader.Load()
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if !changed {
		t.Fatalf("first load should report changed")
	}
	if token != "oauth:first" {
		t.Fatalf("first token = %q", token)
	}

	token, changed, err = loader.Load()
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if changed {
		t.Fatalf("second load should not report changed")
	}
	if token != "oauth:first" {
		t.Fatalf("second token = %q", token)
	}

	if err := os.WriteFile(path, []byte("rotated"), 0o600); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	token, changed, err = loader.Load()
	if err != nil {
		t.Fatalf("third load: %v", err)
	}
	if !changed {
		t.Fatalf("third load should report changed")
	}
	if token != "oauth:rotated" {
		t.Fatalf("third token = %q", token)
	}
}

func TestFileTokenLoader_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("\n\n"), 0o600); err != nil {
		t.Fatalf("write empty: %v", err)
	}

	loader := NewFileTokenLoader(path)
	token, changed, err := loader.Load()
	if !errors.Is(err, ErrEmptyToken) {
		t.Fatalf("expected ErrEmptyToken, got %v", err)
	}
	if token != "" || changed {
		t.Fatalf("expected empty token, changed=false; got %q, %v", token, changed)
	}
}
