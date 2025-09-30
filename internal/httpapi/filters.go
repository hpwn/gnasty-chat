package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/you/gnasty-chat/internal/core"
)

const (
	defaultLimit = 100
	maxLimit     = 1000
)

// Order represents the chronological order to use when listing messages.
type Order string

const (
	// OrderDesc returns messages newest first.
	OrderDesc Order = "desc"
	// OrderAsc returns messages oldest first.
	OrderAsc Order = "asc"
)

// Filters captures the parsed query parameters for message lookups.
type Filters struct {
	Platforms []string
	Usernames []string
	Since     *time.Time
	Limit     int
	Order     Order
}

// ParseFilters parses query parameters into a Filters struct.
func ParseFilters(values url.Values) (Filters, error) {
	f := Filters{
		Limit: defaultLimit,
		Order: OrderDesc,
	}

	if raw := values.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return Filters{}, errors.New("limit must be a positive integer")
		}
		if n > maxLimit {
			n = maxLimit
		}
		f.Limit = n
	}

	if raw := values.Get("order"); raw != "" {
		switch strings.ToLower(raw) {
		case "desc":
			f.Order = OrderDesc
		case "asc":
			f.Order = OrderAsc
		default:
			return Filters{}, errors.New("order must be asc or desc")
		}
	}

	if rawSince := values.Get("since"); rawSince != "" {
		parsed, err := parseSince(rawSince)
		if err != nil {
			return Filters{}, err
		}
		f.Since = &parsed
	}

	if platforms := collect(values, "platform"); len(platforms) > 0 {
		seen := make(map[string]struct{})
		var out []string
		var allowAll bool
		for _, raw := range platforms {
			for _, part := range strings.Split(raw, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				canonical, ok := normalizePlatform(part)
				if !ok {
					return Filters{}, errors.New("invalid platform filter")
				}
				if canonical == "" {
					allowAll = true
					out = nil
					seen = make(map[string]struct{})
					continue
				}
				if _, exists := seen[canonical]; !exists && !allowAll {
					out = append(out, canonical)
					seen[canonical] = struct{}{}
				}
			}
		}
		if !allowAll {
			f.Platforms = out
		}
	}

	if usernames := collect(values, "username"); len(usernames) > 0 {
		seen := make(map[string]struct{})
		for _, raw := range usernames {
			for _, part := range strings.Split(raw, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				lowered := strings.ToLower(part)
				if _, exists := seen[lowered]; !exists {
					f.Usernames = append(f.Usernames, lowered)
					seen[lowered] = struct{}{}
				}
			}
		}
	}

	return f, nil
}

// FiltersFromRequest parses filters from an HTTP request.
func FiltersFromRequest(r *http.Request) (Filters, error) {
	return ParseFilters(r.URL.Query())
}

func collect(values url.Values, key string) []string {
	out := values[key]
	if out == nil {
		return nil
	}
	return out
}

func normalizePlatform(p string) (string, bool) {
	switch strings.ToLower(p) {
	case "twitch", "tw", "t":
		return "Twitch", true
	case "youtube", "yt", "y":
		return "YouTube", true
	case "all", "*":
		return "", true
	default:
		return "", false
	}
}

func parseSince(raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.Unix(n, 0).UTC(), nil
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return time.Now().Add(-d).UTC(), nil
	}
	return time.Time{}, errors.New("invalid since parameter")
}

// Matches reports whether the provided message satisfies the filters.
func (f Filters) Matches(msg core.ChatMessage) bool {
	if len(f.Platforms) > 0 {
		match := false
		for _, p := range f.Platforms {
			if p == "" || msg.Platform == p {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}

	if len(f.Usernames) > 0 {
		username := strings.ToLower(msg.Username)
		match := false
		for _, u := range f.Usernames {
			if strings.Contains(username, u) {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}

	if f.Since != nil {
		since := f.Since.UTC()
		if msg.Ts.Before(since) {
			return false
		}
	}

	return true
}

// CloneForStream returns a copy of the filters adjusted for streaming transports.
func (f Filters) CloneForStream() Filters {
	f.Limit = 0
	return f
}
