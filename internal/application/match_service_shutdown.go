package application

import (
	"context"
)

// Shutdown waits for all background sync goroutines to finish, or until the
// context is cancelled. A cancelled context is reported but not treated as a
// fatal error — outstanding sheet syncs are best-effort.
func (s *MatchServiceImpl) Shutdown(ctx context.Context) error {
	done := make(chan struct{})

	go func() {
		s.syncWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.logger.Info("Match service shutdown completed")
		return nil
	case <-ctx.Done():
		s.logger.Warn("Match service shutdown interrupted: %v — some sync operations may not have completed", ctx.Err())
		return nil
	}
}
