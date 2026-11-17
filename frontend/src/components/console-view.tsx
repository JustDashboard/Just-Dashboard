"use client"

import { useEffect, useRef, useState } from "react"
import { CheckCircle, CrossCircle, Slash } from "@/components/icons"
import { gutterCh } from "@/components/code"
import { Pane, PaneHeader } from "@/components/panel"
import { Status } from "@/components/status-dot"
import type { DotTone } from "@/components/tone"
import { TranscriptRow } from "@/components/transcript-line"
import { Button } from "@/components/ui/button"
import type { TranscriptLine } from "@/lib/transcript"
import { cn } from "@/lib/utils"

/** Where a console's command is: still going, or how it ended. */
export type ConsoleState = "running" | "ok" | "failed" | "cancelled"

/** One line of a console, already a transcript line (`jobLineToTranscript`, `streamKind`). */
export type ConsoleLine = {
  key: React.Key
  line: TranscriptLine
  /** What wrote it, before the line: a compose service's name. */
  prefix?: React.ReactNode
}

const STATE: Record<
  ConsoleState,
  { tone: DotTone; word: string; icon?: React.ComponentType<{ className?: string }> }
> = {
  running: { tone: "running", word: "running" },
  ok: { tone: "running", word: "succeeded", icon: CheckCircle },
  failed: { tone: "danger", word: "failed", icon: CrossCircle },
  cancelled: { tone: "stopped", word: "cancelled", icon: Slash },
}

/**
 * A command's output as it runs: a job that outlives the page (`JobConsole`)
 * and a compose command the page's socket owns (`RunConsole`). The two were
 * the same pane copied line for line — header, follow-scroll, stream colours
 * — around different transports, which stay in their hooks.
 *
 * The header is the run's name, its state as a `Status` word spoken when it
 * changes, and the one verb the state allows: Stop while it runs, Dismiss
 * after. The lines are `TranscriptRow`s, numbered and washed the way a
 * restart's transcript is, in one well that follows the newest line until the
 * reader scrolls up. The well is a `log` region, so a screen reader hears new
 * lines politely rather than finding them by looking.
 */
export function ConsoleView({
  title,
  label,
  state,
  detail,
  note,
  lines,
  dropped = 0,
  error,
  empty,
  onCancel,
  onDismiss,
  rise,
  className,
}: {
  title: React.ReactNode
  /** The run's name in words, where `title` is not a string: the log's and the state's name. */
  label?: string
  state: ConsoleState
  /** After the state: when it ended, the status it exited with. */
  detail?: React.ReactNode
  /** A short word on how the run behaves, as a `Tag`: "Keeps running if you leave". */
  note?: React.ReactNode
  lines: ConsoleLine[]
  /** Lines the buffer let go of at the start, said rather than quietly missing. */
  dropped?: number
  /** The run's own failure, washed under the header. */
  error?: React.ReactNode
  /** What the well says before the first line. */
  empty?: string
  /** Shown only while it runs, and only to a reader allowed to stop it. */
  onCancel?: () => void
  onDismiss?: () => void
  /** Rises into place when it mounts: a console that appears when a command starts. */
  rise?: boolean
  className?: string
}) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const [following, setFollowing] = useState(true)

  useEffect(() => {
    if (!following) return
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [lines, following])

  const running = state === "running"
  const name = label ?? (typeof title === "string" ? title : "Output")
  const shown = STATE[state]
  const last = lines.at(-1)?.line.number ?? 0

  return (
    <Pane className={cn("bg-surface-sunken", rise && "animate-rise", className)}>
      <PaneHeader className="flex-wrap gap-x-2 gap-y-1 px-3">
        <span className="min-w-0 flex-1 truncate text-xs font-medium">{title}</span>
        {note}
        <Status
          tone={shown.tone}
          live={running}
          icon={shown.icon}
          label={shown.word}
          announce={`${name} state`}
        />
        {detail && <span className="numeric text-hint text-muted-foreground">{detail}</span>}
        {running && onCancel && (
          <Button size="xs" variant="ghost" className="text-destructive" onClick={onCancel}>
            <Slash className="size-3" />
            Stop
          </Button>
        )}
        {!running && onDismiss && (
          <Button size="xs" variant="ghost" onClick={onDismiss}>
            Dismiss
          </Button>
        )}
      </PaneHeader>

      {error && (
        <p className="border-b border-hairline bg-wash-danger px-3 py-1.5 text-hint text-destructive">
          {error}
        </p>
      )}

      <div
        ref={scrollRef}
        role="log"
        aria-label={name}
        onScroll={() => {
          const el = scrollRef.current
          if (!el) return
          setFollowing(el.scrollHeight - el.scrollTop - el.clientHeight < 40)
        }}
        className="max-h-80 min-h-24 overflow-auto"
      >
        {dropped > 0 && (
          <p className="px-3 pt-2 font-mono text-xs text-muted-foreground">
            … {dropped} earlier {dropped === 1 ? "line" : "lines"} dropped …
          </p>
        )}
        {lines.length === 0 ? (
          <p className="px-3 py-2.5 font-mono text-xs text-muted-foreground">
            {empty ?? (running ? "Starting…" : "No output.")}
          </p>
        ) : (
          <ol className="py-2 font-mono text-xs leading-6">
            {lines.map(({ key, line, prefix }) => (
              <TranscriptRow
                key={key}
                line={line}
                wrap
                needle=""
                gutter={gutterCh(last)}
                stamp={
                  prefix ? (
                    <span className="shrink-0 text-muted-foreground select-none">{prefix} |</span>
                  ) : undefined
                }
              />
            ))}
          </ol>
        )}
      </div>
    </Pane>
  )
}
