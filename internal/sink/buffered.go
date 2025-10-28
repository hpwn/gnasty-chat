package sink

import (
	"errors"
	"sync"
	"time"

	"github.com/you/gnasty-chat/internal/core"
)

type Writer interface {
	Write(core.ChatMessage) error
}

type BufferedWriter struct {
	base          Writer
	batchSize     int
	flushInterval time.Duration

	mu      sync.Mutex
	buffer  []core.ChatMessage
	timer   *time.Timer
	closed  bool
	lastErr error
}

type BufferedOptions struct {
	BatchSize     int
	FlushInterval time.Duration
}

func NewBufferedWriter(base Writer, opts BufferedOptions) *BufferedWriter {
	batch := opts.BatchSize
	if batch <= 0 {
		batch = 1
	}
	return &BufferedWriter{
		base:          base,
		batchSize:     batch,
		flushInterval: opts.FlushInterval,
	}
}

func (b *BufferedWriter) Write(msg core.ChatMessage) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return errors.New("buffered writer closed")
	}

	pendingErr := b.lastErr
	b.lastErr = nil

	b.buffer = append(b.buffer, msg)
	if len(b.buffer) == 1 && b.flushInterval > 0 {
		b.startTimerLocked()
	}

	if len(b.buffer) < b.batchSize {
		b.mu.Unlock()
		return pendingErr
	}

	msgs := append([]core.ChatMessage(nil), b.buffer...)
	b.buffer = b.buffer[:0]
	b.stopTimerLocked()
	b.mu.Unlock()

	if err := b.writeAll(msgs); err != nil {
		return err
	}
	return pendingErr
}

func (b *BufferedWriter) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.stopTimerLocked()
	msgs := append([]core.ChatMessage(nil), b.buffer...)
	b.buffer = nil
	pendingErr := b.lastErr
	b.lastErr = nil
	b.mu.Unlock()

	if len(msgs) > 0 {
		if err := b.writeAll(msgs); err != nil {
			return err
		}
	}
	return pendingErr
}

func (b *BufferedWriter) onTimer() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	if len(b.buffer) == 0 {
		b.timer = nil
		b.mu.Unlock()
		return
	}
	msgs := append([]core.ChatMessage(nil), b.buffer...)
	b.buffer = b.buffer[:0]
	b.timer = nil
	b.mu.Unlock()

	if err := b.writeAll(msgs); err != nil {
		b.mu.Lock()
		b.lastErr = err
		b.mu.Unlock()
	}
}

func (b *BufferedWriter) startTimerLocked() {
	if b.flushInterval <= 0 {
		return
	}
	if b.timer != nil {
		b.timer.Stop()
	}
	b.timer = time.AfterFunc(b.flushInterval, b.onTimer)
}

func (b *BufferedWriter) stopTimerLocked() {
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
}

func (b *BufferedWriter) writeAll(msgs []core.ChatMessage) error {
	for _, msg := range msgs {
		if err := b.base.Write(msg); err != nil {
			return err
		}
	}
	return nil
}
