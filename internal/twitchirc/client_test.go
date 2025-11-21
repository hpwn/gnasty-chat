package twitchirc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/you/gnasty-chat/internal/core"
)

func TestAuthFailureTriggersRefresh(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
				default:
				}
				return
			}

			go func(c net.Conn) {
				defer c.Close()
				reader := bufio.NewReader(c)
				for i := 0; i < 4; i++ {
					if _, err := reader.ReadString('\n'); err != nil {
						return
					}
				}
				fmt.Fprintf(c, ":tmi.twitch.tv NOTICE * :Login authentication failed\r\n")
			}(conn)
		}
	}()

	tokenMu := sync.Mutex{}
	token := "oauth:old"
	refreshCalled := make(chan struct{}, 1)

	client := New(Config{
		Channel: "chan",
		Nick:    "nick",
		Token:   token,
		UseTLS:  false,
		Addr:    ln.Addr().String(),
		TokenProvider: func() string {
			tokenMu.Lock()
			defer tokenMu.Unlock()
			return token
		},
		RefreshNow: func(ctx context.Context) (string, error) {
			tokenMu.Lock()
			token = "oauth:new"
			tokenMu.Unlock()
			select {
			case refreshCalled <- struct{}{}:
			default:
			}
			return token, nil
		},
	}, nil)

	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx)
	}()

	select {
	case <-refreshCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("RefreshNow was not called")
	}

	cancel()
	_ = ln.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("client did not exit")
	}
	wg.Wait()
}

func TestParsePrivmsgBadges(t *testing.T) {
	channel := "chan"
	tests := []struct {
		name     string
		line     string
		expected []core.ChatBadge
		raw      core.BadgesRaw
	}{
		{
			name: "moderator subscriber with info override",
			line: "@badge-info=subscriber/24;badges=moderator/1,subscriber/6,partner/1;display-name=User;color=#1E90FF;id=msg-1;" +
				"tmi-sent-ts=1234567890 :user!user@user.tmi.twitch.tv PRIVMSG #chan :hello world",
			expected: []core.ChatBadge{
				{Platform: "twitch", ID: "moderator", Version: "1"},
				{Platform: "twitch", ID: "subscriber", Version: "24"},
				{Platform: "twitch", ID: "partner", Version: "1"},
			},
			raw: core.BadgesRaw{"twitch": map[string]string{"badges": "moderator/1,subscriber/6,partner/1", "badge_info": "subscriber/24"}},
		},
		{
			name:     "broadcaster channel fallback",
			line:     "@badges=broadcaster/;display-name=Streamer;id=msg-2; :streamer!streamer@streamer.tmi.twitch.tv PRIVMSG #chan :hi",
			expected: []core.ChatBadge{{Platform: "twitch", ID: "broadcaster", Version: channel}},
			raw:      core.BadgesRaw{"twitch": map[string]string{"badges": "broadcaster/"}},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			msg, _, ok, _ := parsePrivmsg(context.Background(), tt.line, channel, nil)
			if !ok {
				t.Fatalf("expected parsePrivmsg to succeed")
			}
			if !reflect.DeepEqual(msg.Badges, tt.expected) {
				t.Fatalf("badges mismatch:\nexpected %#v\nactual   %#v", tt.expected, msg.Badges)
			}
			if !reflect.DeepEqual(msg.BadgesRaw, tt.raw) {
				t.Fatalf("badges raw mismatch:\nexpected %#v\nactual   %#v", tt.raw, msg.BadgesRaw)
			}
		})
	}
}

type stubBadgeResolver struct{}

func (stubBadgeResolver) Enrich(_ context.Context, _ string, badges []core.ChatBadge) []core.ChatBadge {
	out := make([]core.ChatBadge, len(badges))
	copy(out, badges)
	for i := range out {
		out[i].Images = []core.ChatBadgeImage{{URL: "https://example.com/badge.png", Width: 18, Height: 18}}
	}
	return out
}

func TestParsePrivmsgEnrichesBadges(t *testing.T) {
	line := "@badges=moderator/1;display-name=User;id=msg-3; :user!user@user.tmi.twitch.tv PRIVMSG #chan :hi"
	msg, _, ok, _ := parsePrivmsg(context.Background(), line, "chan", stubBadgeResolver{})
	if !ok {
		t.Fatalf("expected parsePrivmsg to succeed")
	}
	if len(msg.Badges) != 1 {
		t.Fatalf("expected one badge, got %d", len(msg.Badges))
	}
	if len(msg.Badges[0].Images) != 1 {
		t.Fatalf("expected badge images to be populated")
	}
}

func TestParsePrivmsgEncodesBadgeImages(t *testing.T) {
	line := "@badges=moderator/1;badge-info=subscriber/6;display-name=User;id=msg-4; :user!user@user.tmi.twitch.tv PRIVMSG #chan :hello"
	msg, _, ok, _ := parsePrivmsg(context.Background(), line, "chan", stubBadgeResolver{})
	if !ok {
		t.Fatalf("expected parsePrivmsg to succeed")
	}

	var payload struct {
		Badges []core.ChatBadge `json:"badges"`
		Raw    core.BadgesRaw   `json:"raw"`
	}
	if err := json.Unmarshal([]byte(msg.BadgesJSON), &payload); err != nil {
		t.Fatalf("failed to decode badges json: %v", err)
	}
	if len(payload.Badges) != 1 || len(payload.Badges[0].Images) != 1 {
		t.Fatalf("expected encoded badge images, got %#v", payload.Badges)
	}
	if payload.Raw == nil || payload.Raw["twitch"] == nil {
		t.Fatalf("expected raw twitch badge info to be preserved, got %#v", payload.Raw)
	}
}

func TestParsePrivmsgWithResolverPopulatesImages(t *testing.T) {
	line := "@badge-info=subscriber/24;badges=subscriber/24,premium/1;display-name=User;id=msg-5; :user!user@user.tmi.twitch.tv PRIVMSG #chan :hi"
	msg, _, ok, _ := parsePrivmsg(context.Background(), line, "chan", stubBadgeResolver{})
	if !ok {
		t.Fatalf("expected parsePrivmsg to succeed")
	}

	if len(msg.Badges) != 2 {
		t.Fatalf("expected two badges, got %d", len(msg.Badges))
	}
	for i, badge := range msg.Badges {
		if len(badge.Images) == 0 {
			t.Fatalf("badge %d missing images: %#v", i, badge)
		}
	}

	var payload struct {
		Badges []core.ChatBadge `json:"badges"`
	}
	if err := json.Unmarshal([]byte(msg.BadgesJSON), &payload); err != nil {
		t.Fatalf("failed to decode badges json: %v", err)
	}
	if len(payload.Badges) != 2 || len(payload.Badges[0].Images) == 0 || len(payload.Badges[1].Images) == 0 {
		t.Fatalf("expected serialized badge images, got %#v", payload.Badges)
	}
}

func TestParsePrivmsgWithoutResolverKeepsBadges(t *testing.T) {
	line := "@badge-info=subscriber/12;badges=subscriber/12,partner/1;display-name=User;id=msg-6; :user!user@user.tmi.twitch.tv PRIVMSG #chan :hi"
	msg, _, ok, _ := parsePrivmsg(context.Background(), line, "chan", nil)
	if !ok {
		t.Fatalf("expected parsePrivmsg to succeed")
	}

	if len(msg.Badges) != 2 {
		t.Fatalf("expected two badges, got %d", len(msg.Badges))
	}
	for i, badge := range msg.Badges {
		if badge.Images != nil {
			t.Fatalf("expected badge %d images to be empty when resolver disabled, got %#v", i, badge.Images)
		}
	}
}

type deadlineBadgeResolver struct {
	deadlineSet bool
}

func (d *deadlineBadgeResolver) Enrich(ctx context.Context, _ string, badges []core.ChatBadge) []core.ChatBadge {
	_, d.deadlineSet = ctx.Deadline()
	return badges
}

func TestParsePrivmsgBadgeEnrichmentTimeout(t *testing.T) {
	line := "@badges=moderator/1;display-name=User;id=msg-3; :user!user@user.tmi.twitch.tv PRIVMSG #chan :hi"
	resolver := &deadlineBadgeResolver{}

	_, _, ok, _ := parsePrivmsg(context.Background(), line, "chan", resolver)
	if !ok {
		t.Fatalf("expected parsePrivmsg to succeed")
	}
	if !resolver.deadlineSet {
		t.Fatalf("expected badge resolver context to include a deadline")
	}
}
