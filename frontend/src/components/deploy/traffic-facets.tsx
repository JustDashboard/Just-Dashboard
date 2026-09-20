"use client"

import { bytes, clock, plural } from "@/lib/format"
import type { RequestEntry, RequestFacet, RequestSummary } from "@/lib/types"
import { latency } from "@/lib/requests"
import { BarList } from "@/components/bar-list"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"

/**
 * The readings the window already computes, drawn.
 *
 * The rows say what happened; these say what it adds up to: which page is
 * failing, which client is trying doors, how much of the traffic is a bot,
 * where visitors came from, and which route the slow tenth belongs to. Each
 * list is a plain block — an eyebrow, a hairline, rows — because six framed
 * boxes under a chart is six boxes, and the reader is here to compare.
 *
 * Two columns rather than three: the lists differ in length, and a
 * three-column grid left a hole under every short one. The long list leads
 * the left column; the right holds the short ones in turn.
 *
 * Every row that names a path narrows the rows above to it on a click, so a
 * spike on the chart, a failing path here and the requests behind it are one
 * motion rather than three.
 */
export function TrafficFacets({
  summary,
  slowest,
  latencyKnown,
  onFilterPath,
  onFilterClient,
  onBlock,
  blocking,
}: {
  summary: RequestSummary
  slowest?: RequestEntry[]
  latencyKnown: boolean
  onFilterPath: (path: string) => void
  onFilterClient: (ip: string) => void
  /** Deny an address at the firewall. Absent for a role that may not. */
  onBlock?: (ip: string) => void
  blocking?: string | null
}) {
  const humans = summary.agents.filter((a) => !isBot(a.value)).reduce((n, a) => n + a.count, 0)
  const bots = summary.agents.filter((a) => isBot(a.value)).reduce((n, a) => n + a.count, 0)
  const perAnswer = summary.total > 0 ? summary.bytes / summary.total : 0

  return (
    <div className="flex flex-col gap-6 px-5 py-5">
      {summary.scanners.length > 0 && (
        <Notice tone="warning" title="Scanners">
          {plural(summary.scanners.length, "client")} asked for{" "}
          {summary.probes.slice(0, 3).map((p) => p.value).join(", ")}
          {summary.probes.length > 3 ? " and more" : ""} — the paths a script tries on every host,
          refused every time. The addresses are listed under Clients
          {onBlock ? ", each with a Block." : "."}
        </Notice>
      )}

      <div className="grid grid-cols-1 gap-x-10 gap-y-8 lg:grid-cols-2">
        <div className="flex min-w-0 flex-col gap-8">
          <Facet title="Pages" reading={`${summary.pages.toLocaleString()} page views`}>
            <BarList
              items={rank(summary.paths).map((p) => ({
                key: p.value,
                label: p.value,
                value: p.count.toLocaleString(),
                share: p.count / peak(summary.paths),
                signal: p.count ? p.errors / p.count : 0,
                hint: [
                  p.errors > 0 ? `${p.errors} × 5xx` : null,
                  latencyKnown && p.p95 !== undefined ? `p95 ${latency(p.p95)}` : null,
                ]
                  .filter(Boolean)
                  .join(" · "),
                title: `Show every request to ${p.value}`,
                onClick: () => onFilterPath(p.value),
              }))}
              emptyLabel="No requests in this window."
            />
          </Facet>

          <Facet title="Came from">
            <BarList
              items={rank(summary.referers).map((r) => ({
                key: r.value,
                label: r.value,
                value: r.count.toLocaleString(),
                share: r.count / peak(summary.referers),
              }))}
              emptyLabel="No outside referers — direct visits and the site's own links."
            />
          </Facet>

          <Facet title="Served">
            <dl className="grid grid-cols-3 gap-4 py-1">
              {[
                ["Sent", bytes(summary.bytes)],
                ["Answers", summary.total.toLocaleString()],
                ["Each, on average", bytes(perAnswer)],
              ].map(([label, value]) => (
                <div key={label} className="min-w-0">
                  <dt className="truncate text-hint text-muted-foreground">{label}</dt>
                  <dd className="numeric truncate text-body font-medium">{value}</dd>
                </div>
              ))}
            </dl>
          </Facet>
        </div>

        <div className="flex min-w-0 flex-col gap-8">
          <Facet title="Clients" reading={`${summary.clients.length} shown`}>
            <BarList
              items={rank(summary.clients).map((c) => {
                const scanner = (c.probes ?? 0) >= 3
                return {
                  key: c.value,
                  label: c.value,
                  value: c.count.toLocaleString(),
                  share: c.count / peak(summary.clients),
                  signal: c.count ? (c.refused ?? 0) / c.count : 0,
                  tone: "warning" as const,
                  hint: scanner
                    ? `scanner · ${c.probes} probes`
                    : (c.refused ?? 0) > 0
                      ? `${c.refused} refused`
                      : undefined,
                  title: `Show every request from ${c.value}`,
                  onClick: () => onFilterClient(c.value),
                  trailing:
                    onBlock && !isPrivate(c.value) ? (
                      <Button
                        size="sm"
                        variant={scanner ? "destructive" : "ghost"}
                        className="h-6 shrink-0 px-2 text-hint"
                        disabled={blocking === c.value}
                        onClick={() => onBlock(c.value)}
                      >
                        {blocking === c.value ? "Blocking…" : "Block"}
                      </Button>
                    ) : undefined,
                }
              })}
              emptyLabel="No clients in this window."
            />
          </Facet>

          <Facet
            title="Agents"
            reading={bots + humans > 0 ? `${Math.round((bots / (bots + humans)) * 100)}% bots` : undefined}
          >
            <BarList
              items={rank(summary.agents).map((a) => ({
                key: a.value,
                label: (
                  <>
                    {a.value}
                    {isBot(a.value) && <Tag className="ml-2 align-middle">bot</Tag>}
                  </>
                ),
                value: a.count.toLocaleString(),
                share: a.count / peak(summary.agents),
                signal: a.count ? a.errors / a.count : 0,
                hint: a.errors > 0 ? `${a.errors} × 5xx` : undefined,
              }))}
              emptyLabel="No user agents recorded."
            />
          </Facet>

          <Facet title="Status codes">
            <BarList
              items={summary.statuses.map((s) => {
                const code = Number(s.value)
                return {
                  key: s.value,
                  label: `${s.value} ${statusWord(code)}`,
                  value: s.count.toLocaleString(),
                  share: s.count / peak(summary.statuses),
                  signal: code >= 400 ? 1 : 0,
                  tone: code >= 500 ? ("danger" as const) : ("warning" as const),
                }
              })}
              emptyLabel="No requests in this window."
            />
          </Facet>

          {latencyKnown && (
            <Facet title="Slowest" reading="in this window">
              {slowest && slowest.length > 0 ? (
                <ul className="flex flex-col">
                  {slowest.map((e, i) => (
                    <li key={`${e.seq ?? i}`}>
                      <button
                        type="button"
                        onClick={() => onFilterPath(e.path)}
                        aria-label={`Show every request to ${e.path}`}
                        title={`Show every request to ${e.path}`}
                        className="flex w-full min-w-0 items-baseline gap-3 rounded-sm px-2 py-1.5 text-left font-mono text-xs focus-ring-inset transition-colors hover:bg-row-hover"
                      >
                        <span className="numeric w-16 shrink-0 text-right text-warning">
                          {latency(e.durationMs)}
                        </span>
                        <span className="min-w-0 flex-1 truncate">{e.path}</span>
                        <span className="numeric shrink-0 text-muted-foreground/70">{clock(e.time)}</span>
                      </button>
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

/** One block: an eyebrow with its reading, a hairline, the list. */
function Facet({
  title,
  reading,
  children,
}: {
  title: string
  reading?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <section className="min-w-0">
      <div className="mb-3 flex items-baseline justify-between gap-3 border-b border-hairline pb-2">
        <h3 className="eyebrow">{title}</h3>
        {reading && <span className="numeric truncate text-hint text-muted-foreground">{reading}</span>}
      </div>
      {children}
    </section>
  )
}

function rank(facets: RequestFacet[]) {
  return facets.slice(0, 8)
}

function peak(facets: RequestFacet[]) {
  return Math.max(...facets.map((f) => f.count), 1)
}

function isBot(agent: string) {
  return /bot|spider|crawl|curl|wget|python|go-http|java\//i.test(agent)
}

/**
 * An address there is no point offering to block: this server talking to
 * itself, or something already inside the network the firewall stands at the
 * edge of.
 *
 * `172.` is the trap, and this read it as private for the whole /8. RFC 1918
 * reserves 172.16 through 172.31 and nothing either side, so a prefix test
 * hides the verb for 172.217.x.x — which is Google, and precisely the kind of
 * address somebody looking at this list wants to act on.
 */
function isPrivate(ip: string) {
  // An IPv4-mapped address is an IPv4 address wearing a hat.
  const address = ip.toLowerCase().replace(/^::ffff:/, "")
  const v4 = /^(\d{1,3})\.(\d{1,3})\.\d{1,3}\.\d{1,3}$/.exec(address)
  if (v4) {
    const [a, b] = [Number(v4[1]), Number(v4[2])]
    return (
      a === 0 ||
      a === 10 ||
      a === 127 ||
      (a === 172 && b >= 16 && b <= 31) ||
      (a === 192 && b === 168) ||
      (a === 169 && b === 254)
    )
  }
  // ::1 is loopback, fc00::/7 unique-local, fe80::/10 link-local.
  return address === "::1" || /^f[cd]/.test(address) || /^fe[89ab]/.test(address)
}

function statusWord(code: number) {
  const words: Record<number, string> = {
    200: "OK", 201: "Created", 204: "No content", 301: "Moved", 302: "Found", 304: "Not modified",
    307: "Redirect", 308: "Redirect", 400: "Bad request", 401: "Unauthorized", 403: "Forbidden",
    404: "Not found", 405: "Method", 408: "Timeout", 429: "Too many", 500: "Server error",
    502: "Bad gateway", 503: "Unavailable", 504: "Gateway timeout",
  }
  return words[code] ?? ""
}
