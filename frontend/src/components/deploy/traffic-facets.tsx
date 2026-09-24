"use client"

import { Bug, DesktopDevice, Globe, Terminal, Warning } from "@/components/icons"
import { cn } from "@/lib/utils"
import { bytes, clock, plural } from "@/lib/format"
import type { RequestEntry, RequestFacet, RequestSummary } from "@/lib/types"
import { agentProduct, networkOf, refererProduct } from "@/lib/clients"
import { latency, latencyShare, latencyTone, statusClass } from "@/lib/requests"
import { BarList } from "@/components/bar-list"
import { NETWORK_GLYPH } from "@/components/client-mark"
import { FactDot } from "@/components/metrics/host-identity"
import { DimActions } from "@/components/icon-action"
import { ProductGlyph } from "@/components/product-logo"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { BlurFade } from "@/components/ui/blur-fade"
import { LatencyLadder } from "@/components/deploy/latency-ladder"
import {
  Address,
  MethodWord,
  RequestPath,
  StatusCode,
  isPrivate,
} from "@/components/deploy/request-marks"

/**
 * The readings the window already computes, drawn.
 *
 * The rows say what happened; these say what it adds up to: how the response
 * times spread, which page is failing, which client is trying doors, how much
 * of the traffic is a bot, where visitors came from, and which route the slow
 * tenth belongs to. Each list is a plain block — a head, a hairline, rows —
 * because six framed boxes under a chart is six boxes, and the reader is here
 * to compare. The heads are 13px and semibold, a step above the 12px rows
 * they open; as small-caps eyebrows they were the quietest line in each block
 * (§8's argument about `FormSection`, one rung down).
 *
 * Everything in the lists is drawn as what it is (§14): a path in the path
 * hue, a status code in its family's colour, a referrer as the site it is, a
 * browser or a crawler as its own mark, an address as the network it sits on.
 * A column of ten grey strings is read; a column of marks is scanned.
 *
 * Two columns rather than three: the lists differ in length, and a
 * three-column grid left a hole under every short one. The long list leads
 * the left column; the right holds the short ones in turn. The response-time
 * ladder spans both, because it is the window's shape rather than one of its
 * lists.
 *
 * Every row that names a path, an address, a domain, a code or a latency
 * narrows the rows above to it on a click — through the API's own `path`,
 * `client`, `host`, `status` and `minMs` — so a spike on the chart, a failing
 * path here and the requests behind it are one motion rather than three.
 */
export function TrafficFacets({
  summary,
  slowest,
  latencyKnown,
  threshold,
  onFilterPath,
  onFilterClient,
  onFilterHost,
  onFilterStatus,
  onFilterSlow,
  onBlock,
  blocking,
}: {
  summary: RequestSummary
  slowest?: RequestEntry[]
  latencyKnown: boolean
  /** The enabled latency alert's line, drawn on the ladder. */
  threshold?: number
  onFilterPath: (path: string) => void
  onFilterClient: (ip: string) => void
  onFilterHost: (host: string) => void
  onFilterStatus: (code: number) => void
  onFilterSlow: (ms: number) => void
  /** Deny an address at the firewall. Absent for a role that may not. */
  onBlock?: (ip: string) => void
  blocking?: string | null
}) {
  const agents = summary.agents.map((facet) => ({ facet, agent: agentProduct(facet.value) }))
  const counted = agents.reduce((n, { facet }) => n + facet.count, 0)
  const share = (kind: string) =>
    counted > 0
      ? Math.round(
          (agents
            .filter(({ agent }) => agent.kind === kind)
            .reduce((n, { facet }) => n + facet.count, 0) /
            counted) *
            100,
        )
      : 0
  const perAnswer = summary.total > 0 ? summary.bytes / summary.total : 0
  const heaviest = [...summary.paths]
    .filter((path) => (path.bytes ?? 0) > 0)
    .sort((a, b) => (b.bytes ?? 0) - (a.bytes ?? 0))
    .slice(0, 5)
  const scanners = new Set(summary.scanners.map((scanner) => scanner.value))
  // One trailing column down the right-hand lists — the Block verb's, a bot's
  // tag, nothing — so Clients, Agents and Status codes end their bars on one
  // line and the figures stay the last column. As wide as Block where the
  // role may block, as wide as the tag where it may not.
  const slot = onBlock ? "w-16" : "w-8"
  const cut = agents.length >= SHOWN ? ` of the top ${SHOWN}` : ""

  return (
    <div className="flex flex-col gap-8 px-5 py-5">
      {summary.scanners.length > 0 && (
        <BlurFade>
          <Scanners summary={summary} onBlock={onBlock} blocking={blocking} />
        </BlurFade>
      )}

      {latencyKnown && summary.latency && (
        <Facet title="Response times" reading={`mean ${latency(summary.latency.mean)}`} index={1}>
          <LatencyLadder latency={summary.latency} threshold={threshold} onPick={onFilterSlow} />
        </Facet>
      )}

      <div className="grid grid-cols-1 gap-x-10 gap-y-8 lg:grid-cols-2">
        <div className="flex min-w-0 flex-col gap-8">
          <Facet title="Pages" reading={tally(summary.paths, "path")} index={2}>
            <BarList
              items={rank(summary.paths).map((p) => ({
                key: p.value,
                label: <RequestPath path={p.value} />,
                value: p.count.toLocaleString(),
                share: p.count / peak(summary.paths),
                signal: p.count ? p.errors / p.count : 0,
                hint: pageHint(p, latencyKnown),
                title: `Show every request to ${p.value}`,
                onClick: () => onFilterPath(p.value),
              }))}
              emptyLabel="No requests in this window."
            />
          </Facet>

          <Facet
            title="Came from"
            reading={summary.referers.length > 0 ? tally(summary.referers, "site") : undefined}
            index={3}
          >
            <BarList
              items={rank(summary.referers).map((r) => {
                const product = refererProduct(r.value)
                return {
                  key: r.value,
                  mark: product ? (
                    <ProductGlyph id={product} />
                  ) : (
                    <Globe className="size-3.5 text-muted-foreground" />
                  ),
                  mono: false,
                  label: r.value,
                  value: r.count.toLocaleString(),
                  share: r.count / peak(summary.referers),
                }
              })}
              emptyLabel="No outside referers — direct visits and the site's own links."
            />
          </Facet>

          {summary.hosts.length > 1 && (
            <Facet title="Domains" reading={plural(summary.hosts.length, "name")} index={4}>
              <BarList
                items={rank(summary.hosts).map((h) => ({
                  key: h.value,
                  mark: <Globe className="size-3.5 text-muted-foreground" />,
                  label: h.value,
                  value: h.count.toLocaleString(),
                  share: h.count / peak(summary.hosts),
                  signal: h.count ? h.errors / h.count : 0,
                  hint:
                    h.errors > 0 ? (
                      <span className="text-destructive">{h.errors} × 5xx</span>
                    ) : undefined,
                  title: `Show every request to ${h.value}`,
                  onClick: () => onFilterHost(h.value),
                }))}
              />
            </Facet>
          )}

          <Facet title="Served" index={5}>
            <dl className="grid grid-cols-2 gap-4 pb-3">
              {[
                ["Sent", bytes(summary.bytes)],
                ["Each, on average", bytes(perAnswer)],
              ].map(([label, value]) => (
                <div key={label} className="min-w-0">
                  <dt className="truncate text-hint text-muted-foreground">{label}</dt>
                  <dd className="numeric truncate text-title font-semibold">{value}</dd>
                </div>
              ))}
            </dl>
            {heaviest.length > 0 && (
              <>
                <p className="pb-1 text-hint text-muted-foreground">
                  Heaviest of the busiest pages
                </p>
                <BarList
                  items={heaviest.map((p) => ({
                    key: p.value,
                    label: <RequestPath path={p.value} />,
                    value: bytes(p.bytes ?? 0, 0),
                    share: (p.bytes ?? 0) / Math.max(heaviest[0].bytes ?? 0, 1),
                    title: `Show every request to ${p.value}`,
                    onClick: () => onFilterPath(p.value),
                  }))}
                />
              </>
            )}
          </Facet>
        </div>

        <div className="flex min-w-0 flex-col gap-8">
          <Facet
            title="Clients"
            reading={[
              tally(summary.clients, "address", "addresses"),
              scanners.size > 0 ? plural(scanners.size, "scanner") : null,
            ]
              .filter(Boolean)
              .join(" · ")}
            index={2}
          >
            <BarList
              items={rank(summary.clients).map((c) => {
                const scanner = scanners.has(c.value) || (c.probes ?? 0) >= 3
                const network = networkOf(c.value)
                const Place = NETWORK_GLYPH[network.kind]
                return {
                  key: c.value,
                  mark: network.product ? (
                    <ProductGlyph id={network.product} />
                  ) : (
                    <Place className="size-3.5 text-muted-foreground" />
                  ),
                  label: <Address ip={c.value} />,
                  value: c.count.toLocaleString(),
                  share: c.count / peak(summary.clients),
                  signal: c.count ? (c.refused ?? 0) / c.count : 0,
                  tone: "warning" as const,
                  // The most telling thing about the address, and only that:
                  // the mark already draws the network, so beside a scanner's
                  // tell or a refusal count its word only cost the address
                  // its width on a phone.
                  hint: scanner ? (
                    <span className="text-warning">scanner · {c.probes} probes</span>
                  ) : (c.refused ?? 0) > 0 ? (
                    `${c.refused} refused`
                  ) : (
                    network.label
                  ),
                  title: `Show every request from ${c.value}`,
                  onClick: () => onFilterClient(c.value),
                  trailing: onBlock ? (
                    // A column of its own, always drawn and quiet until the
                    // row is under the pointer (§6): an address worth a deny
                    // rule gets the verb, one inside the network gets the
                    // same width of nothing so the figures still line up.
                    <DimActions className={cn(slot, "justify-end")}>
                      {!isPrivate(c.value) && (
                        <Button
                          size="xs"
                          variant="ghost"
                          disabled={blocking === c.value}
                          onClick={() => onBlock(c.value)}
                        >
                          {blocking === c.value ? "Blocking…" : "Block"}
                        </Button>
                      )}
                    </DimActions>
                  ) : (
                    <span aria-hidden className={cn(slot, "shrink-0")} />
                  ),
                }
              })}
              emptyLabel="No clients in this window."
            />
          </Facet>

          <Facet
            title="Agents"
            reading={
              counted > 0
                ? `${share("bot")}% bots · ${share("browser")}% browsers${cut}`
                : undefined
            }
            index={3}
          >
            <BarList
              items={agents.slice(0, SHOWN).map(({ facet: a, agent }) => ({
                key: a.value,
                mark: agent.product ? (
                  <ProductGlyph id={agent.product} />
                ) : agent.kind === "bot" ? (
                  <Bug className="size-3.5 text-muted-foreground" />
                ) : agent.kind === "program" ? (
                  <Terminal className="size-3.5 text-muted-foreground" />
                ) : (
                  <DesktopDevice className="size-3.5 text-muted-foreground" />
                ),
                mono: false,
                label: a.value || "No user agent",
                value: a.count.toLocaleString(),
                share: a.count / peak(summary.agents),
                signal: a.count ? a.errors / a.count : 0,
                hint:
                  a.errors > 0 ? (
                    <span className="text-destructive">{a.errors} × 5xx</span>
                  ) : undefined,
                // The column's slot on every row, so the bars end on one line
                // whether the row is a bot or not.
                trailing: (
                  <span className={cn("flex shrink-0 justify-end", slot)}>
                    {agent.kind === "bot" && <Tag>bot</Tag>}
                  </span>
                ),
              }))}
              emptyLabel="No user agents recorded."
            />
          </Facet>

          <Facet title="Status codes" reading={<StatusReading summary={summary} />} index={4}>
            <BarList
              items={summary.statuses.map((s) => {
                const code = Number(s.value)
                const klass = statusClass(code)
                return {
                  key: s.value,
                  mono: false,
                  label: <StatusCode status={code} word />,
                  value: s.count.toLocaleString(),
                  share: s.count / peak(summary.statuses),
                  signal: klass === "5xx" || klass === "4xx" ? 1 : 0,
                  tone: klass === "5xx" ? ("danger" as const) : ("warning" as const),
                  title: `Show every ${code}`,
                  onClick: () => onFilterStatus(code),
                  trailing: <span aria-hidden className={cn(slot, "shrink-0")} />,
                }
              })}
              emptyLabel="No requests in this window."
            />
          </Facet>

          {latencyKnown && (
            <Facet
              title="Slowest"
              reading={summary.latency ? `p99 ${latency(summary.latency.p99)}` : undefined}
              index={5}
            >
              {slowest && slowest.length > 0 ? (
                <ul className="flex flex-col">
                  {slowest.map((e, i) => (
                    <li key={`${e.seq ?? i}`}>
                      <SlowRow entry={e} summary={summary} onFilterPath={onFilterPath} />
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="py-3 text-hint text-muted-foreground">No timed requests yet.</p>
              )}
            </Facet>
          )}
        </div>
      </div>
    </div>
  )
}

/**
 * One block: a head that outranks the rows it opens, its reading at the
 * right, a hairline, the list. Each arrives a beat after the one before it,
 * as the fleet's cards do (§11 *arrived*); the whole set remounts when the
 * window moves, so a new window arrives the same way.
 */
function Facet({
  title,
  reading,
  index,
  children,
}: {
  title: string
  reading?: React.ReactNode
  index: number
  children: React.ReactNode
}) {
  return (
    <BlurFade delay={index * 0.04} className="min-w-0">
      <section className="min-w-0">
        <div className="mb-3 flex items-baseline justify-between gap-3 border-b border-hairline pb-2">
          <h3 className="text-body font-semibold text-foreground">{title}</h3>
          {reading && (
            <span className="numeric truncate text-hint text-muted-foreground">{reading}</span>
          )}
        </div>
        {children}
      </section>
    </BlurFade>
  )
}

/**
 * A page's two telling figures, in one hint so it reads as one line: its
 * failures in the failure hue and its tail latency in amber once it is past a
 * second.
 */
function pageHint(p: RequestFacet, latencyKnown: boolean) {
  const timed = latencyKnown && p.p95 !== undefined
  if (p.errors === 0 && !timed) return undefined
  return (
    <>
      {p.errors > 0 && <span className="text-destructive">{p.errors} × 5xx</span>}
      {p.errors > 0 && timed && " · "}
      {timed && (
        <span className={cn(latencyTone(p.p95) === "warning" && "text-warning")}>
          p95 {latency(p.p95)}
        </span>
      )}
    </>
  )
}

/** The window's two failure shares, each in its family's hue when it is not zero. */
function StatusReading({ summary }: { summary: RequestSummary }) {
  const failing = summary.errorRate * 100
  const refused = summary.clientErrorRate * 100
  return (
    <>
      <span className={cn(failing > 0 && "text-destructive")}>{failing.toFixed(1)}% 5xx</span>
      <span className="text-muted-foreground/50"> · </span>
      <span className={cn(refused > 0 && "text-warning")}>{refused.toFixed(1)}% 4xx</span>
    </>
  )
}

/**
 * One of the window's slowest requests, laid out as the console lays a row:
 * how long it took against the window's slowest hundredth, then what it was.
 * The figure is amber only past a second — the slowest request of a healthy
 * window can be thirty milliseconds, and colouring it anyway says nothing.
 */
function SlowRow({
  entry,
  summary,
  onFilterPath,
}: {
  entry: RequestEntry
  summary: RequestSummary
  onFilterPath: (path: string) => void
}) {
  const share = latencyShare(entry.durationMs, summary)
  const slow = latencyTone(entry.durationMs) === "warning"
  // On a phone the clock goes, not the path: the path is what the row is
  // read for, and the title still names the moment.
  return (
    <button
      type="button"
      onClick={() => onFilterPath(entry.path)}
      aria-label={`Show every request to ${entry.path}`}
      title={`${clock(entry.time)} — Show every request to ${entry.path}`}
      className="flex w-full min-w-0 items-center gap-3 rounded-sm px-2 py-1.5 text-left font-mono text-xs focus-ring-inset transition-colors hover:bg-row-hover"
    >
      <span className="relative w-16 shrink-0 overflow-hidden text-right">
        {share > 0 && (
          <span
            aria-hidden
            className={cn(
              "absolute inset-y-0 right-0 rounded-sm",
              slow ? "bg-wash-warning" : "bg-meter-track",
            )}
            style={{ width: `${Math.max(share * 100, 4)}%` }}
          />
        )}
        <span className={cn("numeric relative", slow ? "text-warning" : "text-foreground")}>
          {latency(entry.durationMs)}
        </span>
      </span>
      <MethodWord method={entry.method} className="w-10 shrink-0" />
      <StatusCode status={entry.status} className="w-8 shrink-0" />
      <RequestPath path={entry.path} className="flex-1" />
      <span className="numeric shrink-0 text-muted-foreground/70 max-sm:hidden">
        {clock(entry.time)}
      </span>
    </button>
  )
}

/**
 * The clients that tried the doors a script tries on every host. A notice
 * because it asks for a decision — whether to deny them — and it carries its
 * own verb for each (§14: a notice is for what the reader has to act on; a
 * `Notice` that carries its own button, as the Packages page's do).
 */
function Scanners({
  summary,
  onBlock,
  blocking,
}: {
  summary: RequestSummary
  onBlock?: (ip: string) => void
  blocking?: string | null
}) {
  const probes = summary.probes.slice(0, 4)
  const more = summary.probes.length - probes.length
  return (
    <Notice tone="warning" icon={Warning} title="Scanners">
      <p>
        {plural(summary.scanners.length, "client")} tried the paths a script tries on every host,
        refused every time.
      </p>
      <p className="mt-1.5 flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs">
        {probes.map((probe, i) => (
          <span key={probe.value} className="inline-flex items-center gap-1.5">
            {i > 0 && <FactDot />}
            <RequestPath path={probe.value} />
          </span>
        ))}
        {more > 0 && <span className="text-muted-foreground">+{more} more</span>}
      </p>
      <ul className="mt-2 flex flex-col gap-1">
        {summary.scanners.map((scanner) => {
          const network = networkOf(scanner.value)
          const Place = NETWORK_GLYPH[network.kind]
          return (
            <li key={scanner.value} className="flex flex-wrap items-center gap-x-2 gap-y-1">
              {network.product ? (
                <ProductGlyph id={network.product} />
              ) : (
                <Place aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
              )}
              <Address ip={scanner.value} />
              <span className="numeric text-muted-foreground">
                {plural(scanner.probes ?? 0, "probe")}
              </span>
              {onBlock && !isPrivate(scanner.value) && (
                <Button
                  size="xs"
                  variant="destructive"
                  disabled={blocking === scanner.value}
                  onClick={() => onBlock(scanner.value)}
                >
                  {blocking === scanner.value ? "Blocking…" : "Block"}
                </Button>
              )}
            </li>
          )
        })}
      </ul>
    </Notice>
  )
}

/** How many rows a list draws. */
const SHOWN = 8

function rank(facets: RequestFacet[]) {
  return facets.slice(0, SHOWN)
}

/**
 * A list's reading says only what is known. The server caps each list (ten
 * addresses, ten sites, twelve paths) and this draws the first eight, so a
 * full list is the top of a longer one: "top 8", never "10 addresses" over
 * eight rows of a window that saw three hundred.
 */
function tally(facets: RequestFacet[], one: string, many?: string) {
  return facets.length >= SHOWN ? `top ${SHOWN}` : plural(facets.length, one, many)
}

function peak(facets: RequestFacet[]) {
  return Math.max(...facets.map((f) => f.count), 1)
}
