"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { useReducedMotion } from "motion/react"
import { Copy, Download } from "@/components/icons"
import { Pane, PaneFooter, PaneHeader } from "@/components/panel"
import { SearchInput } from "@/components/page"
import { ChipCount, FilterChip } from "@/components/tabs"
import { IconAction } from "@/components/icon-action"
import { Status } from "@/components/status-dot"
import { BorderBeam } from "@/components/ui/border-beam"
import { getText } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { downloadText } from "@/lib/metrics-export"
import { extendTranscript, isTrimmed, transcriptLines, type TranscriptLine } from "@/lib/transcript"
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
  const reduced = useReducedMotion()
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

  // The drip. `held` is how many lines at the end are not drawn yet; it grows
  // only when the transcript grew by appending, so the switch from the tail to
  // the whole file — which renumbers everything — is drawn at once.
  const drips = live && follow && !query && !errorsOnly && !reduced
  const [previous, setPrevious] = useState({ display, count: lines.length, generation: 0 })
  const [held, setHeld] = useState(0)
  const [arrivedFrom, setArrivedFrom] = useState(lines.length)
  if (display !== previous.display) {
    const appended = display.startsWith(previous.display)
    setPrevious({
      display,
      count: lines.length,
      generation: appended ? previous.generation : previous.generation + 1,
    })
    if (appended && drips) {
      // Four hundred is a screenful and then some; a burst past that is drawn
      // outright rather than queued for longer than the next poll.
      setHeld(Math.min(held + lines.length - previous.count, 400))
    } else {
      setHeld(0)
      if (!appended) setArrivedFrom(lines.length)
    }
  }
  // Whatever was held is drawn the moment the reader stops following, and
  // must not be held again when they come back to the end.
  if (!drips && held !== 0) setHeld(0)
  const pending = drips ? held : 0
  useEffect(() => {
    if (pending === 0) return
    const frame = requestAnimationFrame(() =>
      setHeld((h) => Math.max(0, h - Math.max(1, Math.ceil(h / 24)))),
    )
    return () => cancelAnimationFrame(frame)
  }, [pending])

  const drawn = pending > 0 ? lines.slice(0, lines.length - pending) : lines
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
              <Line
                key={`${previous.generation}:${line.number}`}
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

/**
 * The hue a service's steps are drawn in: the eight `--tag-*` lanes the branch
 * graph uses, picked by name, so `frontend` is one colour down the whole braid.
 */
const LANES = [
  "var(--tag-blue)",
  "var(--tag-green)",
  "var(--tag-violet)",
  "var(--tag-amber)",
  "var(--tag-cyan)",
  "var(--tag-pink)",
  "var(--tag-slate)",
]

function laneFor(service: string | undefined) {
  if (!service) return undefined
  let hash = 0
  for (const char of service) hash = (hash * 31 + char.charCodeAt(0)) >>> 0
  return LANES[hash % LANES.length]
}

const SETTLED = /^(Built|Created|Recreated|Started|Healthy|Running|Pulled|Removed|Stopped)$/

function Line({
  line,
  wrap,
  needle,
  arrived,
  loading,
}: {
  line: TranscriptLine
  wrap: boolean
  needle: string
  arrived: boolean
  loading: boolean
}) {
  if (line.kind === "blank") return <li aria-hidden className="h-3" />
  const lane = laneFor(line.service)
  const text = (value: string) => <Hit text={value} needle={needle} />
  const flow = wrap ? "break-all whitespace-pre-wrap" : "whitespace-pre"

  let content: React.ReactNode
  switch (line.kind) {
    case "trimmed":
      content = (
        <span className="text-muted-foreground italic">
          {loading ? "Reading the start of the transcript…" : "Earlier output was not kept."}
        </span>
      )
      break
    case "command":
      content = (
        <span className={cn("font-medium text-foreground", flow)}>
          <span className="text-brand select-none">$ </span>
          {text(line.text.slice(2))}
        </span>
      )
      break
    case "note":
      // The runner's own voice, in the product's face rather than the
      // commands' monospace: it is the dashboard telling the story, and the
      // eye finds the chapter headings of a thousand-line run by it.
      content = (
        <span className={cn("font-sans text-body font-medium text-foreground", flow)}>
          {text(line.text)}
        </span>
      )
      break
    case "step": {
      const rest = line.text.slice(line.text.indexOf("]") + 2)
      content = (
        <span className={flow}>
          <StepNo step={line.step} />
          <span className="font-medium" style={lane ? { color: lane } : undefined}>
            [{line.target}]
          </span>{" "}
          <span className="font-medium text-foreground">{text(rest)}</span>
        </span>
      )
      break
    }
    case "done":
      content = (
        <span>
          <StepNo step={line.step} />
          <span className="text-success">DONE</span>{" "}
          <span className="numeric text-muted-foreground">{line.time}</span>
        </span>
      )
      break
    case "cached":
      content = (
        <span className="text-muted-foreground">
          <StepNo step={line.step} />
          CACHED
        </span>
      )
      break
    case "resource": {
      const [, name] = line.text.trim().split(/\s+/)
      content = (
        <span className={flow}>
          <span className="text-muted-foreground">{line.resource} </span>
          <span style={lane ? { color: lane } : undefined}>{text(name ?? "")}</span>{" "}
          <span
            className={cn(
              "font-medium",
              SETTLED.test(line.state ?? "") ? "text-success" : "text-muted-foreground",
            )}
          >
            {line.state}
          </span>
        </span>
      )
      break
    }
    default: {
      const stepped = line.step !== undefined && line.text.startsWith(`#${line.step} `)
      let rest = stepped ? line.text.slice(line.step!.length + 2) : line.text
      if (line.time && rest.startsWith(`${line.time} `)) rest = rest.slice(line.time.length + 1)
      content = (
        <span className={flow}>
          {stepped && <StepNo step={line.step} />}
          {line.time && line.kind !== "progress" && (
            <span className="numeric mr-2 text-muted-foreground/70 select-none">{line.time}</span>
          )}
          <span
            className={cn(
              line.kind === "error" && "text-destructive",
              line.kind === "warning" && "text-warning",
              line.kind === "progress" && "text-muted-foreground",
              line.kind === "output" && "text-foreground/85",
            )}
          >
            {text(rest)}
          </span>
        </span>
      )
    }
  }

  return (
    <li
      // Long runs are a few thousand rows; the ones off screen are not laid out.
      style={{ contentVisibility: "auto", containIntrinsicSize: "auto 24px" }}
      className={cn(
        "relative flex min-w-0 gap-3 px-3 hover:bg-row-hover sm:px-4",
        line.kind === "error" && "bg-wash-danger hover:bg-wash-danger",
        line.kind === "warning" && "bg-wash-warning hover:bg-wash-warning",
        line.kind === "command" && "mt-1 border-t border-hairline pt-1",
        line.kind === "note" && "py-0.5",
        arrived && "animate-rise",
      )}
    >
      {lane && (line.kind === "step" || line.kind === "resource") && (
        // The lane, drawn once at the step's head: which image this braid of
        // lines is building is read off the edge before the words.
        <span
          aria-hidden
          className="absolute inset-y-1 left-0 w-0.5 rounded-sm"
          style={{ background: lane }}
        />
      )}
      <span
        aria-hidden
        className="numeric w-9 shrink-0 text-right text-muted-foreground/60 select-none"
      >
        {line.number}
      </span>
      <span className="min-w-0 flex-1">{content}</span>
    </li>
  )
}

function StepNo({ step }: { step?: string }) {
  if (!step) return null
  return (
    <span className="numeric mr-2 inline-block min-w-7 text-muted-foreground/70 select-none">
      #{step}
    </span>
  )
}

/** A search hit, marked the way the log console marks one. */
function Hit({ text, needle }: { text: string; needle: string }) {
  if (!needle) return text
  const parts: React.ReactNode[] = []
  const lower = text.toLowerCase()
  let from = 0
  for (let at = lower.indexOf(needle); at >= 0; at = lower.indexOf(needle, from)) {
    if (at > from) parts.push(text.slice(from, at))
    parts.push(
      <mark key={at} className="rounded-sm bg-mark px-px text-foreground">
        {text.slice(at, at + needle.length)}
      </mark>,
    )
    from = at + needle.length
  }
  parts.push(text.slice(from))
  return parts
}
