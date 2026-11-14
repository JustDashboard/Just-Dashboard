"use client"

import { useMemo } from "react"
import Link from "next/link"
import { ArrowLeftRight, ChartActivity, Clock, ShieldOff, Users, Warning } from "@/components/icons"
import { post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { bytes, plural } from "@/lib/format"
import type {
  StreamEntry,
  StreamResult,
  StreamSessions,
  StreamTraffic,
  StreamWindow,
} from "@/lib/types"
import {
  STREAM_STATUSES,
  accessOf,
  ruleableClient,
  saveOutcome,
  streamBody,
  streamSpecOf,
  withClientRule,
} from "@/lib/streams"
import type { PollState } from "@/hooks/use-poll"
import { RankList, RankSkeleton, SplitBar, percent, type RankItem } from "@/components/rank-list"
import { useConfirm } from "@/components/confirm-dialog"
import { ChartPanel } from "@/components/metrics/chart-panel"
import type { ChartRowLike, Series } from "@/components/metrics/metric-chart"
import { TileTrend } from "@/components/metrics/sparkline"
import { HUE } from "@/components/overview/readings"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { NoReading, ReadingLine, ReadingLines } from "@/components/reading-line"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { WireMark } from "@/components/deploy/wire"
import { lengthWords, sessionsWords, streamHref } from "@/components/proxy/stream-facts"

/**
 * What a stream carried, and who is on it now: its page's Overview, Clients
 * and Connected now.
 *
 * A raw forward has no request line, no path and no user agent — only who
 * connected, what nginx did with them, how long they stayed and how much
 * crossed. That is what the stream's own log holds, one line per finished
 * session, and it is enough to answer the questions an open port raises:
 * is anyone using this, who is being turned away, and which address is
 * hammering it. The busiest addresses carry a Deny (or an Allow, for one
 * the list turns away) that saves the stream with that rule first.
 *
 * It was a sheet over the list with two rows of four tiles and the session
 * lengths headed p50/p90/p99. The figures are reading lines now, each with its
 * window's shape at its end, and the lengths are said in words.
 */

/** Module constant: ChartPanel is memoised on its props. */
const SESSION_SERIES: Series[] = [
  { key: "sessions", label: "Sessions", color: "var(--chart-1)", kind: "area" },
  { key: "denied", label: "Denied", color: "var(--warning)", kind: "line" },
  { key: "failed", label: "Not forwarded", color: "var(--destructive)", kind: "line" },
]

export const WINDOWS: { value: StreamWindow; label: string; words: string }[] = [
  { value: "1h", label: "Hour", words: "in the last hour" },
  { value: "24h", label: "Day", words: "in the last day" },
  { value: "7d", label: "Week", words: "in the last week" },
]

const count = (value: number) => Math.round(value).toLocaleString()

/** The window's read, or nothing while the one on screen is another stream's or window's. */
export function windowRead(
  traffic: PollState<StreamTraffic>,
  name: string,
  span: StreamWindow,
): StreamTraffic | undefined {
  const data = traffic.data
  return data && data.name === name && data.window === span ? data : undefined
}

/** Why the figures below are empty, or only a floor. */
export function LogState({
  traffic,
  settingsHref,
}: {
  traffic: StreamTraffic
  /** Where Log connections is switched on, for an account that may. */
  settingsHref?: string
}) {
  if (traffic.unreadable) {
    return (
      <Notice tone="warning" icon={Warning} title="The log could not be read">
        <p className="break-words">{traffic.unreadable}</p>
      </Notice>
    )
  }
  if (!traffic.logging) {
    return (
      <Notice title="This stream does not log its sessions">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <span>
            Turn on <span className="font-medium">Log connections</span> in its settings, and nginx
            writes one line per session to <code className="font-mono">{traffic.path}</code>.
            {traffic.sessions > 0 && " What is below is from when it did."}
          </span>
          {settingsHref && (
            <Button size="xs" variant="outline" asChild>
              <Link href={settingsHref}>Settings</Link>
            </Button>
          )}
        </div>
      </Notice>
    )
  }
  if (!traffic.complete) {
    return (
      <Notice
        tone="warning"
        icon={Warning}
        title="Only the most recent part of the window was read"
      >
        The log is larger than one reading takes, or older lines are compressed, so every figure is
        a floor
        {traffic.oldest ? `, counted from ${new Date(traffic.oldest * 1000).toLocaleString()}` : ""}
        .
      </Notice>
    )
  }
  return null
}

/** An outcome's colour on the strip: forwarded, turned away, or not forwarded. */
function outcomeHue(code: string) {
  if (code === "200") return "var(--success)"
  return code === "403" || code === "400" ? "var(--warning)" : "var(--destructive)"
}

/**
 * The window as five lines — sessions, turned away, not forwarded, what
 * crossed, and how long sessions lasted in words — each one height whether
 * its reading has landed or not, with the window's shape at its end.
 */
export function StreamReadings({
  name,
  traffic,
  span,
  error,
}: {
  name: string
  traffic: StreamTraffic | undefined
  span: StreamWindow
  error?: Error
}) {
  const words = WINDOWS.find((w) => w.value === span)?.words ?? ""
  const waiting = <NoReading>{error ? "Could not read the log" : "Reading the log…"}</NoReading>
  const series = (key: "sessions" | "denied" | "failed") =>
    traffic?.buckets.map((b) => b[key]) ?? []
  const share = (n: number) =>
    traffic && traffic.sessions > 0 ? `${percent(n / traffic.sessions)} of sessions` : words
  const length = traffic && traffic.sessions > 0 ? lengthWords(traffic.duration) : undefined
  return (
    <ReadingLines aria-label={`What ${name} carried`} className="xl:grid-cols-1 2xl:grid-cols-2">
      <ReadingLine
        mark={
          <WireMark size="md" hue={HUE.cpu}>
            <ChartActivity aria-hidden />
          </WireMark>
        }
        name="Sessions"
        hint={traffic ? words : undefined}
        trend={
          traffic && (
            <TileTrend values={series("sessions")} label={`Sessions ${words}`} color={HUE.cpu} />
          )
        }
      >
        {traffic ? sessionsWords(traffic) : waiting}
      </ReadingLine>
      <ReadingLine
        mark={
          <WireMark
            size="md"
            tone={traffic && traffic.denied > 0 ? "warning" : undefined}
            hue={traffic && traffic.denied > 0 ? undefined : HUE.load}
          >
            <ShieldOff aria-hidden />
          </WireMark>
        }
        name="Turned away"
        hint={traffic ? `by the access list · ${share(traffic.denied)}` : undefined}
        trend={
          traffic && (
            <TileTrend values={series("denied")} label={`Denied ${words}`} color={HUE.load} />
          )
        }
      >
        {traffic
          ? traffic.denied > 0
            ? `${count(traffic.denied)} denied`
            : "None denied"
          : waiting}
      </ReadingLine>
      <ReadingLine
        mark={
          <WireMark
            size="md"
            tone={traffic && traffic.failed > 0 ? "danger" : undefined}
            hue={traffic && traffic.failed > 0 ? undefined : HUE.load}
          >
            <Warning aria-hidden />
          </WireMark>
        }
        name="Not forwarded"
        hint={traffic ? (traffic.failed > 0 ? "no server accepted, or a limit" : words) : undefined}
        trend={
          traffic && (
            <TileTrend
              values={series("failed")}
              label={`Not forwarded ${words}`}
              color={HUE.load}
            />
          )
        }
      >
        {traffic
          ? traffic.failed > 0
            ? `${count(traffic.failed)} not forwarded`
            : "Every session forwarded"
          : waiting}
      </ReadingLine>
      <ReadingLine
        mark={
          <WireMark size="md" hue={HUE.net}>
            <ArrowLeftRight aria-hidden />
          </WireMark>
        }
        name="Transferred"
        hint={
          traffic
            ? `${words} · ${bytes(traffic.bytesIn)} in · ${bytes(traffic.bytesOut)} out`
            : undefined
        }
        trend={
          traffic && (
            <TileTrend
              values={traffic.buckets.map((b) => b.bytes)}
              label={`Bytes ${words}`}
              color={HUE.net}
            />
          )
        }
      >
        {traffic ? bytes(traffic.bytesIn + traffic.bytesOut) : waiting}
      </ReadingLine>
      <ReadingLine
        mark={
          <WireMark size="md" hue={HUE.disk}>
            <Clock aria-hidden />
          </WireMark>
        }
        name="Session length"
        hint={length?.hint}
      >
        {length ? (
          <span className="first-letter:uppercase">{length.reading}</span>
        ) : traffic ? (
          <NoReading>No session ended {words}</NoReading>
        ) : (
          waiting
        )}
      </ReadingLine>
      <ReadingLine
        href={streamHref(name, "clients")}
        label="Busiest clients"
        mark={
          <WireMark size="md" hue={HUE.mem}>
            <Users aria-hidden />
          </WireMark>
        }
        name="Clients"
        hint={
          traffic?.clients[0]
            ? `busiest ${traffic.clients[0].address} · ${plural(traffic.clients[0].sessions, "session")}`
            : undefined
        }
      >
        {traffic
          ? traffic.clients.length > 0
            ? plural(traffic.clients.length, "address", "addresses")
            : "Nobody connected"
          : waiting}
      </ReadingLine>
    </ReadingLines>
  )
}

/** The window's sessions over time, and how each one ended. */
export function StreamTimeline({ traffic }: { traffic: StreamTraffic | undefined }) {
  const rows: ChartRowLike[] = useMemo(
    () =>
      (traffic?.buckets ?? []).map((b) => ({
        ts: b.start * 1000,
        sessions: b.sessions,
        denied: b.denied,
        failed: b.failed,
      })),
    [traffic?.buckets],
  )
  // Every session ended one way, so the outcomes are parts of one whole: one
  // strip in their colours, and the list under it is the strip's legend.
  const statuses: (RankItem & { count: number; swatch: string })[] = useMemo(() => {
    const entries = Object.entries(traffic?.statuses ?? {}).sort((a, b) => b[1] - a[1])
    const whole = entries.reduce((sum, [, n]) => sum + n, 0)
    return entries.map(([code, n]) => ({
      key: code,
      label: code,
      hint: STREAM_STATUSES[code] ?? "",
      value: count(n),
      count: n,
      share: whole > 0 ? n / whole : undefined,
      swatch: outcomeHue(code),
    }))
  }, [traffic?.statuses])

  return (
    <div className="grid min-w-0 gap-x-10 gap-y-8 xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
      <ChartPanel
        plain
        title="Sessions"
        rows={rows}
        series={SESSION_SERIES}
        height={160}
        format={count}
        note={traffic ? "No session ended in this window." : "Reading the log…"}
      />
      <Panel plain>
        <PanelHeader title="How sessions ended" />
        <PanelBody flush>
          {!traffic ? (
            <RankSkeleton rows={3} className="mt-2" />
          ) : statuses.length === 0 ? (
            <EmptyNote className="py-6">No session ended in this window.</EmptyNote>
          ) : (
            <>
              <SplitBar
                className="mt-2 mb-1.5"
                parts={statuses.map((status) => ({
                  key: status.key,
                  count: status.count,
                  color: status.swatch,
                }))}
                label={statuses
                  .map((status) => `${status.value} ended ${status.hint || status.key}`)
                  .join(", ")}
              />
              <RankList items={statuses} />
            </>
          )}
        </PanelBody>
      </Panel>
    </div>
  )
}

/** The busiest addresses in the window, each with the rule that would turn it away or let it in. */
export function StreamClients({
  traffic,
  stream,
  admin,
  live,
  onSaved,
}: {
  traffic: StreamTraffic | undefined
  stream: StreamEntry
  admin: boolean
  /** nginx reads the stream directory, so a save reloads it. */
  live: boolean
  onSaved: () => void
}) {
  const { confirm, dialog } = useConfirm()
  // A rule is a save of the stream's form, so only a stream the form can
  // save over takes one.
  const editable = admin && !stream.error && !stream.paused && stream.unsupported.length === 0

  const addRule = (address: string, action: "allow" | "deny") => {
    let result: StreamResult | undefined
    confirm({
      title: `${action === "deny" ? "Deny" : "Allow"} ${address}`,
      confirmLabel: live ? `${action === "deny" ? "Deny" : "Allow"} and reload` : "Save",
      description: (
        <p>
          {action === "deny"
            ? `${address} is turned away from port ${stream.listen}, ahead of every other rule. Connections it has open now stay until they end.`
            : `${address} may connect to port ${stream.listen}, ahead of every other rule — including a deny that covers it.`}{" "}
          The rule goes first in the stream&rsquo;s access list, where its settings can move or
          remove it.
        </p>
      ),
      action: async () => {
        const access = withClientRule(accessOf(stream), action, address)
        result = await post<StreamResult>("/proxy/streams/", {
          spec: streamBody(streamSpecOf(stream), access),
          previous: stream.name,
          reload: live,
        })
        onSaved()
      },
      onDone: () => {
        if (!result) return
        const outcome = saveOutcome(result, live)
        notify[outcome.tone](outcome.title, { description: outcome.description })
      },
    })
  }

  // A client's share is of every session in the window, which the read counts.
  const items: RankItem[] = (traffic?.clients ?? []).map((c) => ({
    key: c.address,
    label: c.address,
    value: count(c.sessions),
    share: traffic && traffic.sessions > 0 ? c.sessions / traffic.sessions : undefined,
    hint: (
      <>
        {bytes(c.bytes)}
        {c.denied > 0 && <span className="text-warning"> · {count(c.denied)} denied</span>}
        {c.failed > 0 && (
          <span className="text-destructive"> · {count(c.failed)} not forwarded</span>
        )}
      </>
    ),
    title: `${plural(c.sessions, "session")}, last ${new Date(c.last * 1000).toLocaleString()}`,
    trailing:
      editable && ruleableClient(c.address) ? (
        <Button
          size="xs"
          variant="ghost"
          onClick={() => addRule(c.address, c.denied === c.sessions ? "allow" : "deny")}
        >
          {c.denied === c.sessions ? "Allow" : "Deny"}
        </Button>
      ) : undefined,
  }))

  return (
    <Panel plain>
      <PanelHeader
        title="Busiest clients"
        actions={
          traffic && (
            <span className="numeric text-hint text-muted-foreground">
              {plural(traffic.clients.length, "address", "addresses")}
            </span>
          )
        }
      />
      <PanelBody flush className="pt-1">
        {traffic ? (
          <RankList items={items} emptyLabel="No client connected in this window." />
        ) : (
          <RankSkeleton rows={4} className="mt-2" />
        )}
      </PanelBody>
      {dialog}
    </Panel>
  )
}

/** The TCP connections nginx holds on the stream's port now. */
export function StreamConnections({
  name,
  state,
}: {
  name: string
  state: PollState<StreamSessions>
}) {
  const { error, refresh } = state
  // A reading of the stream this page showed before is not this one's.
  const data = state.data?.name === name ? state.data : undefined
  if (error && !data) return <ErrorState error={error} onRetry={refresh} />
  if (!data) return <LoadingRows rows={3} />
  if (data.paused) {
    return <EmptyNote className="py-6">Paused: nginx is not reading this stream.</EmptyNote>
  }
  if (data.udpOnly) {
    return (
      <EmptyNote className="py-6">
        nginx answers every UDP client from its one listening socket, so there is no connection per
        client to list. The Overview counts UDP sessions as they end.
      </EmptyNote>
    )
  }
  return (
    <Panel plain>
      <PanelHeader
        title={
          data.total === 0
            ? "No TCP connection open"
            : `${plural(data.total, "TCP connection")} open`
        }
      />
      <PanelBody flush>
        {data.sessions.length === 0 ? (
          <EmptyNote className="py-6">
            Nobody is connected to port {data.listen} right now.
          </EmptyNote>
        ) : (
          <RowList>
            {data.sessions.map((s) => (
              <Row
                key={`${s.client}-${s.local}`}
                title={s.client}
                subtitle={`to ${s.local}`}
                mono
              />
            ))}
          </RowList>
        )}
      </PanelBody>
    </Panel>
  )
}
