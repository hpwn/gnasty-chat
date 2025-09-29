package sink

import "github.com/you/gnasty-chat/internal/core"

type broadcaster interface {
	Broadcast(core.ChatMessage)
}

type WithBroadcast struct {
	*SQLiteSink
	api broadcaster
}

func WithAPI(base *SQLiteSink, api broadcaster) *WithBroadcast {
	return &WithBroadcast{SQLiteSink: base, api: api}
}

func (w *WithBroadcast) Write(msg core.ChatMessage) error {
	if err := w.SQLiteSink.Write(msg); err != nil {
		return err
	}
	if w.api != nil {
		w.api.Broadcast(msg)
	}
	return nil
}
