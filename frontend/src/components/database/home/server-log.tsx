"use client"

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react"
import { get } from "@/lib/api"
import { LEVEL_MARK, type LogLevel } from "@/lib/log-filter"
import { formatLogTime } from "@/lib/log-view"
import { plural, timestamp } from "@/lib/format"
import type { DbLogSource, DbLogSources, LogLine } from "@/lib/types"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { LEVEL_WORD, LogText } from "@/components/logs/log-text"
import { resumeFrom, unseen, type Resume } from "@/components/logs/resume"
import { Well } from "@/components/panel"
import { Status } from "@/components/status-dot"
import {
  Block,
  BlockLink,
  CouldNotRead,
  Quiet,
  RowsSkeleton,
} from "@/components/database/home/blocks"
import { useDatabase } from "@/components/database/shell/database-context"

/** How many of the newest lines the tail asks for, and keeps. */
const KEPT = 200

/**
 * The server's own log, followed live under the readings: the lines it is
 * writing now, each in the colours the Logs page draws it in — the level as
 * its word, an address, a number, a failed password found without reading.
 *
 * The source is the one the Logs page opens on (`GET /databases/{id}/logs/
 * sources`): a database in a container is its container's output, one on the
 * machine the file its process writes. A stopped server keeps its source, so
 * its last lines — usually the ones that say why — are here too. A server on
 * another machine, or a file, has no log here, and the block says so in the
 * server's words.
 *
 * The tail is the log socket the Logs page uses, so the dot beside it is the
 * truth about that socket (§11): Live while it is open.
 */
export function ServerLog() {
  const { id, engine, href } = useDatabase()
  const found = usePoll(
    (signal) => get<DbLogSources>(`/databases/${id}/logs/sources`, undefined, signal),
    60_000,
    [id],
  )
  const sources = found.data?.sources ?? []
  const source = sources.find((one) => one.primary) ?? sources[0]
  const link = engine.has("logs") ? <BlockLink href={href("logs")}>Logs</BlockLink> : undefined

  if (found.error && !found.data) {
    return (
      <Block title="Server log" actions={link}>
        <CouldNotRead
          what="where this server writes its log"
          error={found.error}
          onRetry={found.refresh}
        />
      </Block>
    )
  }
  if (!found.data) {
    return (
      <Block title="Server log" actions={link}>
        <RowsSkeleton rows={4} mark={false} />
      </Block>
    )
  }
  if (!source) {
    return (
      <Block title="Server log" actions={link}>
        <Quiet>
          {found.data.reason ??
            (found.data.refused?.length
              ? `${found.data.refused[0].path} is ${found.data.refused[0].reason}.`
              : "This server's log is not on this machine.")}
        </Quiet>
      </Block>
    )
  }
  // Keyed on the source: a new source is a new tail, never the old one's lines.
  return <Tail key={source.id} source={source} link={link} />
}

type Held = LogLine & { seq: number }

function Tail({ source, link }: { source: DbLogSource; link?: React.ReactNode }) {
  const [lines, setLines] = useState<Held[]>([])
  const [ended, setEnded] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)
  const seq = useRef(0)
  const onScreen = useRef<Held[]>([])
  const resume = useRef<Resume | null>(null)

  useEffect(() => {
    onScreen.current = lines
  }, [lines])

  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.error) {
      setFailed(envelope.error)
      return
    }
    if (envelope.type === "meta") {
      // A new connection opens with the server's last lines again: keep what
      // is drawn and take only what is newer, as the Logs page does.
      resume.current = resumeFrom(onScreen.current, KEPT)
      return
    }
    if (envelope.type === "eof") {
      setEnded(true)
      return
    }
    if (envelope.type !== "logs" || !Array.isArray(envelope.data)) return
    let batch = envelope.data as LogLine[]
    if (resume.current) {
      const next = unseen(batch, resume.current)
      batch = next.lines
      if (next.caughtUp) resume.current = null
      if (batch.length === 0) return
    }
    const held = batch.map((line) => ({ ...line, seq: seq.current++ }))
    setFailed(null)
    setLines((before) => [...before, ...held].slice(-KEPT))
  }, [])

  const { state } = useSocket("/logs/stream", {
    onMessage,
    query: { source: source.id, lines: KEPT },
    enabled: !ended,
  })

  const errors = lines.filter((line) => line.level === "error" || line.level === "critical").length
  const warnings = lines.filter((line) => line.level === "warn").length
  const live = state === "open" && !ended

  return (
    <Block
      title="Server log"
      actions={
        <>
          {errors > 0 && <Status tone="danger" label={plural(errors, "error")} />}
          {warnings > 0 && <Status tone="warning" label={plural(warnings, "warning")} />}
          <Status
            tone={live ? "running" : ended ? "stopped" : "notice"}
            live={live}
            label={
              live ? "Live" : ended ? "Ended" : lines.length > 0 ? "Reconnecting…" : "Connecting…"
            }
          />
          {link}
        </>
      }
    >
      <div className="space-y-2">
        <p className="flex min-w-0 items-center gap-2 text-hint text-muted-foreground">
          <span className="truncate font-mono" title={source.path ?? source.label}>
            {source.path ?? source.label}
          </span>
          {source.detail && <span className="truncate max-md:hidden">· {source.detail}</span>}
        </p>
        {failed && lines.length === 0 ? (
          <Quiet className="wrap-anywhere">The log could not be followed: {failed}</Quiet>
        ) : (
          <Lines lines={lines} waiting={!ended && lines.length === 0} />
        )}
      </div>
    </Block>
  )
}

/**
 * The lines, newest at the foot as a terminal draws them, held to the foot
 * while the reader is there — and left where they are once the reader has
 * scrolled up to read one.
 */
function Lines({ lines, waiting }: { lines: Held[]; waiting: boolean }) {
  const box = useRef<HTMLDivElement>(null)
  const pinned = useRef(true)

  useLayoutEffect(() => {
    const element = box.current
    if (element && pinned.current) element.scrollTop = element.scrollHeight
  }, [lines])

  useEffect(() => {
    const element = box.current
    if (!element) return
    const onScroll = () => {
      pinned.current = element.scrollHeight - element.scrollTop - element.clientHeight < 24
    }
    element.addEventListener("scroll", onScroll, { passive: true })
    return () => element.removeEventListener("scroll", onScroll)
  }, [])

  return (
    <Well
      ref={box}
      role="log"
      aria-label="Server log"
      tabIndex={0}
      className="max-h-80 px-0 py-2 focus-ring-inset"
    >
      {lines.length === 0 ? (
        <p className="px-3 font-sans text-hint text-muted-foreground">
          {waiting ? "Waiting for the server's first lines…" : "The server has written nothing."}
        </p>
      ) : (
        <ol>
          {lines.map((line) => {
            const level = (line.level || "unknown") as LogLevel
            const bad = level === "error" || level === "critical"
            return (
              <li
                key={line.seq}
                className={cn(
                  "flex animate-rise gap-3 px-3 py-px whitespace-pre-wrap",
                  bad && "bg-wash-danger",
                  level === "warn" && "bg-wash-warning",
                )}
              >
                <span
                  className="w-16 shrink-0 text-muted-foreground/60 select-none"
                  title={line.timestamp ? timestamp(line.timestamp) : undefined}
                >
                  {line.timestamp ? formatLogTime(line.timestamp, "clock") : ""}
                </span>
                <span className={cn("w-8 shrink-0 uppercase select-none", LEVEL_WORD[level])}>
                  {LEVEL_MARK[level]}
                </span>
                <LogText
                  text={line.message ?? line.text}
                  skipTime={Boolean(line.timestamp)}
                  className="min-w-0 flex-1 break-words"
                />
              </li>
            )
          })}
        </ol>
      )}
    </Well>
  )
}
