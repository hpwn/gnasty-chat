package sink

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/you/gnasty-chat/internal/core"
)

type recordingWriter struct {
	mu        sync.Mutex
	messages  []core.ChatMessage
	failAfter int
	calls     int
}

func (r *recordingWriter) Write(msg core.ChatMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.failAfter > 0 && r.calls >= r.failAfter {
		return fmt.Errorf("boom")
	}
	r.messages = append(r.messages, msg)
	return nil
}

func (r *recordingWriter) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.messages)
}

func TestBufferedWriterBatchFlush(t *testing.T) {
	base := &recordingWriter{}
	bw := NewBufferedWriter(base, BufferedOptions{BatchSize: 2, FlushInterval: time.Hour})
	defer func() {
		if err := bw.Close(); err != nil {
			t.Fatalf("close error: %v", err)
		}
	}()

	if err := bw.Write(core.ChatMessage{ID: "1"}); err != nil {
		t.Fatalf("write1: %v", err)
	}
	if base.Count() != 0 {
		t.Fatalf("expected no flush yet")
	}
	if err := bw.Write(core.ChatMessage{ID: "2"}); err != nil {
		t.Fatalf("write2: %v", err)
	}
	if base.Count() != 2 {
		t.Fatalf("expected batch flush, got %d", base.Count())
	}
}

func TestBufferedWriterFlushInterval(t *testing.T) {
	base := &recordingWriter{}
	bw := NewBufferedWriter(base, BufferedOptions{BatchSize: 10, FlushInterval: 20 * time.Millisecond})
	defer func() {
		if err := bw.Close(); err != nil {
			t.Fatalf("close error: %v", err)
		}
	}()

	if err := bw.Write(core.ChatMessage{ID: "interval"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if base.Count() != 1 {
		t.Fatalf("expected timer flush, got %d", base.Count())
	}
}

func TestBufferedWriterErrorPropagation(t *testing.T) {
	base := &recordingWriter{failAfter: 1}
	bw := NewBufferedWriter(base, BufferedOptions{BatchSize: 1, FlushInterval: 0})
	defer func() {
		_ = bw.Close()
	}()

	if err := bw.Write(core.ChatMessage{ID: "err"}); err == nil {
		t.Fatalf("expected error from underlying writer")
	}
}
