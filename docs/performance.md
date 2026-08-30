# Measured costs

Numbers, not opinions. printer-cycle targets a Raspberry Pi Zero 2 W: four slow cores and 512MB of
RAM shared with the operating system, cupsd, and Ghostscript. Anything that runs forever has to
justify itself there.

Reproduce any of these with `make measure` and `make test-integration`.

## The event loop, idle

Core learns about print jobs by subscribing to CUPS events and asking for them on an interval. It has
to ask, rather than being told: **CUPS 2.4.10 does not honour `notify-wait`**, so `Get-Notifications`
returns immediately whether or not anything is pending, and CUPS advertises `notify-get-interval: 60`,
expecting clients to poll once a minute. A minute is useless for showing somebody their print job
moving, so core chooses its own interval: 500ms while events are arriving, backing off to 2s after
five empty replies.

Measured over one minute of a completely idle box, on an Apple Silicon Mac talking to CUPS in Docker:

| | |
| --- | --- |
| requests to cupsd | 35, or 0.58 per second |
| core CPU | 22ms, 0.036% of one core |
| cupsd CPU | 26ms, 0.043% of one core |
| **combined** | **0.079% of one core** |

**Read that as a floor.** A Zero 2 W core is perhaps ten to twenty times slower than the one measured
here, which puts the loop somewhere near 1 to 2% of a single core, on a machine that has four. The
figure survives being wrong by an order of magnitude, which is the only reason it is safe to rely on.

One thing pulls the other way and makes the real figure lower: this measurement went over TCP through
Docker's bridge, while production talks to cupsd over a Unix socket on the same machine.

**Decision: the intervals stand, and no wake mechanism is needed.** Stage 20 held open the option of
letting core poll immediately after submitting a job so the idle interval could grow much longer. At
0.079% of a core there is nothing to buy with that complexity. Revisit only if the idle interval ever
needs to stretch beyond a couple of seconds.

The cost of asking does not grow with the number of printers or jobs. One request covers the whole
machine. Polling job state instead would mean one request per queue, growing with every printer
somebody adds.

## Streaming a document

A document is never held in memory. The encoded IPP message goes out first and the document follows
it in the same request body, with no length known in advance, so the request is chunked.

| | |
| --- | --- |
| document sent | 32MB |
| peak heap growth | 0.1MB |
| bytes CUPS accounted for | 32.0MB |

The third row matters as much as the second. A flat heap is also consistent with the document never
having been sent, so the transfer is confirmed against `job-k-octets` read back from CUPS.

On a 512MB machine shared with Ghostscript, buffering a large scan is not a performance problem, it
is an out-of-memory kill.

## Discovery

Discovery is slow because it has to be: the SNMP backend broadcasts across the subnet and waits out
its own timeout.

| | |
| --- | --- |
| first devices | 30ms |
| last device | ~2.5s |
| response encoding | chunked |

Because CUPS streams the response, devices are delivered as they are found rather than in one batch
at the end. Nothing user-facing should block on the complete list.

## The driver catalogue

| | |
| --- | --- |
| drivers installed | 17,974 |
| unfiltered fetch and decode | 2.6s |
| filtered by device id | 2 to 5s, a handful of results |

Filtering is not faster than fetching everything, because CUPS scans the whole catalogue either way.
What it saves is memory and the size of the response. **Driver lookup is not interactive speed and
has to be cached**, which is why Stage 54 includes a cache keyed by device id.
