import { useCallback, useEffect, useRef, useState } from "react";

import { Button } from "@/components/Button";
import { Notice } from "@/components/Notice";
import { api, subscribe, type Job, type JobState, type JobUpdate, type Printer } from "@/api";

/** States a job can still move out of, and so can still be stopped. */
const RUNNING: JobState[] = ["queued", "held", "printing", "stopped"];

/**
 * What a state means, in words rather than in the vocabulary of a print system.
 *
 * "stopped" in particular: IPP means the printer stopped, which for anybody
 * standing next to it means it wants something, and that is worth saying.
 */
function describeState(job: Job): string {
  switch (job.state) {
    case "queued":
      return "Waiting";
    case "held":
      return "Held";
    case "printing":
      return job.pages_total > 0
        ? `Printing, page ${job.pages_done} of ${job.pages_total}`
        : "Printing";
    case "stopped":
      return "Stopped, the printer needs attention";
    case "done":
      return "Printed";
    case "failed":
      return "Failed";
    case "cancelled":
      return "Cancelled";
    default:
      return job.state;
  }
}

/**
 * The jobs page.
 *
 * Pushed, never polled. The list is fetched once and everything after that
 * arrives because core decided the page should know, which is the whole reason
 * the notification channel exists. A page that polled would be asking a
 * Raspberry Pi a question several times a second for an answer that is almost
 * always "nothing has changed".
 */
export function Jobs() {
  const [jobs, setJobs] = useState<Job[] | null>(null);
  const [printers, setPrinters] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [cancelling, setCancelling] = useState<string | null>(null);

  // Held in a ref as well, because the subscription is set up once and its
  // handler would otherwise close over the first, empty list forever.
  const known = useRef<Set<string>>(new Set());

  const load = useCallback(async () => {
    try {
      const [{ jobs }, { printers }] = await Promise.all([api.jobs(), api.printers()]);
      setJobs(jobs ?? []);
      known.current = new Set((jobs ?? []).map((j) => j.job_id));
      setPrinters(
        Object.fromEntries((printers ?? []).map((p: Printer) => [p.id, p.name])),
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : "cannot list what has been printed");
    }
  }, []);

  useEffect(() => {
    void load();

    const stop = subscribe({
      "job.updated": (data) => {
        const update = data as JobUpdate;

        // A job this page has never heard of is one that started somewhere
        // else: another connector, another tab, the print page next door. The
        // update alone does not carry enough to render a row, so the list is
        // fetched again. Still not polling: nothing happens until core says
        // something did.
        if (!known.current.has(update.job_id)) {
          void load();
          return;
        }

        setJobs((current) =>
          (current ?? []).map((job) =>
            job.job_id === update.job_id
              ? {
                  ...job,
                  state: update.state,
                  state_reasons: update.state_reasons,
                  pages_done: update.pages_done,
                  pages_total: update.pages_total,
                }
              : job,
          ),
        );
      },
    });

    return stop;
  }, [load]);

  async function cancel(job: Job) {
    setCancelling(job.job_id);
    setError(null);
    try {
      const { state } = await api.cancelJob(job.job_id);
      setJobs((current) =>
        (current ?? []).map((j) => (j.job_id === job.job_id ? { ...j, state } : j)),
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : "could not stop that job");
    } finally {
      setCancelling(null);
    }
  }

  if (jobs === null) {
    return <p className="text-sm text-muted">Loading.</p>;
  }

  return (
    <div>
      {error ? (
        <div className="mb-4 max-w-lg">
          <Notice>{error}</Notice>
        </div>
      ) : null}

      {jobs.length === 0 ? (
        <p className="text-sm text-muted">
          Nothing has been printed yet. Anything sent from here, or from a connector, shows up
          on this page as it happens.
        </p>
      ) : (
        <ul className="divide-y divide-line">
          {jobs.map((job) => (
            <li key={job.job_id} className="flex items-center justify-between gap-4 py-3">
              <div className="min-w-0">
                <p className="truncate font-medium">{job.name || "Untitled"}</p>
                <p className="truncate text-sm text-muted">
                  {describeState(job)}
                  {printers[job.printer_id] ? ` on ${printers[job.printer_id]}` : ""}
                </p>
                {job.state === "failed" && job.state_reasons ? (
                  <p className="truncate text-sm text-muted">{job.state_reasons}</p>
                ) : null}
              </div>

              {RUNNING.includes(job.state) ? (
                <Button
                  variant="plain"
                  onClick={() => cancel(job)}
                  disabled={cancelling === job.job_id}
                >
                  {cancelling === job.job_id ? "Stopping" : "Stop"}
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
