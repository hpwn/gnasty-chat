package httpapi

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/you/gnasty-chat/internal/core"
)

func TestAlertFiltersFromRequest(t *testing.T) {
	req := httptest.NewRequest("GET", "/alerts?platform=yt&type=youtube.super_chats&type=youtube.members&username=alice&since=5m&limit=10&order=asc", nil)
	f, err := AlertFiltersFromRequest(req)
	if err != nil {
		t.Fatalf("parse filters: %v", err)
	}
	if len(f.Platforms) != 1 || f.Platforms[0] != "YouTube" {
		t.Fatalf("unexpected platforms: %#v", f.Platforms)
	}
	if len(f.Types) != 2 {
		t.Fatalf("unexpected types: %#v", f.Types)
	}
	if f.Limit != 10 || f.Order != OrderAsc {
		t.Fatalf("unexpected paging: limit=%d order=%s", f.Limit, f.Order)
	}
	if f.Since == nil {
		t.Fatalf("expected since to be parsed")
	}
}

func TestAlertFiltersMatches(t *testing.T) {
	since := time.Now().Add(-1 * time.Hour)
	f := AlertFilters{
		Platforms: []string{"Twitch"},
		Types:     []string{"twitch.bits"},
		Usernames: []string{"alice"},
		Since:     &since,
	}
	alert := core.AlertEvent{
		Platform: "Twitch",
		Type:     "twitch.bits",
		Username: "Alice",
		Ts:       time.Now().UTC(),
	}
	if !f.Matches(alert) {
		t.Fatalf("expected filter to match")
	}
	alert.Type = "twitch.subs"
	if f.Matches(alert) {
		t.Fatalf("expected type mismatch")
	}
}
