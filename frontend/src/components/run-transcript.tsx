"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { Copy, Download } from "@/components/icons"
import { Pane, PaneFooter, PaneHeader } from "@/components/panel"
import { SearchInput } from "@/components/page"
import { ChipCount, FilterChip } from "@/components/tabs"
import { IconAction } from "@/components/icon-action"
import { Status } from "@/components/status-dot"
import { TranscriptRow, useTranscriptDrip } from "@/components/transcript-line"
import { BorderBeam } from "@/components/ui/border-beam"
import { getText } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { downloadText } from "@/lib/metrics-export"
import { extendTranscript, isTrimmed, transcriptLines } from "@/lib/transcript"
import { cn } from "@/lib/utils"

/**
 * A restart's or an upgrade's transcript, read line by line.
 *
 * It used to be a 256px well of pre-wrapped text holding the last 64 KB of the
 * file — for a rebuild, about the second half of it, in one grey column where
 * two images building at once braid their BuildKit steps together. This is the
 * whole run in a console sized to be read: every line numbered, each of
 * `lib/transcript.ts`'s shapes drawn as itself, the step a line belongs to
 * coloured by the service it is building, and a failure washed where it
 * happened rather than repeated in a banner above.
 *
 * **Whole, not the tail.** The report the page polls carries the end of the
 * file, because it is asked for every two seconds. When that end says it was
 * trimmed, the console reads the whole transcript once from `source` and from
 * then on extends it with each tail (`extendTranscript`), so a long rebuild is
 * one file from its first `#1` without a megabyte on every poll. During the
 * restart itself the backend is briefly gone; a failed read leaves what is on
 * screen alone.
 *
 * **Line by line.** A poll lands forty lines at once, and forty lines
 * appearing in a frame is a page that jumped. While the run is live and the
 * reader is at the end, the new lines are let out a few a frame — the whole
 * batch well inside the poll that brought it — each rising into place once
 * (§11's *arrived*). Scrolled up, searching, or with reduced motion, the lines
 * are simply there.
 */
export function RunTranscript({
  text,
  live,
  source,
  label = "Transcript",
  className,
}: {
  /** The transcript as the report carries it, trimmed marker and all. */
  text: string
  live: boolean
  /** Where the whole file is read from when `text` is only its end. */
  source?: string
  label?: string
  /** Its height: a console sizes itself to the space it is given. */
  className?: string
}) {
  const [whole, setWhole] = useState<string>()
  const [query, setQuery] = useState("")
  const [errorsOnly, setErrorsOnly] = useState(false)
  const [wrap, setWrap] = useState(true)
  const [follow, setFollow] = useState(true)
  const body = useRef<HTMLDivElement>(null)

  const merged = useMemo(
    () => (whole === undefined ? { text, overlapped: true } : extendTranscript(whole, text)),
    [whole, text],
  )
  // Asked for once when the tail is short of the start, and again only if the
  // file moved on so far between two polls that the tail no longer overlaps.
  const stale = Boolean(source) && isTrimmed(text) && (whole === undefined || !merged.overlapped)
  useEffect(() => {
    if (!stale || !source) return
    const ctrl = new AbortController()
    getText(source, ctrl.signal)
      .then((full) => setWhole(full))
      // Unreachable is the restart doing its job; the tail is still on screen.
      .catch(() => undefined)
    return () => ctrl.abort()
  }, [stale, source])

  const display = whole !== undefined && merged.overlapped ? merged.text : text
  const lines = useMemo(() => transcriptLines(display), [display])

  const { drawn, generation, arrivedFrom } = useTranscriptDrip(
    display,
    lines,
    live && follow && !query && !errorsOnly,
  )
  const needle = query.trim().toLowerCase()
  const visible = useMemo(
    () =>
      drawn.filter(
        (line) =>
          (!errorsOnly || line.kind === "error") &&
          (!needle || line.text.toLowerCase().includes(needle)),
      ),
    [drawn, errorsOnly, needle],
  )
  const errors = useMemo(() => lines.filter((line) => line.kind === "error").length, [lines])
  const steps = useMemo(
    () => new Set(lines.filter((line) => line.kind === "step").map((line) => line.step)).size,
    [lines],
  )

  useEffect(() => {
    if (follow && body.current) body.current.scrollTop = body.current.scrollHeight
  }, [visible, follow])

  const trimmed = isTrimmed(display)
  const plain = () => lines.map((line) => line.text).join("\n")

  return (
    <Pane className={cn("relative h-[min(70vh,44rem)] min-h-72", className)}>
      {live && <BorderBeam size={96} duration={7} />}
      <PaneHeader className="flex-wrap gap-2 py-2">
        <span className="text-xs font-medium">{label}</span>
        {/* Not `live`: the transcript arrives on the report's two-second poll,
            and §11 keeps the breathing dot for a socket. The beam says it is
            in flight. */}
        <Status
          tone={live ? "running" : errors > 0 ? "danger" : "stopped"}
          label={live ? "Running" : "Finished"}
        />
        <SearchInput
          dense
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Find in the transcript"
          aria-label="Find in the transcript"
          containerClassName="order-last min-w-0 basis-full sm:order-none sm:ml-auto sm:max-w-64 sm:flex-1 sm:basis-auto"
        />
        <span className="ml-auto flex shrink-0 items-center gap-1.5 sm:ml-0">
          <FilterChip
            selected={errorsOnly}
            disabled={errors === 0 && !errorsOnly}
            className="disabled:opacity-50"
            onClick={() => setErrorsOnly(!errorsOnly)}
          >
            Errors <ChipCount>{errors}</ChipCount>
          </FilterChip>
          <FilterChip selected={wrap} onClick={() => setWrap(!wrap)}>
            Wrap
          </FilterChip>
          <FilterChip
            selected={follow}
            onClick={() => {
              setFollow(!follow)
              if (!follow && body.current) body.current.scrollTop = body.current.scrollHeight
            }}
          >
            Follow
          </FilterChip>
          <IconAction
            label="Copy the transcript"
            disabled={!lines.length}
            onClick={() => copyText(plain(), "Transcript copied")}
          >
            <Copy />
          </IconAction>
          <IconAction
            label="Download the transcript"
            disabled={!lines.length}
            onClick={() =>
              downloadText(
                `${label.toLowerCase().replace(/\s+/g, "-")}.log`,
                `${plain()}\n`,
                "text/plain",
              )
            }
          >
            <Download />
          </IconAction>
        </span>
      </PaneHeader>

      <div
        ref={body}
        onScroll={(event) => {
          const el = event.currentTarget
          const atEnd = el.scrollHeight - el.scrollTop - el.clientHeight < 32
          // Following stops the moment the reader scrolls up, or a log that is
          // still growing yanks them back down every two seconds.
          if (atEnd !== follow) setFollow(atEnd)
        }}
        className="min-h-0 flex-1 overflow-auto bg-surface-sunken"
      >
        {visible.length > 0 ? (
          <ol
            aria-label={label}
            className={cn("py-3 font-mono text-xs leading-6", !wrap && "min-w-max")}
          >
            {visible.map((line) => (
              <TranscriptRow
                key={`${generation}:${line.number}`}
                line={line}
                wrap={wrap}
                needle={needle}
                arrived={line.number > arrivedFrom}
                loading={line.kind === "trimmed" && stale}
              />
            ))}
          </ol>
        ) : (
          <div className="flex h-full min-h-40 items-center justify-center px-6 text-center text-body text-muted-foreground">
            {lines.length
              ? "No line matches that."
              : live
                ? "Waiting for the first line. New lines appear here as they are written."
                : "This run left no transcript."}
          </div>
        )}
      </div>

      <PaneFooter className="justify-between text-hint text-muted-foreground">
        <span className="numeric">
          {visible.length === lines.length
            ? `${lines.length.toLocaleString()} lines`
            : `${visible.length.toLocaleString()} of ${lines.length.toLocaleString()} lines`}
          {steps > 0 && ` · ${steps} build steps`}
          {errors > 0 && ` · ${errors} ${errors === 1 ? "error" : "errors"}`}
        </span>
        <span>
          {trimmed
            ? stale
              ? "Reading the whole transcript…"
              : "The last 64 KB"
            : live
              ? follow
                ? "Following the newest line"
                : "Paused — scroll to the end to follow"
              : "The whole transcript"}
        </span>
      </PaneFooter>
    </Pane>
  )
}
