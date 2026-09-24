"use client"

import { useEffect, useState } from "react"
import { useReducedMotion } from "motion/react"
import { LogText } from "@/components/logs/log-text"
import { LANES, hueFor } from "@/lib/hue"
import { hitRanges, type TranscriptLine } from "@/lib/transcript"
import { cn } from "@/lib/utils"

/**
 * A transcript's lines, drawn once for every console that shows one: the
 * dashboard's own restarts and upgrades (`RunTranscript`) and a deployment's
 * build (`BuildConsole`). Each of `lib/transcript.ts`'s shapes is drawn as
 * itself, the step a line belongs to is coloured by the service it is
 * building, and a failure is washed where it happened.
 */

/**
 * The drip: a poll or a burst of events lands forty lines at once, and forty
 * lines appearing in a frame is a page that jumped. While `drips` holds — the
 * run is live and the reader is at the end, not searching or filtering — new
 * lines are let out a few a frame, the whole batch well inside the poll that
 * brought it, and each line past `arrivedFrom` rises into place once (§11's
 * *arrived*). With reduced motion the lines are simply there.
 *
 * `text` is what the lines were read from. The lines count as appended only
 * when the new text starts with the old, so a switch that renumbers
 * everything — the tail replaced by the whole file — is drawn at once, and
 * `generation` moves so the caller can key its rows by it.
 */
export function useTranscriptDrip<T>(text: string, lines: T[], drips: boolean) {
  const reduced = useReducedMotion()
  const on = drips && !reduced
  // `held` is how many lines at the end are not drawn yet.
  const [previous, setPrevious] = useState({ text, count: lines.length, generation: 0 })
  const [held, setHeld] = useState(0)
  const [arrivedFrom, setArrivedFrom] = useState(lines.length)
  if (text !== previous.text) {
    const appended = text.startsWith(previous.text)
    setPrevious({
      text,
      count: lines.length,
      generation: appended ? previous.generation : previous.generation + 1,
    })
    if (appended && on) {
      // Four hundred is a screenful and then some; a burst past that is drawn
      // outright rather than queued for longer than the next poll.
      setHeld(Math.min(held + lines.length - previous.count, 400))
    } else {
      setHeld(0)
      if (!appended) setArrivedFrom(lines.length)
    }
  }
  // Whatever was held is drawn the moment the reader stops following, and
  // must not be held again when they come back to the end. Nor does it rise
  // then: lines drawn while paused did not arrive in front of the reader.
  if (!on && held !== 0) setHeld(0)
  if (!on && arrivedFrom !== lines.length) setArrivedFrom(lines.length)
  const pending = on ? held : 0
  useEffect(() => {
    if (pending === 0) return
    const frame = requestAnimationFrame(() =>
      setHeld((h) => Math.max(0, h - Math.max(1, Math.ceil(h / 24)))),
    )
    return () => cancelAnimationFrame(frame)
  }, [pending])

  return {
    drawn: pending > 0 ? lines.slice(0, lines.length - pending) : lines,
    generation: previous.generation,
    arrivedFrom,
  }
}

/**
 * The hue a service's steps are drawn in, picked by name so `frontend` is one
 * colour down the whole braid — and never red or amber, which on this console
 * mean a line that failed or warned.
 */
function laneFor(service: string | undefined) {
  return service ? hueFor(service, LANES) : undefined
}

const SETTLED = /^(Built|Created|Recreated|Started|Healthy|Running|Pulled|Removed|Stopped)$/

/** One line of a transcript, as an `<li>` of the console's `<ol>`. */
export function TranscriptRow({
  line,
  wrap,
  needle,
  arrived,
  loading,
  tokens,
  stamp,
  trailing,
}: {
  line: TranscriptLine
  wrap: boolean
  /** The search, lowercased; every hit on the line is marked. */
  needle: string
  /** Not here a moment ago: rises into place once. */
  arrived?: boolean
  /** The trimmed marker while the whole file is being read. */
  loading?: boolean
  /**
   * Draw a plain output line in the log console's token colours (`LogText`),
   * for a build whose output is as often a server's request log as a
   * compiler's: `GET /health 200` then reads as it does on the Logs page.
   */
  tokens?: boolean
  /** A cell between the number and the line, for lines that carry their own clock. */
  stamp?: React.ReactNode
  /** After the line's own text — the engine's note that it cut a long chunk short. */
  trailing?: React.ReactNode
}) {
  if (line.kind === "blank") return <li aria-hidden className="h-3" />
  const lane = laneFor(line.service)
  const text = (value: string) => <Hit text={value} needle={needle} />
  // `wrap-anywhere` breaks a token only when it cannot fit a line on its own;
  // `break-all` split `--frozen-lockfile` and `events` mid-word on a phone.
  const flow = wrap ? "wrap-anywhere whitespace-pre-wrap" : "whitespace-pre"

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
          {tokens && line.kind === "output" ? (
            <LogText text={rest} hits={hitRanges(rest, needle)} className="text-foreground/85" />
          ) : (
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
          )}
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
      {stamp}
      <span className="min-w-0 flex-1">
        {content}
        {trailing}
      </span>
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
  let from = 0
  for (const [at, end] of hitRanges(text, needle)) {
    if (at > from) parts.push(text.slice(from, at))
    parts.push(
      <mark key={at} className="rounded-sm bg-mark px-px text-foreground">
        {text.slice(at, end)}
      </mark>,
    )
    from = end
  }
  parts.push(text.slice(from))
  return parts
}
