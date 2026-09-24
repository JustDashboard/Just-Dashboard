"use client"

import { useCallback, useLayoutEffect, useRef, useState } from "react"
import {
  BlendMode,
  Bug,
  ChevronDoubleDown,
  ClockRewind,
  Copy,
  Filter,
  Globe,
  LockClosed,
  LockOpen,
  MoreHorizontal,
  Pause,
  Play,
  Slash,
  Terminal,
  TerminalWindow,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { bytes, clock, timestamp } from "@/lib/format"
import { copyText } from "@/lib/clipboard"
import { setLogView, useLogView } from "@/lib/log-view"
import { crawlerOf, networkOf, refererProduct } from "@/lib/clients"
import type { RequestEntry, RequestSummary } from "@/lib/types"
import {
  CLASS_EDGE,
  latency,
  latencyBracket,
  latencyShare,
  latencyTone,
  requestKey,
  requestURI,
  statusClass,
} from "@/lib/requests"
import { useArrivals } from "@/hooks/use-arrivals"
import { useMediaQuery } from "@/hooks/use-mobile"
import { ClientMark, NETWORK_GLYPH, NetworkFact } from "@/components/client-mark"
import { FactDot } from "@/components/metrics/host-identity"
import { PaneFooter } from "@/components/panel"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { VerbMenu, type Verb } from "@/components/verbs"
import {
  Address,
  ClientCell,
  MethodWord,
  RequestPath,
  StatusCode,
  clientName,
  isPrivate,
} from "@/components/deploy/request-marks"

/**
 * The requests, and the chrome that belongs to them.
 *
 * Drawn as log lines rather than as a data table, and that is the deliberate
 * choice: a request record is read the way a log is read — down the left edge
 * for the time, across for the one row that matters — and a nine-column
 * `<table>` at this density spends its width on cell padding and its
 * maintenance on breakpoint rules. The registry blocks that draw this as a
 * table also draw a filled colour chip per HTTP verb, which turns a column of
 * GETs into a column of rectangles competing with the status for attention.
 *
 * Each part of a row is coloured by the rules the host's log console colours
 * the same request by (`request-marks.tsx`): the code in its family's hue, so
 * a column of 200s scans as all green and the 500 and the 404 in it are found
 * by colour; a write in the method hue; the path in the path hue; the address
 * in the address hue, after the browser or program that sent it drawn as
 * itself. A failed request's row is washed the way the log console washes an
 * error line, and a response past a second takes amber on its figure. The
 * Colour switch beside Copy is the log console's own setting, so one switch
 * governs both consoles, and off shows every row as it was written.
 *
 * On a phone a row is two lines rather than eight squeezed columns: the code,
 * the path and the time it took, then when, how, how big and from whom under
 * the path. The shape is chosen once (`useMediaQuery`), never drawn twice and
 * hidden (§12).
 *
 * Two things carry over from the log console next door, because they were
 * learned there and the two panes must behave the same way:
 *
 * **Following stops the moment the reader scrolls**, and resumes at the top.
 * **Pausing holds what arrives rather than dropping it**, so reading a busy
 * deployment does not cost you the requests you read past.
 *
 * While following a live tail, each row the socket brings rises once as it
 * lands (§11 *arrived*); scrolled away or paused, rows are simply drawn.
 *
 * `content-visibility` rather than a virtualiser: the browser skips layout for
 * rows outside the viewport, and the scrollbar stays honest, the expanded row
 * keeps its real height, and the browser's own find still works.
 */
export function RequestConsole({
  entries,
  summary,
  latencyKnown,
  empty,
  status,
  footer,
  leading,
  paused,
  onPausedChange,
  held = 0,
  onFilterPath,
  onFilterClient,
  onBlock,
  blocking,
  outputHref,
  onEventsAround,
}: {
  entries: RequestEntry[]
  summary: RequestSummary
  latencyKnown: boolean
  empty: React.ReactNode
  /** The footer's first words: the stream's state, or the window's summary. */
  status?: React.ReactNode
  footer?: React.ReactNode
  /** The left of the strip above the rows: the status-family chips. */
  leading?: React.ReactNode
  paused?: boolean
  onPausedChange?: (paused: boolean) => void
  held?: number
  /** Narrowing to the path under the pointer, from the row itself. */
  onFilterPath?: (path: string) => void
  /** Narrowing to the address an opened row came from. */
  onFilterClient?: (ip: string) => void
  /** Deny an address at the firewall. Absent for a role that may not. */
  onBlock?: (ip: string) => void
  /** The address a deny is in flight for, so the verb says it is working. */
  blocking?: string | null
  /** The host Logs page opened on the container's lines around this request's minute. */
  outputHref?: (entry: RequestEntry) => string | undefined
  /** The Events view scoped to this request's minute. */
  onEventsAround?: (entry: RequestEntry) => void
}) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const [following, setFollowing] = useState(true)
  const [open, setOpen] = useState<string | null>(null)
  const { highlight } = useLogView()
  const plain = !highlight
  const wide = useMediaQuery("(min-width: 640px)")
  const keys = entries.map((entry, i) => requestKey(entry, i))
  const arrived = useArrivals(keys)
  const rising = paused === false && following

  const toTop = useCallback(() => {
    const el = scrollRef.current
    if (el) el.scrollTop = 0
  }, [])

  // Newest first, so following means holding the *top* rather than the bottom.
  // A request log read from the bottom is a request log nobody reads.
  useLayoutEffect(() => {
    if (following) toTop()
  }, [entries, following, toTop])

  const onScroll = () => {
    const el = scrollRef.current
    if (el) setFollowing(el.scrollTop < 24)
  }

  const copyAll = () =>
    copyText(
      entries
        .map(
          (e) =>
            `${e.time} ${e.method} ${e.status} ${latency(e.durationMs)} ${requestURI(e)} ${e.remoteIp ?? ""}`,
        )
        .join("\n"),
      `Copied ${entries.length.toLocaleString()} requests`,
    )

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      {/* Two regions: the chips scroll sideways inside their own, and the
          controls keep a column of their own past a rule, so on a phone the
          last chip can run off the edge but Copy cannot. */}
      <div className="flex min-h-9 shrink-0 items-stretch border-b border-hairline">
        <div className="scroll-affordance flex min-w-0 flex-1 [scrollbar-width:none] items-center gap-1 overflow-x-auto px-2 py-1 [&::-webkit-scrollbar]:hidden">
          {leading}
        </div>
        <div className="flex shrink-0 items-center gap-0.5 border-l border-hairline px-1">
          {onPausedChange && (
            <ToolbarToggle
              active={paused}
              onClick={() => onPausedChange(!paused)}
              icon={paused ? Play : Pause}
              label={paused ? "Resume" : "Pause"}
              hint={
                paused ? "Append what arrived while paused" : "Hold new requests while you read"
              }
              // The held count stays beside the glyph at every width: it is
              // the one thing a paused pane has to say.
              extra={
                paused && held > 0 ? (
                  <span className="numeric">{held.toLocaleString()} held</span>
                ) : undefined
              }
            />
          )}
          <ToolbarToggle
            active={highlight}
            onClick={() => setLogView({ highlight: !highlight })}
            icon={BlendMode}
            label="Colour"
            hint="Colour each request by what is in it — the code by its family, the path, the method and the address as the log console does. Off shows every row as it was written."
          />
          <ToolbarToggle
            onClick={copyAll}
            icon={Copy}
            label="Copy"
            hint="Copy every request in this pane"
          />
        </div>
      </div>

      <div
        ref={scrollRef}
        onScroll={onScroll}
        className="min-h-0 flex-1 overflow-auto bg-surface-sunken font-mono text-xs leading-relaxed"
      >
        {entries.length === 0 ? (
          <div className="flex h-full items-center justify-center p-6 font-sans">{empty}</div>
        ) : (
          <div key="rows" className="animate-rise py-1">
            {/*
              A header, because unlike a log line these columns are not
              self-describing: "512" is a size and "34ms" is a duration only
              once something says so. It is `text-hint` medium and opaque, so
              rows scroll under it rather than through it. A phone's two-line
              rows name their parts by where they sit, and have none.
            */}
            {wide && (
              <div className="sticky top-0 z-10 flex items-center gap-3 border-b border-hairline bg-surface-sunken px-0 pr-4 pb-1 font-sans text-hint font-medium text-muted-foreground">
                <span aria-hidden className="w-0.5 shrink-0" />
                <span className="w-16 shrink-0">Time</span>
                <span className="w-12 shrink-0">Method</span>
                <span className="w-9 shrink-0 text-right">Code</span>
                {latencyKnown && <span className="w-20 shrink-0 text-right">Took</span>}
                <span className="min-w-0 flex-1">Path</span>
                <span className="hidden w-16 shrink-0 text-right lg:block">Size</span>
                <span className="hidden w-44 shrink-0 truncate xl:block">Client</span>
              </div>
            )}

            {entries.map((entry, i) => {
              const key = keys[i]
              const klass = statusClass(entry.status)
              const face = cn(
                "flex cursor-default transition-colors hover:bg-row-hover",
                // A failed request is washed the way the log console washes an
                // error line. A 4xx keeps its edge only: it is usually the
                // caller's, and a scanner's forty refusals washed amber would
                // bury the one 500 among them.
                klass === "5xx" && !plain && "bg-wash-danger",
                open === key && "bg-accent hover:bg-accent",
                rising && arrived.has(key) && "animate-rise",
              )
              const toggle = () => setOpen((current) => (current === key ? null : key))
              // The face is a real button, so the opened row — and the
              // client filter, Copy as curl and Block behind it — is one Tab
              // and Enter away rather than reachable by pointer alone. It
              // holds only text, so nothing interactive nests inside it.
              const opener = {
                type: "button" as const,
                onClick: toggle,
                "aria-expanded": open === key,
              }
              return (
                <div
                  key={key}
                  className={
                    wide
                      ? "[contain-intrinsic-size:auto_22px] [content-visibility:auto]"
                      : "[contain-intrinsic-size:auto_38px] [content-visibility:auto]"
                  }
                >
                  {wide ? (
                    <button
                      {...opener}
                      className={cn(
                        face,
                        "w-full items-center gap-3 py-px pr-4 text-left focus-ring-inset",
                      )}
                    >
                      <span
                        aria-hidden
                        className={cn("w-0.5 shrink-0 self-stretch", CLASS_EDGE[klass])}
                      />
                      <span
                        title={timestamp(entry.time)}
                        className="numeric w-16 shrink-0 text-muted-foreground/70 select-none"
                      >
                        {clock(entry.time)}
                      </span>
                      <MethodWord
                        method={entry.method}
                        plain={plain}
                        className="w-12 shrink-0 truncate"
                      />
                      <StatusCode
                        status={entry.status}
                        plain={plain}
                        className="w-9 shrink-0 justify-end"
                      />
                      {latencyKnown && <Took entry={entry} summary={summary} className="w-20" />}
                      <span className="flex min-w-0 flex-1" title={requestURI(entry)}>
                        <RequestPath path={entry.path} query={entry.query} plain={plain} />
                      </span>
                      <span className="numeric hidden w-16 shrink-0 text-right text-muted-foreground/70 lg:block">
                        {entry.size ? bytes(entry.size, 0) : "—"}
                      </span>
                      <ClientCell
                        ip={entry.remoteIp}
                        userAgent={entry.userAgent}
                        plain={plain}
                        className="hidden w-44 shrink-0 xl:flex"
                      />
                    </button>
                  ) : (
                    <button
                      {...opener}
                      className={cn(face, "w-full gap-3 pr-3 text-left focus-ring-inset")}
                    >
                      <span
                        aria-hidden
                        className={cn("w-0.5 shrink-0 self-stretch", CLASS_EDGE[klass])}
                      />
                      <span className="block min-w-0 flex-1 py-1">
                        <span className="flex min-w-0 items-center gap-3">
                          <StatusCode
                            status={entry.status}
                            plain={plain}
                            className="w-9 shrink-0"
                          />
                          <span className="flex min-w-0 flex-1" title={requestURI(entry)}>
                            <RequestPath path={entry.path} query={entry.query} plain={plain} />
                          </span>
                          {latencyKnown && (
                            <Took entry={entry} summary={summary} className="w-16" />
                          )}
                        </span>
                        <span className="flex min-w-0 items-center gap-1.5 pl-12 font-sans text-hint text-muted-foreground">
                          <span className="numeric shrink-0">{clock(entry.time)}</span>
                          <FactDot />
                          <MethodWord method={entry.method} plain={plain} className="shrink-0" />
                          <FactDot />
                          <span className="numeric shrink-0">
                            {entry.size ? bytes(entry.size, 0) : "—"}
                          </span>
                          <FactDot />
                          <ClientCell
                            ip={entry.remoteIp}
                            userAgent={entry.userAgent}
                            plain={plain}
                            className="min-w-0"
                          />
                        </span>
                      </span>
                    </button>
                  )}

                  {open === key && (
                    <RequestDetail
                      entry={entry}
                      summary={summary}
                      plain={plain}
                      onFilterPath={onFilterPath}
                      onFilterClient={onFilterClient}
                      onBlock={onBlock}
                      blocking={blocking}
                      outputHref={outputHref?.(entry)}
                      onEventsAround={onEventsAround}
                    />
                  )}
                </div>
              )
            })}
          </div>
        )}
      </div>

      {!following && entries.length > 0 && (
        <button
          className="flex items-center justify-center gap-1.5 border-t border-hairline py-1.5 text-xs text-muted-foreground focus-ring-inset transition-colors hover:bg-row-hover hover:text-foreground"
          onClick={() => {
            setFollowing(true)
            toTop()
          }}
        >
          <ChevronDoubleDown className="size-3 rotate-180" />
          Jump to the newest
          {held > 0 && <span className="numeric">· {held.toLocaleString()} held</span>}
        </button>
      )}

      <PaneFooter className="gap-x-4 gap-y-1 px-3 text-hint text-muted-foreground">
        <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          {status}
          {status && <FactDot />}
          <span className="numeric whitespace-nowrap">{entries.length.toLocaleString()} shown</span>
        </span>
        {footer}
      </PaneFooter>
    </div>
  )
}

/**
 * How long a request took, with a bar behind the figure for where it sits in
 * the window. The bar is behind rather than beside: a separate bar column
 * costs 60px of path, and what the reader wants from it is "is this one of
 * the slow ones", which a wash answers at a glance. Past a second both the
 * figure and its bar take amber — the same line the Slowest tenth tile draws.
 */
function Took({
  entry,
  summary,
  className,
}: {
  entry: RequestEntry
  summary: RequestSummary
  className?: string
}) {
  const share = latencyShare(entry.durationMs, summary)
  const slow = latencyTone(entry.durationMs) === "warning"
  return (
    <span className={cn("relative shrink-0 overflow-hidden text-right", className)}>
      {/* Only a share worth a bar: a tenth of the slowest hundredth and up. A
          sliver under a 24ms figure read as a text cursor, not a proportion. */}
      {share >= 0.1 && (
        <span
          aria-hidden
          className={cn(
            "absolute inset-y-px right-0 rounded-sm",
            slow ? "bg-wash-warning" : "bg-meter-track",
          )}
          style={{ width: `${share * 100}%` }}
        />
      )}
      <span
        className={cn(
          "numeric relative",
          slow ? "font-medium text-warning" : "text-muted-foreground",
        )}
      >
        {latency(entry.durationMs)}
      </span>
    </span>
  )
}

/**
 * One request, opened.
 *
 * Everything the row had no width for. It is a disclosure inside the row
 * rather than a drawer over the page, because the question it answers — "what
 * was this one" — is asked while scanning, and a panel that covers the list
 * makes the reader close it to ask the same question about the next row.
 *
 * It opens on who asked, in the account pages' own vocabulary (§14): the
 * browser as its mark with the system it runs on in the corner, what a person
 * would call it, and the network the address is on. The facts under it are
 * data rather than strings — the code with its word, the time with where it
 * sits in the window, the lock that says whether it was encrypted, the site
 * it came from as that site. The three questions a request raises are named
 * buttons; everything else is one menu, each verb with its sentence (§13).
 */
function RequestDetail({
  entry,
  summary,
  plain,
  onFilterPath,
  onFilterClient,
  onBlock,
  blocking,
  outputHref,
  onEventsAround,
}: {
  entry: RequestEntry
  summary: RequestSummary
  plain: boolean
  onFilterPath?: (path: string) => void
  onFilterClient?: (ip: string) => void
  onBlock?: (ip: string) => void
  blocking?: string | null
  outputHref?: string
  onEventsAround?: (entry: RequestEntry) => void
}) {
  const ip = entry.remoteIp
  const scanner = ip ? summary.scanners.some((s) => s.value === ip) : false
  const bracket = latencyBracket(entry.durationMs, summary.latency)
  const referer = entry.referer ? refererProduct(entry.referer) : undefined
  const Lock = entry.tls ? LockClosed : LockOpen
  const curl = curlOf(entry)
  // Who asked, drawn as the row draws it: a crawler as the engine that sends
  // it — a smartphone Googlebot names Chrome and Android too, and drawing
  // those beside the word "Googlebot" was the mark and the name disagreeing
  // — and a request with no agent as where it came from.
  const crawler = entry.userAgent ? crawlerOf(entry.userAgent) : undefined
  const network = ip ? networkOf(ip) : undefined
  const mark = crawler ? (
    <ProductLogo id={crawler.product} size="sm" fallback={Bug} />
  ) : entry.userAgent ? (
    <ClientMark userAgent={entry.userAgent} />
  ) : (
    <ProductLogo
      id={network?.product}
      size="sm"
      fallback={network ? NETWORK_GLYPH[network.kind] : Globe}
    />
  )

  const facts: [string, React.ReactNode, string?][] = [
    ["When", timestamp(entry.time)],
    [
      "Request",
      <span key="r" className="flex min-w-0 items-baseline gap-2">
        <MethodWord method={entry.method} plain={plain} className="shrink-0" />
        <RequestPath path={entry.path} query={entry.query} plain={plain} />
      </span>,
      `${entry.method} ${requestURI(entry)}`,
    ],
    ["Status", <StatusCode key="s" status={entry.status} word plain={plain} />],
    [
      "Took",
      <span key="t" className="flex min-w-0 items-baseline gap-2">
        <span
          className={cn(
            "numeric shrink-0",
            latencyTone(entry.durationMs) === "warning" && "font-medium text-warning",
          )}
        >
          {latency(entry.durationMs)}
        </span>
        {bracket && <span className="truncate font-sans text-muted-foreground">{bracket}</span>}
      </span>,
    ],
    ["Sent", entry.size ? bytes(entry.size) : "nothing"],
    ["Host", entry.host ?? "—"],
    [
      "Protocol",
      <span key="p" className="flex min-w-0 items-center gap-1.5">
        <Lock
          aria-hidden
          className={cn("size-3 shrink-0", entry.tls ? "text-muted-foreground" : "text-warning")}
        />
        <span className="truncate">
          {[entry.proto, entry.tls ? "TLS" : "plaintext"].filter(Boolean).join(" · ")}
        </span>
      </span>,
    ],
    ["Agent", entry.userAgent || "—", entry.userAgent],
    [
      "Referer",
      entry.referer ? (
        <span key="f" className="flex min-w-0 items-center gap-1.5">
          {referer ? (
            <ProductGlyph id={referer} />
          ) : (
            <Globe aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
          )}
          <span className="truncate">{entry.referer}</span>
        </span>
      ) : (
        "—"
      ),
      entry.referer,
    ],
  ]

  const verbs: Verb[] = [
    ...(ip && onFilterClient
      ? [
          {
            key: "client",
            label: "Show every request from this client",
            detail: "Narrow the rows and the insights to this address.",
            icon: Filter,
            run: () => onFilterClient(ip),
          },
        ]
      : []),
    {
      key: "json",
      label: "Copy as JSON",
      detail: "The whole record, as the server sent it.",
      icon: Copy,
      run: () => void copyText(JSON.stringify(entry, null, 2), "Request copied"),
    },
    ...(curl
      ? [
          {
            key: "curl",
            label: "Copy as curl",
            detail: "The same request again from a shell — method, host, path and agent.",
            icon: Terminal,
            run: () => void copyText(curl, "Copied as curl"),
          },
        ]
      : []),
    ...(ip && onBlock && !isPrivate(ip)
      ? [
          {
            key: "block",
            // A deny takes a round trip, and a verb that sits still while it
            // runs is pressed twice — which writes the rule twice (§13).
            label: blocking === ip ? "Blocking…" : "Block this address",
            detail: "Put a deny rule in front of every allow. It does not expire.",
            icon: Slash,
            danger: true,
            disabled: blocking === ip,
            progressive: "Blocking…",
            run: () => onBlock(ip),
          },
        ]
      : []),
  ]

  const action = "w-full font-sans max-sm:h-9 sm:w-auto"

  return (
    <div className="animate-rise border-y border-hairline bg-background px-4 py-3 pl-[1.125rem]">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b border-hairline pb-2 font-sans text-xs">
        {mark}
        <span className="text-body font-medium">
          {entry.userAgent ? clientName(entry.userAgent) : "No user agent"}
        </span>
        {ip && (
          <>
            <FactDot />
            <NetworkFact ip={ip} glyph address={<Address ip={ip} plain={plain} />} />
          </>
        )}
        {scanner && (
          <Tag tone="warning" className="ml-auto">
            scanner
          </Tag>
        )}
      </div>

      <dl className="mt-2.5 grid grid-cols-1 gap-x-6 gap-y-1.5 sm:grid-cols-2 xl:grid-cols-3">
        {facts.map(([label, value, title]) => (
          <div key={label} className="flex min-w-0 gap-2">
            <dt className="w-20 shrink-0 font-sans text-hint leading-5 text-muted-foreground">
              {label}
            </dt>
            <dd
              className="min-w-0 truncate"
              title={title ?? (typeof value === "string" ? value : undefined)}
            >
              {value}
            </dd>
          </div>
        ))}
      </dl>

      <div className="mt-3 flex flex-col gap-1.5 sm:flex-row sm:flex-wrap sm:items-center">
        {/* The two questions a failing request raises, each one press: what
            did the container print then, and what happened to it then. */}
        {outputHref && (
          <Button size="xs" variant="outline" className={action} asChild>
            <a href={outputHref}>
              <TerminalWindow className="size-3" />
              Container output around this moment
            </a>
          </Button>
        )}
        {onEventsAround && (
          <Button
            size="xs"
            variant="outline"
            className={action}
            onClick={() => onEventsAround(entry)}
          >
            <ClockRewind className="size-3" />
            Container events around this moment
          </Button>
        )}
        {onFilterPath && (
          <Button
            size="xs"
            variant="outline"
            className={action}
            onClick={() => onFilterPath(entry.path)}
          >
            <Filter className="size-3" />
            Show every request to this path
          </Button>
        )}
        <VerbMenu
          verbs={verbs}
          align="start"
          label="More actions for this request"
          trigger={
            <Button
              size="xs"
              variant="outline"
              className={action}
              aria-label="More actions for this request"
            >
              <MoreHorizontal className="size-3" />
              <span className="sm:hidden">More</span>
            </Button>
          }
        />
      </div>
    </div>
  )
}

/**
 * The request again, as a line a shell can run — or nothing, for a request
 * the ingress recorded no host for: a curl at localhost would ask whatever
 * answers on the reader's own machine, not the deployment that answered this.
 * A HEAD is `-I`: `-X HEAD` makes curl wait for a body that never comes.
 */
function curlOf(entry: RequestEntry) {
  if (!entry.host) return undefined
  const quote = (value: string) => `'${value.replace(/'/g, `'\\''`)}'`
  const url = `${entry.tls ? "https" : "http"}://${entry.host}${requestURI(entry)}`
  return [
    "curl",
    entry.method === "HEAD" ? "-I" : entry.method === "GET" ? null : `-X ${entry.method}`,
    quote(url),
    entry.userAgent ? `-H ${quote(`User-Agent: ${entry.userAgent}`)}` : null,
    entry.referer ? `-H ${quote(`Referer: ${entry.referer}`)}` : null,
  ]
    .filter(Boolean)
    .join(" ")
}

/** A control on the strip above the rows, matching the log console's own. */
function ToolbarToggle({
  active,
  onClick,
  icon: Icon,
  label,
  hint,
  extra,
}: {
  active?: boolean
  onClick: () => void
  icon: React.ComponentType<{ className?: string }>
  label: string
  hint: string
  extra?: React.ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          size="sm"
          variant={active ? "secondary" : "ghost"}
          className="h-7 shrink-0 gap-1.5 px-2 text-xs"
          aria-label={label}
          aria-pressed={active}
          onClick={onClick}
        >
          <Icon className="size-3" />
          <span className="hidden 2xl:inline">{label}</span>
          {extra}
        </Button>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">{hint}</TooltipContent>
    </Tooltip>
  )
}
