"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { Copy } from "@/components/icons"
import { Pane, PaneFooter, PaneHeader } from "@/components/panel"
import { SearchInput } from "@/components/page"
import { FilterChip } from "@/components/tabs"
import { IconAction } from "@/components/icon-action"
import { Status } from "@/components/status-dot"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { humanize } from "@/components/deploy/vocabulary"
import { clock } from "@/lib/format"
import { copyText } from "@/lib/clipboard"
import { cn } from "@/lib/utils"
import type { DeploymentStep } from "@/lib/types"

export type TranscriptLine = {
  seq: number
  stepId: number
  ts: string
  stream: string
  text: string
  truncated: boolean
}

// Persisted events are chunks, not lines. Keep event identity while giving each
// displayed line a stable number, including when a chunk contains several lines.
export function transcriptRows(lines: TranscriptLine[]) {
  return lines
    .flatMap((line) => {
      // Terminal escape sequences are presentation, never markup from the build.
      const clean = line.text.replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, "").replace(/\r\n?/g, "\n")
      const parts = clean.replace(/\n$/, "").split("\n")
      return parts.map((text, index) => ({ ...line, text, id: `${line.seq}:${index}` }))
    })
    .map((line, index) => ({ ...line, number: index + 1 }))
}

/**
 * The build transcript: a Pane rather than a Panel, because it is a working
 * region with its own scrolling, not a block of content sitting in the page.
 */
export function BuildConsole({
  lines,
  steps,
  active,
  connected,
  selectedStep,
  onSelectStep,
  hidden,
}: {
  lines: TranscriptLine[]
  steps: DeploymentStep[]
  active: boolean
  connected: boolean
  selectedStep?: number
  onSelectStep: (id: number | undefined) => void
  /** Keeps the console mounted while another run view is active, so its
   * search, wrap, follow and scroll position survive switching back. */
  hidden?: boolean
}) {
  const [query, setQuery] = useState("")
  const [errorsOnly, setErrorsOnly] = useState(false)
  const [follow, setFollow] = useState(true)
  const [wrap, setWrap] = useState(true)
  const body = useRef<HTMLDivElement>(null)
  const rows = useMemo(() => transcriptRows(lines), [lines])
  const visible = useMemo(
    () =>
      rows.filter(
        (line) =>
          (!selectedStep || line.stepId === selectedStep) &&
          (!query || line.text.toLowerCase().includes(query.toLowerCase())) &&
          (!errorsOnly || /\b(error|fatal|panic|failed)\b/i.test(line.text)),
      ),
    [rows, selectedStep, query, errorsOnly],
  )
  useEffect(() => {
    if (follow && body.current) body.current.scrollTop = body.current.scrollHeight
  }, [visible, follow])

  return (
    <Pane className="h-[36rem]" hidden={hidden}>
      <PaneHeader className="flex-wrap gap-2 py-2">
        <SearchInput
          dense
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Search build logs…"
          aria-label="Search build logs"
          containerClassName="min-w-0 basis-full sm:max-w-64 sm:flex-1 sm:basis-auto"
        />
        <Select
          value={selectedStep ? String(selectedStep) : "all"}
          onValueChange={(value) => onSelectStep(value === "all" ? undefined : Number(value))}
        >
          <SelectTrigger
            // Forces a fresh element whenever the selected stage changes, so
            // `autoFocus` fires again: with the console kept permanently
            // mounted (§21), picking a stage from Details no longer mounts
            // this trigger for the first time, which is the only moment
            // `autoFocus` normally acts.
            key={selectedStep ?? "all"}
            size="sm"
            aria-label="Build log stage"
            className="w-40"
            autoFocus={selectedStep !== undefined}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All stages</SelectItem>
            {steps.map((step) => (
              <SelectItem key={step.id} value={String(step.id)}>
                {humanize(step.key)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <FilterChip selected={errorsOnly} onClick={() => setErrorsOnly(!errorsOnly)}>
          Errors
        </FilterChip>
        <FilterChip selected={wrap} onClick={() => setWrap(!wrap)}>
          Wrap
        </FilterChip>
        <FilterChip selected={follow} onClick={() => setFollow(!follow)}>
          Follow
        </FilterChip>
        <span className="ml-auto flex shrink-0 items-center gap-2">
          <Status
            tone={active ? (connected ? "running" : "warning") : "stopped"}
            label={active ? (connected ? "Live" : "Reconnecting…") : "Completed"}
            live={active && connected}
          />
          <IconAction
            label="Copy build logs"
            disabled={!visible.length}
            onClick={() =>
              copyText(
                visible.map((line) => `[${clock(line.ts)}] ${line.text}`).join("\n"),
                "Build logs copied",
              )
            }
          >
            <Copy />
          </IconAction>
        </span>
      </PaneHeader>

      <div
        ref={body}
        onScroll={(event) => {
          const el = event.currentTarget
          if (el.scrollHeight - el.scrollTop - el.clientHeight > 24 && follow) setFollow(false)
        }}
        className="min-h-0 flex-1 overflow-auto bg-surface-sunken"
      >
        {visible.length ? (
          <ol
            aria-label="Deployment transcript"
            className={cn("py-3 font-mono text-xs leading-6", !wrap && "min-w-max")}
          >
            {visible.map((line) => {
              const failed = /\b(error|fatal|panic|failed)\b/i.test(line.text)
              const warning = /\b(warn|warning)\b/i.test(line.text)
              return (
                <li
                  key={line.id}
                  className={cn(
                    "flex min-w-0 gap-3 px-3 hover:bg-row-hover sm:px-4",
                    failed && "bg-wash-danger",
                    warning && !failed && "bg-wash-warning",
                  )}
                >
                  <span
                    aria-hidden="true"
                    className="w-7 shrink-0 text-right text-muted-foreground select-none"
                  >
                    {line.number}
                  </span>
                  <time
                    dateTime={line.ts}
                    className="hidden w-20 shrink-0 text-muted-foreground select-none sm:block"
                  >
                    {clock(line.ts)}
                  </time>
                  <span
                    className={cn(
                      "min-w-0 flex-1",
                      wrap ? "break-all whitespace-pre-wrap" : "whitespace-pre",
                      failed ? "text-destructive" : warning ? "text-warning" : "text-foreground",
                    )}
                  >
                    {line.text}
                    {line.truncated && (
                      <span className="text-muted-foreground"> [output truncated]</span>
                    )}
                  </span>
                </li>
              )
            })}
          </ol>
        ) : (
          <div className="flex h-full min-h-48 items-center justify-center px-6 text-center text-body text-muted-foreground">
            {rows.length
              ? selectedStep && !query && !errorsOnly
                ? `${humanize(steps.find((step) => step.id === selectedStep)?.key ?? "This stage")} wrote nothing to the build log.`
                : "No lines match. Try another search or stage."
              : active
                ? "Waiting for build output. New lines appear here automatically."
                : "No build output was retained for this run."}
          </div>
        )}
      </div>

      <PaneFooter className="justify-between text-hint text-muted-foreground">
        <span className="numeric">
          {visible.length} of {rows.length} lines
        </span>
        <span>
          {lines.length >= 5000
            ? "Showing the latest 5,000 events"
            : follow
              ? "Following latest output"
              : "Scroll paused"}
        </span>
      </PaneFooter>
    </Pane>
  )
}
