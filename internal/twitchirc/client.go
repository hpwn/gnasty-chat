package twitchirc

import (
	"context"
	"time"
)

type Config struct {
	Channel string
	Nick    string
	Token   string
}

type Client struct {
	cfg Config
}

func New(cfg Config) *Client { return &Client{cfg: cfg} }

func (c *Client) Run(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
