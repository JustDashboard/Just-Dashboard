"use client"

import { useLayoutEffect, useRef, useState } from "react"
import Link from "next/link"
import { Warning } from "@/components/icons"
import { get } from "@/lib/api"
import { clock } from "@/lib/format"
import type { EngineAction, JournalEntry, SystemdUnit } from "@/lib/types"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { Disclosure } from "@/components/form"
import { Well } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { failureSummary } from "@/components/proxy/engine-lifecycle"

/** How much of the journal the fold reads: enough for a start's last attempt and its reason. */
const JOURNAL_LINES = 30

/**
 * A failed engine, said where the reader can act on it: why systemd says it
 * failed, how often it restarted it first, and the journal's last lines, which
 * is where nginx wrote the reason — a port another process holds, a
 * certificate file that is gone. The line above only said "failed", and the
 * reason was two pages away on Services.
 *
 * The journal is read when the fold opens, afresh each time it does and on
 * Read again, with the old lines cleared first so what shows is never an
 * earlier read passed off as the latest. It reads oldest first, as journalctl
 * does, and opens scrolled to its newest line: the reason is among the last,
 * and thirty lines stand several times the box's height, on a phone seven.
 */
export function EngineFailure({
  engine,
  unit,
  onClear,
  busy,
}: {
  engine: string
  unit: SystemdUnit
  /** Clears the failed state; absent for an account that may not. */
  onClear?: () => void
  /** The service verb in flight, which clearing waits behind. */
  busy?: EngineAction
}) {
  const summary = failureSummary(engine, unit)
  const [open, setOpen] = useState(false)
  const [reads, setReads] = useState(0)
  const journal = usePoll(
    (signal) =>
      get<JournalEntry[]>(
        `/systemd/${encodeURIComponent(unit.name)}/journal`,
        { lines: JOURNAL_LINES },
        signal,
      ),
    0,
    [unit.name, reads],
    { enabled: open },
  )
  const well = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    if (well.current) well.current.scrollTop = well.current.scrollHeight
  }, [journal.data])

  return (
    <Notice tone="danger" icon={Warning} title={summary.title}>
      <div className="space-y-2">
        {summary.facts.length > 0 && <p>{summary.facts.join(" · ")}</p>}
        <Disclosure
          quiet
          summary={`Last ${JOURNAL_LINES} journal lines`}
          open={open}
          onOpenChange={(next) => {
            setOpen(next)
            if (next) setReads((n) => n + 1)
          }}
        >
          <div className="space-y-2 pb-1">
            {journal.loading ? (
              <LoadingRows rows={3} />
            ) : journal.error ? (
              <ErrorState error={journal.error} />
            ) : journal.data && journal.data.length > 0 ? (
              <Well
                ref={well}
                className="max-h-72 space-y-0.5 text-foreground"
                aria-label="Journal"
              >
                {journal.data.map((entry, index) => (
                  <p
                    key={`${entry.timestamp}:${index}`}
                    className={cn(
                      "break-words whitespace-pre-wrap",
                      entry.priority <= 3 && "text-destructive",
                    )}
                  >
                    <span className="text-muted-foreground">{clock(entry.timestamp)}</span>{" "}
                    {entry.message}
                  </p>
                ))}
              </Well>
            ) : (
              <EmptyNote className="py-3">The journal has no lines for {unit.name}.</EmptyNote>
            )}
            {!journal.loading && (
              <Button size="xs" variant="ghost" onClick={() => setReads((n) => n + 1)}>
                Read again
              </Button>
            )}
          </div>
        </Disclosure>
        <div className="flex flex-wrap items-center gap-2">
          {onClear && (
            <Button
              size="xs"
              variant="outline"
              onClick={onClear}
              pending={busy === "reset-failed"}
              disabled={busy !== undefined}
            >
              Clear failed state
            </Button>
          )}
          <Button size="xs" variant="ghost" asChild>
            <Link href={`/processes/services?unit=${encodeURIComponent(unit.name)}`}>
              Service details
            </Link>
          </Button>
        </div>
      </div>
    </Notice>
  )
}
