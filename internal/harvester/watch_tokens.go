package harvester

import (
	"log/slog"
	"time"

	"github.com/fsnotify/fsnotify"
)

func (h *Harvester) WatchTokenFiles(paths ...string) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	added := false
	for _, p := range paths {
		if p == "" {
			continue
		}
		if err := w.Add(p); err != nil {
			slog.Error("watch add", "path", p, "err", err)
			continue
		}
		added = true
	}
	if !added {
		w.Close()
		return nil
	}

	go func() {
		defer w.Close()
		debounce := time.NewTimer(0)
		if !debounce.Stop() {
			<-debounce.C
		}
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
					if err := w.Add(ev.Name); err != nil {
						slog.Error("watch re-add", "path", ev.Name, "err", err)
					}
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
					if !debounce.Stop() {
						select {
						case <-debounce.C:
						default:
						}
					}
					debounce.Reset(250 * time.Millisecond)
				}
			case <-debounce.C:
				if _, err := h.ReloadTwitch(); err != nil {
					slog.Error("token reload failed", "err", err)
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				slog.Error("watch error", "err", err)
			}
		}
	}()
	return nil
}
