package driver

import "time"

// SetClock moves a Finder's idea of now, so expiry can be tested without a
// test that sleeps for ten minutes.
func SetClock(f *Finder, now func() time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = now
}
