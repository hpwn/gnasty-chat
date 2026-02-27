package sink

import (
	"github.com/you/gnasty-chat/internal/core"
	"github.com/you/gnasty-chat/internal/ingesttrace"
)

type broadcaster interface {
	Broadcast(core.ChatMessage)
	BroadcastAlert(core.AlertEvent)
}

type WithBroadcast struct {
	*SQLiteSink
	api broadcaster
}

func WithAPI(base *SQLiteSink, api broadcaster) *WithBroadcast {
	return &WithBroadcast{SQLiteSink: base, api: api}
}

func (w *WithBroadcast) Write(msg core.ChatMessage, trace *ingesttrace.MessageTrace) error {
	if err := w.SQLiteSink.Write(msg, trace); err != nil {
		return err
	}
	if w.api != nil {
		w.api.Broadcast(msg)
	}
	return nil
}

func (w *WithBroadcast) WriteAlert(alert core.AlertEvent) error {
	if err := w.SQLiteSink.WriteAlert(alert); err != nil {
		return err
	}
	if w.api != nil {
		w.api.BroadcastAlert(alert)
	}
	return nil
}
