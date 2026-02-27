package sink

import "github.com/you/gnasty-chat/internal/core"

// AlertWriter persists non-chat alert events.
type AlertWriter interface {
	WriteAlert(core.AlertEvent) error
}
