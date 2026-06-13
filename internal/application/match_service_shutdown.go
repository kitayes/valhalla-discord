package application

import "time"

// Shutdown gracefully waits for all background goroutines to complete
func (s *MatchServiceImpl) Shutdown(timeout time.Duration) error {
	done := make(chan struct{})

	go func() {
		s.syncWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.logger.Info("Match service shutdown completed")
		return nil
	case <-time.After(timeout):
		s.logger.Warn("Match service shutdown timeout - some sync operations may not have completed")
		return nil // Don't error on timeout, just warn
	}
}
