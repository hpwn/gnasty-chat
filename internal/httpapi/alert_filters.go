package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/you/gnasty-chat/internal/core"
)

// AlertFilters captures query parameters for alert lookups.
type AlertFilters struct {
	Platforms []string
	Types     []string
	Usernames []string
	Since     *time.Time
	Limit     int
	Order     Order
}

func ParseAlertFilters(values url.Values) (AlertFilters, error) {
	f := AlertFilters{
		Limit: defaultLimit,
		Order: OrderDesc,
	}

	base, err := ParseFilters(values)
	if err != nil {
		return AlertFilters{}, err
	}
	f.Platforms = base.Platforms
	f.Usernames = base.Usernames
	f.Since = base.Since
	f.Limit = base.Limit
	f.Order = base.Order

	if rawTypes := collect(values, "type"); len(rawTypes) > 0 {
		seen := map[string]struct{}{}
		for _, raw := range rawTypes {
			for _, part := range strings.Split(raw, ",") {
				part = strings.ToLower(strings.TrimSpace(part))
				if part == "" {
					continue
				}
				if _, ok := seen[part]; ok {
					continue
				}
				seen[part] = struct{}{}
				f.Types = append(f.Types, part)
			}
		}
	}

	return f, nil
}

func AlertFiltersFromRequest(r *http.Request) (AlertFilters, error) {
	if r == nil {
		return AlertFilters{}, errors.New("request required")
	}
	return ParseAlertFilters(r.URL.Query())
}

func (f AlertFilters) CloneForStream() AlertFilters {
	f.Limit = 0
	return f
}

func (f AlertFilters) Matches(alert core.AlertEvent) bool {
	if len(f.Platforms) > 0 {
		match := false
		for _, p := range f.Platforms {
			if p == "" || alert.Platform == p {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}
	if len(f.Types) > 0 {
		match := false
		alertType := strings.ToLower(alert.Type)
		for _, t := range f.Types {
			if alertType == t {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}
	if len(f.Usernames) > 0 {
		username := strings.ToLower(alert.Username)
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
		if alert.Ts.Before(f.Since.UTC()) {
			return false
		}
	}
	return true
}
