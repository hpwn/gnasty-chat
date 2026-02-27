package sink

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/you/gnasty-chat/internal/core"
	"github.com/you/gnasty-chat/internal/httpapi"
)

func TestSQLiteAlertsWriteListCount(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "alerts.db")
	s, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	alerts := []core.AlertEvent{
		{
			ID:              "a1",
			PlatformEventID: "a1",
			Platform:        "Twitch",
			Type:            "twitch.subs",
			Ts:              now,
			Username:        "alice",
			Text:            "subscribed",
		},
		{
			ID:              "a2",
			PlatformEventID: "a2",
			Platform:        "YouTube",
			Type:            "youtube.super_chats",
			Ts:              now.Add(time.Second),
			Username:        "bob",
			Text:            "great stream",
			Amount:          5,
			Currency:        "$",
		},
	}
	for _, alert := range alerts {
		if err := s.WriteAlert(alert); err != nil {
			t.Fatalf("write alert: %v", err)
		}
	}

	count, err := s.CountAlerts(context.Background(), httpapi.AlertFilters{})
	if err != nil {
		t.Fatalf("count alerts: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 alerts, got %d", count)
	}

	list, err := s.ListAlerts(context.Background(), httpapi.AlertFilters{
		Platforms: []string{"YouTube"},
		Types:     []string{"youtube.super_chats"},
		Limit:     10,
		Order:     httpapi.OrderDesc,
	})
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(list))
	}
	if list[0].Username != "bob" || list[0].Type != "youtube.super_chats" {
		t.Fatalf("unexpected alert: %#v", list[0])
	}
}
