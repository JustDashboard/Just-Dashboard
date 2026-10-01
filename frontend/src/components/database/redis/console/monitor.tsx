"use client"

import { useMemo, useRef, useState } from "react"
import { Play, StopCircle, Warning } from "@/components/icons"
import { duration, plural } from "@/lib/format"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { BarList } from "@/components/bar-list"
import { Segments } from "@/components/deploy/settings/segments"
import { SearchInput } from "@/components/page"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { FeedList } from "@/components/database/redis/console/feed-list"
import type { RedisFeedEnd, RedisMonitorEvent } from "@/components/database/redis/types"
import type { Redis } from "@/components/database/redis/use-redis"

/** The most commands one run keeps on screen. The server stops the feed at the same count. */
const MAX = 5000

const SPANS = [
  { value: "10", label: "10 s" },
  { value: "30", label: "30 s" },
  { value: "60", label: "1 min" },
  { value: "300", label: "5 min" },
]

const ENDED: Record<RedisFeedEnd["reason"], string> = {
  duration: "The run reached its time",
  limit: "The run reached its limit of commands",
  error: "The run ended early",
}

/** A moment of the server's clock, to the millisecond. */
function at(seconds: number): string {
  const date = new Date(seconds * 1000)
  return `${date.toLocaleTimeString(undefined, { hour12: false })}.${String(date.getMilliseconds()).padStart(3, "0")}`
}

/**
 * Every command the server runs, as it runs it.
 *
 * MONITOR is a tap on the whole server: it shows what every client sends,
 * arguments and all, and costs the server throughput for as long as it is
 * attached. So a run is asked for, lasts a stated time, and ends on its own;
 * nothing here starts one by itself or starts it again.
 */
export function MonitorView({ redis }: { redis: Redis }) {
  const { id } = redis
  const [seconds, setSeconds] = useState("30")
  const [running, setRunning] = useState(false)
  const [events, setEvents] = useState<RedisMonitorEvent[]>([])
  const [ended, setEnded] = useState<RedisFeedEnd | null>(null)
  const [opened, setOpened] = useState(false)
  // Read by the close handler, which can run before the render that follows
  // the first frame.
  const answered = useRef(false)
  const [failed, setFailed] = useState("")
  const [filter, setFilter] = useState("")

  useSocket(`/databases/${id}/redis/monitor`, {
    enabled: running,
    query: { seconds, max: MAX },
    onMessage: (frame: Envelope) => {
      answered.current = true
      if (frame.type === "meta") setOpened(true)
      if (frame.type === "commands") {
        setEvents((held) => [...held, ...(frame.data as RedisMonitorEvent[])].slice(-MAX))
      }
      if (frame.type === "end") {
        setEnded(frame.data as RedisFeedEnd)
        setRunning(false)
      }
    },
    onClose: () => {
      // A socket that closes before it said anything was refused, and the
      // hook would otherwise keep knocking.
      if (!answered.current) {
        setFailed(
          "The feed could not be opened. The server may not allow MONITOR for this account.",
        )
        setRunning(false)
      }
    },
  })

  const start = () => {
    setEvents([])
    setEnded(null)
    setOpened(false)
    answered.current = false
    setFailed("")
    setRunning(true)
  }

  const wanted = filter.trim().toLowerCase()
  const shown = useMemo(
    () =>
      wanted
        ? events.filter((event) =>
            `${event.command} ${event.args.join(" ")} ${event.client}`
              .toLowerCase()
              .includes(wanted),
          )
        : events,
    [events, wanted],
  )
  const top = useMemo(() => {
    const counts = new Map<string, number>()
    for (const event of events) counts.set(event.command, (counts.get(event.command) ?? 0) + 1)
    const ranked = [...counts.entries()].sort((a, b) => b[1] - a[1]).slice(0, 12)
    const most = ranked[0]?.[1] ?? 1
    return ranked.map(([command, count]) => ({
      key: command,
      label: command,
      value: count.toLocaleString(),
      share: count / most,
      onClick: () => setFilter(command),
      title: `Show only ${command}`,
    }))
  }, [events])

  const idle = !running && events.length === 0 && !ended

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-2 border-b border-hairline px-3 py-2">
        {running ? (
          <Status tone="running" live label={opened ? "Recording" : "Opening the feed"} />
        ) : ended ? (
          <Status
            tone={ended.reason === "error" ? "warning" : "stopped"}
            label={ENDED[ended.reason]}
          />
        ) : (
          <Status tone="stopped" label="Not recording" />
        )}
        <span className="numeric text-hint text-muted-foreground">
          {plural(events.length, "command")}
          {ended && ended.dropped > 0 ? ` · ${ended.dropped.toLocaleString()} dropped` : ""}
        </span>
        <span className="min-w-0 flex-1" />
        {events.length > 0 && (
          <SearchInput
            dense
            aria-label="Filter the commands"
            placeholder="Filter"
            value={filter}
            spellCheck={false}
            autoComplete="off"
            containerClassName="w-40 sm:w-48"
            onChange={(event) => setFilter(event.target.value)}
          />
        )}
        <Segments
          label="How long the run lasts"
          value={seconds}
          onChange={setSeconds}
          disabled={running}
          options={SPANS}
        />
        {running ? (
          <Button size="sm" variant="outline" onClick={() => setRunning(false)}>
            <StopCircle />
            Stop
          </Button>
        ) : (
          <Button size="sm" onClick={start}>
            <Play />
            {events.length > 0 || ended ? "Record again" : "Start recording"}
          </Button>
        )}
      </div>

      {(failed || ended?.error) && (
        <p
          role="alert"
          className="shrink-0 border-b border-hairline px-3 py-2 text-xs text-destructive"
        >
          {failed || ended?.error}
        </p>
      )}

      {idle ? (
        <div className="flex min-h-0 flex-1 items-center justify-center p-4">
          <Notice
            tone="warning"
            icon={Warning}
            title="A run shows every client's commands, with their arguments"
            className="max-w-xl"
          >
            <p>
              Session tokens and passwords being checked are among them, and the server is slower
              for as long as the run lasts. It records for {duration(Number(seconds))} and stops on
              its own, or when you stop it. The dashboard&apos;s own reads appear in it too.
            </p>
          </Notice>
        </div>
      ) : (
        <div className="flex min-h-0 flex-1 max-lg:flex-col">
          <FeedList
            label="Commands the server ran"
            rows={shown}
            row={(event) => (
              <>
                <span className="shrink-0 text-muted-foreground">{at(event.at)}</span>
                <span className="w-8 shrink-0 text-muted-foreground">db{event.db}</span>
                <span className="w-36 shrink-0 truncate text-muted-foreground">{event.client}</span>
                <span className="shrink-0 font-medium">{event.command}</span>
                <span className="min-w-0 truncate text-muted-foreground">
                  {event.args.join(" ")}
                </span>
              </>
            )}
          />
          <div className="shrink-0 overflow-auto border-hairline p-3 max-lg:max-h-48 max-lg:border-t lg:w-64 lg:border-l">
            <p className="eyebrow pb-1">Most run</p>
            <BarList items={top} emptyLabel="Nothing has run yet." />
          </div>
        </div>
      )}
    </div>
  )
}
