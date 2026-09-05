package driver

import (
	"context"
	"sync"
	"time"

	"github.com/mhd64real/printer-cycle/internal/deviceid"
)

// Lookup is the part of CUPS this package needs: give it a device id, get back
// what CUPS thinks might drive it.
type Lookup func(ctx context.Context, deviceID string) ([]Candidate, error)

// cacheTTL is how long a candidate list is trusted.
//
// The cost being avoided is real and was measured against a live cupsd with a
// full driver installation: a filtered PPD query takes between 2.7 and 5.2
// seconds, and it takes that long every time. CUPS does not cache it, so
// repeating the same query costs the same again. A pairing screen that looked
// candidates up as somebody typed would spend a minute doing it.
//
// Ten minutes rather than a day, because the only thing this can be wrong about
// is a driver installed while it is held, and self-healing in minutes beats
// explaining to somebody why the driver they just installed is not offered.
// printer-cycle installs the whole catalogue up front, so that is a rare case
// to begin with.
const cacheTTL = 10 * time.Minute

// cacheMax bounds the cache.
//
// One entry per distinct printer asked about, and a household has a handful.
// The bound is here so that something asking about generated device ids cannot
// grow it without limit on a machine with 512MB.
const cacheMax = 256

type cached struct {
	candidates []Candidate
	at         time.Time
}

// Finder answers what should drive a printer, once per printer rather than once
// per question.
type Finder struct {
	lookup Lookup
	now    func() time.Time

	mu      sync.Mutex
	entries map[string]cached
}

// New returns a Finder over a lookup.
func New(lookup Lookup) *Finder {
	return &Finder{
		lookup:  lookup,
		now:     time.Now,
		entries: make(map[string]cached, 8),
	}
}

// Candidates returns every driver CUPS offers for a printer, best first.
//
// Keyed on the canonical device id rather than the raw string, so the same
// printer described two ways is one cache entry: a connector that trims a
// trailing semicolon or reorders the fields asks the same question.
func (f *Finder) Candidates(ctx context.Context, printerDeviceID string) ([]Candidate, error) {
	id := deviceid.Parse(printerDeviceID)

	// Something has to name hardware, or there is nothing to look up. A
	// manufacturer or a model will do: "MDL:OKIDATA OKIPAGE 6e;" with no maker
	// is a real device id in the catalogue and CUPS can still match it, while
	// "CMD:PCL;" names a language and no machine at all.
	//
	// Not cached either, or every device that could not describe itself would
	// share one answer, which is the worst possible thing to cache.
	if id.Manufacturer == "" && id.Model == "" {
		return nil, nil
	}
	key := id.String()

	if hit, ok := f.get(key); ok {
		return hit, nil
	}

	found, err := f.lookup(ctx, printerDeviceID)
	if err != nil {
		return nil, err
	}

	ranked := Rank(printerDeviceID, found)
	f.put(key, ranked)
	return ranked, nil
}

// Best is Candidates followed by the choice, and the same caching.
func (f *Finder) Best(ctx context.Context, printerDeviceID string) (Candidate, bool, error) {
	candidates, err := f.Candidates(ctx, printerDeviceID)
	if err != nil {
		return Candidate{}, false, err
	}
	// Already ranked, so this only applies the rules about what may be chosen
	// without asking.
	chosen, safe := Best(printerDeviceID, candidates)
	return chosen, safe, nil
}

// Forget drops everything, for when the catalogue may have changed underneath.
func (f *Finder) Forget() {
	f.mu.Lock()
	defer f.mu.Unlock()
	clear(f.entries)
}

func (f *Finder) get(key string) ([]Candidate, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	entry, ok := f.entries[key]
	if !ok {
		return nil, false
	}
	if f.now().Sub(entry.at) > cacheTTL {
		delete(f.entries, key)
		return nil, false
	}
	return entry.candidates, true
}

func (f *Finder) put(key string, candidates []Candidate) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.entries) >= cacheMax {
		// Evicting the oldest would need a heap or a scan. At this size, and
		// for a cache whose entries all expire anyway, dropping the expired
		// ones and then the whole thing if that was not enough is simpler and
		// cannot go wrong.
		now := f.now()
		for k, e := range f.entries {
			if now.Sub(e.at) > cacheTTL {
				delete(f.entries, k)
			}
		}
		if len(f.entries) >= cacheMax {
			clear(f.entries)
		}
	}

	f.entries[key] = cached{candidates: candidates, at: f.now()}
}
