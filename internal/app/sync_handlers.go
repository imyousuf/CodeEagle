//go:build app

package app

import (
	"context"
	"fmt"
)

// StartSync launches the sync pipeline asynchronously. Progress is streamed
// via Wails events: sync:log, sync:complete, sync:error.
func (a *App) StartSync(full bool) error {
	if a.syncFunc == nil {
		return fmt.Errorf("sync not available")
	}

	a.syncMu.Lock()
	if a.syncing {
		a.syncMu.Unlock()
		return fmt.Errorf("sync already in progress")
	}
	a.syncing = true
	a.syncMu.Unlock()

	go func() {
		defer func() {
			a.syncMu.Lock()
			a.syncing = false
			a.syncMu.Unlock()
		}()

		logFn := func(format string, args ...any) {
			msg := fmt.Sprintf(format, args...)
			a.emit("sync:log", msg)
		}

		// Warnings also go to the log stream.
		warnFn := func(format string, args ...any) {
			msg := fmt.Sprintf(format, args...)
			a.emit("sync:log", msg)
		}

		err := a.syncFunc(context.Background(), a.cfg, a.repoPaths, full, logFn, warnFn)
		if err != nil {
			a.emit("sync:error", err.Error())
			return
		}

		a.emit("sync:complete", "")
	}()

	return nil
}

// IsSyncing returns whether a sync is currently in progress.
func (a *App) IsSyncing() bool {
	a.syncMu.RLock()
	defer a.syncMu.RUnlock()
	return a.syncing
}
