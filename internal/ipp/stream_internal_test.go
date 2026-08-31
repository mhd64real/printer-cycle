package ipp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OpenPrinting/goipp"
)

// TestDiscoveryEmitsBeforeTheResponseEnds pins the property that matters, with
// timing this test controls rather than timing CUPS happens to produce.
//
// An earlier version asserted the spread between arrivals from a real cupsd and
// was flaky: when CUPS finds two devices at nearly the same moment they are
// delivered at nearly the same moment, which is correct behaviour failing an
// assertion about it. The behaviour to prove is that a device reaches the caller
// while the response is still open, so that is what is proven here, against a
// server that deliberately pauses mid-stream.
func TestDiscoveryEmitsBeforeTheResponseEnds(t *testing.T) {
	device := func(uri, info string) goipp.Group {
		var attrs goipp.Attributes
		attrs.Add(goipp.MakeAttribute("device-uri", goipp.TagURI, goipp.String(uri)))
		attrs.Add(goipp.MakeAttribute("device-info", goipp.TagText, goipp.String(info)))
		attrs.Add(goipp.MakeAttribute("device-class", goipp.TagKeyword, goipp.String("network")))
		return goipp.Group{Tag: goipp.TagPrinterGroup, Attrs: attrs}
	}

	var op goipp.Attributes
	op.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset, goipp.String("utf-8")))
	op.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage, goipp.String("en-us")))

	msg := goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOk, 1)
	msg.Groups = goipp.Groups{
		{Tag: goipp.TagOperationGroup, Attrs: op},
		device("socket://10.0.0.1:9100", "First"),
		device("socket://10.0.0.2:9100", "Second"),
		device("socket://10.0.0.3:9100", "Third"),
	}

	full, err := msg.EncodeBytes()
	if err != nil {
		t.Fatal(err)
	}

	// Split so each device lands in its own write, with a pause between. A
	// buffering client cannot produce anything until the last write.
	const pause = 150 * time.Millisecond
	cut := len(full) / 4

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/ipp")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("the test server cannot flush, so nothing can be trickled")
			return
		}
		for start := 0; start < len(full); start += cut {
			end := min(start+cut, len(full))
			w.Write(full[start:end])
			flusher.Flush()
			if end < len(full) {
				time.Sleep(pause)
			}
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	var arrivals []time.Duration
	var devices []Device

	err = c.DiscoverDevices(ctx, 0, func(d Device) {
		arrivals = append(arrivals, time.Since(start))
		devices = append(devices, d)
	})
	total := time.Since(start)
	if err != nil {
		t.Fatalf("DiscoverDevices: %v", err)
	}

	if len(devices) != 3 {
		t.Fatalf("got %d devices, want 3", len(devices))
	}
	for i, at := range arrivals {
		t.Logf("device %d at %v of %v", i+1, at.Round(10*time.Millisecond), total.Round(10*time.Millisecond))
	}

	// The first device must arrive before the response is finished. A client
	// that decoded only at the end would report every arrival at the same
	// instant, right at the close.
	if arrivals[0] >= total-pause {
		t.Errorf("the first device arrived at %v of a %v response; nothing was delivered early",
			arrivals[0], total)
	}
}
