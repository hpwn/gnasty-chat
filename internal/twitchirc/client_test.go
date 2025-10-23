package twitchirc

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
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
