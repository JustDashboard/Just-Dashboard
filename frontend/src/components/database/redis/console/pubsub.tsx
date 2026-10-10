"use client"

import { useEffect, useRef, useState } from "react"
import { Play, StopCircle } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { clock, plural } from "@/lib/format"
import { LANES, hueFor } from "@/lib/hue"
import { usePoll } from "@/hooks/use-poll"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { useMemoryState } from "@/lib/view-state"
import { Field, FormNote } from "@/components/form"
import { EmptyNote, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { redisPubSub, redisPublish } from "@/components/database/redis/api"
import { bytesId, bytesLabel } from "@/components/database/redis/bytes"
import { FeedList } from "@/components/database/redis/console/feed-list"
import { listenTargets } from "@/components/database/redis/console/targets"
import { ReadError } from "@/components/database/redis/read-error"
import type { RedisFeedEnd, RedisMessage } from "@/components/database/redis/types"
import type { Redis } from "@/components/database/redis/use-redis"

const MAX = 5000

type Heard = RedisMessage & { at: number }

/**
 * Pub/Sub: the channels somebody is listening on, a message to send to one,
 * and — for an administrator — the messages themselves as they are published.
 *
 * The three have three owners on the server. The channel list is a read; a
 * publish needs service control; the live feed shows every payload on the
 * channels it names, so it is an administrator's and is drawn for nobody
 * else. A feed is open for a stated time and ends on its own.
 */
export function PubSubView({ redis }: { redis: Redis }) {
  const { id, engine, canWrite, admin } = redis
  const live = admin && engine.can("pubsubLive") && Boolean(redis.server.data?.features.pubsub)
  const [filter, setFilter] = useState("")
  const channels = usePoll((signal) => redisPubSub(id, filter.trim(), signal), 5000, [id, filter])

  const [channel, setChannel] = useState("")
  const [message, setMessage] = useState("")
  const [sending, setSending] = useState(false)
  const [sent, setSent] = useState<{ ok: boolean; text: string } | null>(null)

  const publish = async () => {
    setSending(true)
    setSent(null)
    try {
      const answer = await redisPublish(id, channel, message)
      setSent({
        ok: true,
        text:
          answer.receivers === 0
            ? "Published. Nobody was listening on that channel."
            : `Published to ${answer.receivers.toLocaleString()} ${answer.receivers === 1 ? "subscriber" : "subscribers"}.`,
      })
    } catch (err) {
      setSent({ ok: false, text: errorMessage(err) })
    } finally {
      setSending(false)
    }
  }

  return (
    <div className="flex min-h-0 min-w-0 flex-1 max-lg:flex-col max-lg:overflow-auto">
      <div className="flex min-w-0 shrink-0 flex-col border-hairline max-lg:border-b lg:w-80 lg:border-r">
        {/* As tall as the feed's strip beside it, so one rule runs under both. */}
        <div className="flex h-11 shrink-0 items-center gap-2 border-b border-hairline px-3">
          <span className="text-xs font-medium">Channels with subscribers</span>
          <span className="min-w-0 flex-1" />
          {channels.data && (
            <span className="numeric text-hint text-muted-foreground">
              {channels.data.channels.length.toLocaleString()}
            </span>
          )}
        </div>
        <div className="shrink-0 border-b border-hairline p-2">
          <Input
            aria-label="Channel pattern"
            placeholder="Pattern — orders.*"
            value={filter}
            spellCheck={false}
            autoComplete="off"
            onChange={(event) => setFilter(event.target.value)}
            className="h-7 px-2 font-mono text-xs sm:h-7 sm:text-xs"
          />
        </div>
        <div className="min-h-24 overflow-auto lg:min-h-0 lg:flex-1">
          {channels.error && !channels.data ? (
            <ReadError error={channels.error} onRetry={channels.refresh} className="m-2" />
          ) : !channels.data ? (
            <LoadingRows rows={3} className="p-2" />
          ) : channels.data.channels.length === 0 ? (
            <EmptyNote className="px-4 text-pretty">
              No channel has a subscriber right now
              {channels.data.patterns > 0
                ? `; ${channels.data.patterns.toLocaleString()} pattern subscriptions are open.`
                : "."}
            </EmptyNote>
          ) : (
            <ul className="py-1">
              {channels.data.channels.map((entry) => {
                const name = bytesLabel(entry.name)
                return (
                  <li key={bytesId(entry.name)}>
                    <button
                      type="button"
                      title={`Publish to ${name}`}
                      disabled={!canWrite || typeof entry.name !== "string"}
                      onClick={() => setChannel(name)}
                      className="flex h-7 w-full min-w-0 items-center gap-2 px-3 text-left text-xs focus-ring-inset transition-colors enabled:hover:bg-row-hover"
                    >
                      <span className="min-w-0 flex-1 truncate font-mono">{name}</span>
                      <span className="numeric shrink-0 text-hint text-muted-foreground">
                        {entry.subscribers.toLocaleString()}
                      </span>
                    </button>
                  </li>
                )
              })}
              {channels.data.channelsOmitted ? (
                <li className="px-3 py-1 text-hint text-muted-foreground">
                  {channels.data.channelsOmitted.toLocaleString()} more — narrow the pattern
                </li>
              ) : null}
            </ul>
          )}
        </div>
        {canWrite && (
          <form
            className="shrink-0 space-y-3 border-t border-hairline p-3"
            onSubmit={(event) => {
              event.preventDefault()
              void publish()
            }}
          >
            <Field label="Publish to" htmlFor="redis-publish-channel">
              <Input
                id="redis-publish-channel"
                value={channel}
                spellCheck={false}
                autoComplete="off"
                placeholder="orders.created"
                className="font-mono"
                onChange={(event) => setChannel(event.target.value)}
              />
            </Field>
            <Field label="Message" htmlFor="redis-publish-message">
              <Textarea
                id="redis-publish-message"
                value={message}
                spellCheck={false}
                className="max-h-40 min-h-16 font-mono text-xs sm:text-xs"
                onChange={(event) => setMessage(event.target.value)}
              />
            </Field>
            <div className="flex items-center gap-3">
              <Button
                type="submit"
                size="sm"
                variant="outline"
                pending={sending}
                disabled={!channel}
              >
                Publish
              </Button>
              <div aria-live="polite" className="min-w-0 flex-1">
                {sent && <FormNote tone={sent.ok ? "default" : "danger"}>{sent.text}</FormNote>}
              </div>
            </div>
          </form>
        )}
      </div>
      {live ? (
        <LiveFeed id={id} />
      ) : (
        <div className="flex min-h-40 min-w-0 flex-1 items-center justify-center p-6">
          <EmptyNote className="max-w-md text-pretty">
            The messages themselves are shown to administrators only: a feed carries every payload
            published on the channels it listens to.
          </EmptyNote>
        </div>
      )}
    </div>
  )
}

/**
 * The messages published while the feed is open, on the channels it was
 * pointed at.
 *
 * What was heard, and what was being listened to, are kept for the tab like
 * the console's transcript: looking at the monitor and coming back finds the
 * messages where they were. The feed itself is not kept open behind another
 * view — it closes when this one is left, and says so, with the press that
 * opens it again. Payloads are held in memory only, never written down.
 */
function LiveFeed({ id }: { id: number }) {
  const [targets, setTargets] = useMemoryState(`databases.${id}.redis.pubsub.targets`, "*")
  const [heard, setHeard] = useMemoryState<Heard[]>(`databases.${id}.redis.pubsub.heard`, [])
  /** The feed was open when this view was last left. */
  const [left, setLeft] = useMemoryState(`databases.${id}.redis.pubsub.left`, false)
  const [running, setRunning] = useState(false)
  const [ended, setEnded] = useState<RedisFeedEnd | null>(null)
  const [opened, setOpened] = useState(false)
  const answered = useRef(false)
  const [failed, setFailed] = useState("")
  const listen = listenTargets(targets)
  const valid = listen.channel.length + listen.pattern.length > 0

  const live = useRef(false)
  useEffect(() => {
    live.current = running
  }, [running])
  useEffect(
    () => () => {
      if (live.current) setLeft(true)
    },
    [setLeft],
  )

  useSocket(`/databases/${id}/redis/subscribe`, {
    enabled: running,
    query: { channel: listen.channel, pattern: listen.pattern, max: MAX },
    onMessage: (frame: Envelope) => {
      answered.current = true
      if (frame.type === "meta") setOpened(true)
      if (frame.type === "messages") {
        const batch = (frame.data as RedisMessage[]).map((entry) => ({ ...entry, at: frame.ts }))
        setHeard((held) => [...held, ...batch].slice(-MAX))
      }
      if (frame.type === "end") {
        setEnded(frame.data as RedisFeedEnd)
        setRunning(false)
      }
    },
    onClose: () => {
      if (!answered.current) {
        setFailed("The feed could not be opened.")
        setRunning(false)
      }
    },
  })

  const start = () => {
    setEnded(null)
    setOpened(false)
    answered.current = false
    setFailed("")
    setLeft(false)
    setRunning(true)
  }

  return (
    <div className="flex min-h-80 min-w-0 flex-1 flex-col lg:min-h-0">
      <form
        className="flex min-h-11 shrink-0 flex-wrap items-center gap-x-3 gap-y-2 border-b border-hairline px-3 py-1"
        onSubmit={(event) => {
          event.preventDefault()
          if (valid && !running) start()
        }}
      >
        {running ? (
          <Status tone="running" live label={opened ? "Listening" : "Opening the feed"} />
        ) : ended ? (
          <Status
            tone={ended.reason === "error" ? "warning" : "stopped"}
            label={
              ended.reason === "duration"
                ? "The feed reached its time"
                : ended.reason === "limit"
                  ? "The feed reached its limit"
                  : "The feed ended early"
            }
          />
        ) : (
          <Status
            tone="stopped"
            label={left ? "Stopped when you left this view" : "Not listening"}
          />
        )}
        <span className="numeric text-hint text-muted-foreground">
          {plural(heard.length, "message")}
          {ended && ended.dropped > 0 ? ` · ${ended.dropped.toLocaleString()} dropped` : ""}
        </span>
        <span className="min-w-0 flex-1" />
        <Input
          aria-label="Channels and patterns to listen to"
          placeholder="orders.* news"
          value={targets}
          disabled={running}
          spellCheck={false}
          autoComplete="off"
          onChange={(event) => setTargets(event.target.value)}
          className="h-7 w-48 px-2 font-mono text-xs sm:h-7 sm:text-xs"
        />
        {heard.length > 0 && !running && (
          <Button type="button" size="sm" variant="ghost" onClick={() => setHeard([])}>
            Clear
          </Button>
        )}
        {running ? (
          <Button type="button" size="sm" variant="outline" onClick={() => setRunning(false)}>
            <StopCircle />
            Stop
          </Button>
        ) : (
          <Button type="submit" size="sm" disabled={!valid}>
            <Play />
            {left || ended ? "Listen again" : "Listen"}
          </Button>
        )}
      </form>
      {(failed || ended?.error) && (
        <p
          role="alert"
          className="shrink-0 border-b border-hairline px-3 py-2 text-xs text-destructive"
        >
          {failed || ended?.error}
        </p>
      )}
      {heard.length === 0 ? (
        <div className="flex min-h-0 flex-1 items-center justify-center bg-surface-sunken p-6">
          <EmptyNote className="max-w-md text-pretty">
            {running
              ? "Listening. A message appears here the moment it is published."
              : "Name the channels to listen to — a word is a channel, a glob such as orders.* is a pattern, and * is everything — and press Listen."}
          </EmptyNote>
        </div>
      ) : (
        <FeedList
          label="Published messages"
          rows={heard}
          row={(entry) => (
            <>
              <span className="shrink-0 text-muted-foreground">
                {clock(new Date(entry.at).toISOString())}
              </span>
              <span
                className="max-w-56 shrink-0 truncate"
                style={{ color: hueFor(bytesLabel(entry.channel), LANES) }}
              >
                {bytesLabel(entry.channel)}
              </span>
              <span className="min-w-0 truncate">{bytesLabel(entry.payload)}</span>
              {entry.truncated && <Tag>first 64 KiB</Tag>}
            </>
          )}
        />
      )}
    </div>
  )
}
