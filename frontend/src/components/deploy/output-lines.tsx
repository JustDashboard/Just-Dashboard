"use client"

import { useEffect, useState } from "react"
import { errorMessage, get, type Query } from "@/lib/api"
import { plural } from "@/lib/format"
import { useLogView } from "@/lib/log-view"
import type { LogLine, LogSearchResult } from "@/lib/types"
import { LogRow, eventColumnFor } from "@/components/logs/log-console"

/**
 * A few lines of a log, read for one question and drawn inside the thing that
 * asked it: the container's lines while a request was in flight, what the
 * proxy said about a 502, what a container printed before it died.
 *
 * They are the log console's own rows (`LogRow`), under the reader's own
 * Colour, Wrap and Time, so a line reads here exactly as it does in Output —
 * the one painter every log on the page is drawn by. Read once, when the
 * block is drawn; the thing it sits in is opened on purpose, so the search
 * is the reader's own press rather than one per row on the page.
 */
export function OutputLines({
  title,
  facts,
  query,
  empty,
  action,
}: {
  /** What the lines are, as the block's eyebrow. */
  title: string
  /** What they were read from, and how exactly — "approximate · api-r20". */
  facts?: React.ReactNode
  /** The `/logs/search` question. */
  query: Query
  /** What nothing in the answer means, in the reader's words. */
  empty: string
  /** Where the same lines are read in full. */
  action?: React.ReactNode
}) {
  const { wrap, highlight, time } = useLogView()
  const [state, setState] = useState<{
    key: string
    lines?: LogLine[]
    lens?: string
    error?: string
  }>()
  const key = JSON.stringify(query)

  useEffect(() => {
    const controller = new AbortController()
    get<LogSearchResult>("/logs/search", query, controller.signal).then(
      (result) => setState({ key, lines: result.lines ?? [], lens: result.lens }),
      (err) => {
        if (!controller.signal.aborted) setState({ key, error: errorMessage(err) })
      },
    )
    return () => controller.abort()
    // The question is its words: the object is rebuilt by every render of the
    // row it sits in, and only a different question is a new read.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])

  const answer = state?.key === key ? state : undefined
  const lines = answer?.lines
  const eventColumn = lines ? eventColumnFor(lines, answer?.lens) : false
  return (
    <section aria-label={title} className="mt-3 animate-rise font-sans">
      <div className="mb-1.5 flex min-w-0 items-center gap-2 text-hint text-muted-foreground">
        <span className="eyebrow shrink-0">{title}</span>
        {facts && <span className="flex min-w-0 items-center gap-1.5 truncate">{facts}</span>}
        {lines && lines.length > 0 && (
          <span className="numeric shrink-0">{plural(lines.length, "line")}</span>
        )}
        {action && <span className="ml-auto flex shrink-0 items-center">{action}</span>}
      </div>
      {answer?.error ? (
        <p className="text-hint text-muted-foreground">
          These lines could not be read: {answer.error}
        </p>
      ) : !lines ? (
        <p className="text-hint text-muted-foreground">Reading…</p>
      ) : lines.length === 0 ? (
        <p className="text-hint text-muted-foreground">{empty}</p>
      ) : (
        <div className="max-h-64 overflow-auto rounded-lg border border-hairline bg-surface-sunken py-1 font-mono text-xs leading-relaxed">
          {lines.map((line, i) => (
            <LogRow
              key={i}
              line={line}
              time={time}
              prev={lines[i - 1]?.timestamp}
              wrap={wrap}
              highlight={highlight}
              lens={answer.lens}
              eventColumn={eventColumn}
              cont={Boolean(line.cont) && i > 0}
            />
          ))}
        </div>
      )}
    </section>
  )
}
