package ipp_test

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mhd64real/printer-cycle/internal/ipp"
)

// TestMeasureIdleCost records what the event loop costs while nothing is
// happening.
//
// This is not a pass or fail test, it is a measurement, and it exists because
// the target hardware is a Raspberry Pi Zero 2 W: four slow cores, 512MB, and a
// process that runs forever. Guessing at the cost of a permanent poll loop on
// that machine is not good enough.
//
// Opt in: PRINTER_CYCLE_MEASURE=1 make measure
func TestMeasureIdleCost(t *testing.T) {
	if os.Getenv("PRINTER_CYCLE_MEASURE") == "" {
		t.Skip("set PRINTER_CYCLE_MEASURE=1 to run: this deliberately idles for a minute")
	}
	c := integrationClient(t)

	window := 60 * time.Second
	if s := os.Getenv("PRINTER_CYCLE_MEASURE_SECONDS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			window = time.Duration(n) * time.Second
		}
	}

	cpuBefore := containerCPU(t)
	reqBefore := c.Requests()
	var ruBefore syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ruBefore); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), window)
	defer cancel()

	var events int
	start := time.Now()
	_ = c.Watch(ctx, ipp.WatchOptions{}, func(ipp.Event) { events++ })
	elapsed := time.Since(start)

	var ruAfter syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ruAfter); err != nil {
		t.Fatal(err)
	}
	cpuAfter := containerCPU(t)
	reqAfter := c.Requests()

	goCPU := cpuTime(ruAfter) - cpuTime(ruBefore)
	cupsCPU := cpuAfter - cpuBefore
	requests := reqAfter - reqBefore

	pct := func(d time.Duration) float64 { return 100 * float64(d) / float64(elapsed) }

	t.Logf("idle window:       %v", elapsed.Round(time.Second))
	t.Logf("events seen:       %d (expected 0 on an idle box)", events)
	t.Logf("requests to cupsd: %d, %.2f/s", requests, float64(requests)/elapsed.Seconds())
	t.Logf("core CPU:          %v, %.3f%% of one core", goCPU.Round(time.Millisecond), pct(goCPU))
	t.Logf("cupsd CPU:         %v, %.3f%% of one core", cupsCPU.Round(time.Millisecond), pct(cupsCPU))
	t.Logf("combined:          %.3f%% of one core on this machine", pct(goCPU+cupsCPU))
	t.Logf("")
	t.Logf("A Pi Zero 2 W core is far slower than this one, so read these as a floor,")
	t.Logf("not a ceiling. What matters is whether the figure is small enough that")
	t.Logf("multiplying it by an order of magnitude still leaves it negligible.")
}

func cpuTime(ru syscall.Rusage) time.Duration {
	tv := func(t syscall.Timeval) time.Duration {
		return time.Duration(t.Sec)*time.Second + time.Duration(t.Usec)*time.Microsecond
	}
	return tv(ru.Utime) + tv(ru.Stime)
}

// containerCPU reads cumulative CPU used by the whole CUPS container.
func containerCPU(t *testing.T) time.Duration {
	t.Helper()

	out, err := exec.Command("docker", "exec", "printer-cycle-cups",
		"cat", "/sys/fs/cgroup/cpu.stat").Output()
	if err != nil {
		t.Skipf("cannot read container CPU: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if usec, ok := strings.CutPrefix(line, "usage_usec "); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(usec), 10, 64)
			if err != nil {
				t.Fatalf("parsing cpu.stat: %v", err)
			}
			return time.Duration(n) * time.Microsecond
		}
	}
	t.Fatal("no usage_usec in cpu.stat")
	return 0
}
