"use client"

import { ArrowRight, DesktopDevice, Globe, PhoneDevice, TabletDevice } from "@/components/icons"
import { cn } from "@/lib/utils"
import { plural, relativeTime, timestamp } from "@/lib/format"
import type { RequestAudience, RequestFacet, RequestSummary, RequestVisitor } from "@/lib/types"
import { agentProduct, networkOf, parseAgent, refererProduct, systemProduct } from "@/lib/clients"
import { hueFor, LANES } from "@/lib/hue"
import { BarList } from "@/components/bar-list"
import { BadgedLogo, NETWORK_GLYPH } from "@/components/client-mark"
import { TileTrend } from "@/components/metrics/sparkline"
import { ProductGlyph } from "@/components/product-logo"
import { StatButton, StatGrid, StatTile } from "@/components/stat-tile"
import { NumberTicker } from "@/components/ui/number-ticker"
import { Address, RequestPath } from "@/components/deploy/request-marks"
import { Facet } from "@/components/deploy/traffic-facets"

const DEVICE = {
  desktop: { label: "Desktop", glyph: DesktopDevice, color: "var(--tag-blue)" },
  phone: { label: "Phone", glyph: PhoneDevice, color: "var(--tag-violet)" },
  tablet: { label: "Tablet", glyph: TabletDevice, color: "var(--tag-cyan)" },
} as const

type DeviceKind = keyof typeof DEVICE

/**
 * Who the window's traffic was, as the five figures a person asks of a
 * website's log first: how many real people came, what they opened, how much
 * of the rest was bots, where the people came from and what they came on.
 * Insights opened on two charts and a response-time rule, and the one number
 * everybody wants — how many people visited — was nowhere on it; the lists
 * under them named addresses, and an address is not a person.
 *
 * The visitors are the server's count (`summary.audience`): an address and
 * the browser it sent, once it opened a page, so a crawler, a script and a
 * scanner's probe are never one. Rates, failures and response times are the
 * page's tiles above the pane and the chart under this, so they are not said
 * a third time here. Each figure counts up as it lands; page views narrow the
 * window to themselves on a press, as the field's switch does.
 */
export function AudienceReadings({
  summary,
  pages,
  onPages,
}: {
  summary: RequestSummary
  /** Whether the window is narrowed to page views. */
  pages: boolean
  onPages: () => void
}) {
  const audience = summary.audience
  if (!audience) return null
  const buckets = summary.buckets
  const perVisitor = audience.visitors ? summary.pages / audience.visitors : 0
  const botShare = summary.total ? audience.botRequests / summary.total : 0
  const source = summary.referers[0]
  const site = source ? refererProduct(source.value) : undefined
  const devices = audience.devices ?? []
  const people = devices.reduce((n, d) => n + d.count, 0)
  const lead = devices[0]
  return (
    <StatGrid columns={5} dense className="shrink-0 border-b border-hairline">
      <StatTile
        label="Visitors"
        value={<NumberTicker value={audience.visitors} />}
        trailing={audience.visitors === 1 ? "person" : "people"}
        trend={
          <TileTrend
            values={buckets.map((b) => b.visitors ?? 0)}
            label="People across the window"
            color="var(--tag-green)"
          />
        }
        hint={audience.visitors === 0 ? "nobody opened a page" : "browsers that opened a page"}
      />
      <StatButton label="Show only page views" pressed={pages} onClick={onPages}>
        <StatTile
          label="Page views"
          value={<NumberTicker value={summary.pages} />}
          trend={
            <TileTrend
              values={buckets.map((b) => b.total)}
              label="Requests across the window"
              color="var(--chart-1)"
            />
          }
          hint={
            perVisitor > 0
              ? `${perVisitor.toFixed(1)} per visitor · of ${plural(summary.total, "request")}`
              : `of ${plural(summary.total, "request")}`
          }
          className="group-hover:bg-row-hover"
        />
      </StatButton>
      <StatTile
        label="Bots and scripts"
        value={<NumberTicker value={audience.bots} />}
        trailing={audience.bots === 1 ? "address" : "addresses"}
        meter={botShare > 0 ? botShare * 100 : undefined}
        meterLabel="Share of requests from bots"
        hint={
          audience.botRequests > 0
            ? `${Math.round(botShare * 100)}% of the requests`
            : "none in this window"
        }
      />
      <StatTile
        label="Came from"
        value={
          source ? (
            <span className="flex min-w-0 items-center gap-2">
              {site ? (
                <ProductGlyph id={site} className="size-5" />
              ) : (
                <Globe className="size-5 shrink-0 text-muted-foreground" />
              )}
              <span className="truncate text-xl">{source.value}</span>
            </span>
          ) : (
            "Direct"
          )
        }
        hint={
          source
            ? `${plural(source.count, "request")} · ${plural(summary.referers.length, "site")} in all`
            : "no outside site sent anyone"
        }
      />
      <StatTile
        label="On"
        value={
          lead && people > 0 ? (
            <>
              <NumberTicker value={Math.round((lead.count / people) * 100)} />%
            </>
          ) : (
            "—"
          )
        }
        trailing={
          lead ? (DEVICE[lead.value as DeviceKind]?.label ?? lead.value).toLowerCase() : undefined
        }
        trend={people > 0 ? <DeviceSplit devices={devices} people={people} /> : undefined}
        hint={
          devices.length > 1
            ? devices
                .slice(1)
                .map(
                  (d) =>
                    `${Math.round((d.count / people) * 100)}% ${(DEVICE[d.value as DeviceKind]?.label ?? d.value).toLowerCase()}`,
                )
                .join(" · ")
            : people > 0
              ? "every visitor"
              : "no devices recorded"
        }
      />
    </StatGrid>
  )
}

/** The devices as one bar in their colours, desktop, phone and tablet side by side. */
function DeviceSplit({ devices, people }: { devices: RequestFacet[]; people: number }) {
  return (
    <span
      role="img"
      aria-label={devices
        .map((d) => `${Math.round((d.count / people) * 100)}% ${d.value}`)
        .join(", ")}
      className="flex h-full items-center"
    >
      <span className="flex h-2 w-full gap-0.5 overflow-hidden rounded-full">
        {devices.map((d) => (
          <span
            key={d.value}
            className="h-full transition-[width] duration-500 ease-out first:rounded-l-full last:rounded-r-full"
            style={{
              width: `${(d.count / people) * 100}%`,
              backgroundColor: DEVICE[d.value as DeviceKind]?.color ?? "var(--tag-slate)",
            }}
          />
        ))}
      </span>
    </span>
  )
}

/**
 * Who the people were. The devices, browsers and systems they came on, each
 * counted once a person rather than once a request — a phone that fetched
 * forty assets is one phone — and the most active of them as rows: the
 * browser on its system, the device, where the address sits, the pages they
 * opened in order, the site that sent them and when they were last seen. A
 * row narrows the window to that address, the way an address row does.
 */
export function Visitors({
  audience,
  onFilterClient,
  index,
}: {
  audience: RequestAudience
  onFilterClient: (ip: string) => void
  index: number
}) {
  const devices = audience.devices ?? []
  const browsers = audience.browsers ?? []
  const systems = audience.systems ?? []
  const top = audience.top ?? []
  return (
    <Facet
      title="Visitors"
      reading={
        <>
          <span className="font-medium text-foreground">
            {plural(audience.visitors, "person", "people")}
          </span>
          {audience.bots > 0 && ` · ${plural(audience.bots, "bot")} left out`}
        </>
      }
      index={index}
    >
      {audience.visitors === 0 ? (
        <p className="py-3 text-hint text-muted-foreground">
          Nobody opened a page in this window — only{" "}
          {audience.bots > 0 ? "bots, scripts and assets" : "assets"} were asked for.
        </p>
      ) : (
        <div className="flex flex-col gap-6">
          <div className="grid grid-cols-1 gap-x-10 gap-y-5 sm:grid-cols-3">
            <Breakdown
              title="Devices"
              facets={devices}
              item={(f) => {
                const device = DEVICE[f.value as DeviceKind]
                const Glyph = device?.glyph ?? DesktopDevice
                return {
                  mark: <Glyph className="size-3.5" style={{ color: device?.color }} />,
                  label: device?.label ?? f.value,
                  color: device?.color,
                }
              }}
            />
            <Breakdown
              title="Browsers"
              facets={browsers}
              item={(f) => {
                const product = agentProduct(f.value).product
                return {
                  mark: product ? (
                    <ProductGlyph id={product} />
                  ) : (
                    <Globe className="size-3.5 text-muted-foreground" />
                  ),
                  label: f.value,
                  color: hueFor(f.value, LANES),
                }
              }}
            />
            <Breakdown
              title="Systems"
              facets={systems}
              item={(f) => {
                const product = systemProduct(f.value)
                return {
                  mark: product ? (
                    <ProductGlyph id={product} />
                  ) : (
                    <DesktopDevice className="size-3.5 text-muted-foreground" />
                  ),
                  label: f.value,
                  color: hueFor(f.value, LANES),
                }
              }}
            />
          </div>

          {top.length > 0 && (
            <div className="min-w-0">
              <p className="mb-1 flex items-baseline justify-between gap-3 text-hint text-muted-foreground">
                <span>Most active</span>
                <span className="numeric">
                  {top.length < audience.visitors
                    ? `top ${top.length} of ${audience.visitors.toLocaleString()}`
                    : plural(top.length, "person", "people")}
                </span>
              </p>
              <ul className="-mx-2 flex flex-col">
                {top.map((visitor) => (
                  <li key={`${visitor.client}|${visitor.agent ?? ""}`}>
                    <VisitorRow visitor={visitor} onFilterClient={onFilterClient} />
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}
    </Facet>
  )
}

function Breakdown({
  title,
  facets,
  item,
}: {
  title: string
  facets: RequestFacet[]
  item: (facet: RequestFacet) => { mark: React.ReactNode; label: string; color?: string }
}) {
  const total = facets.reduce((n, f) => n + f.count, 0)
  return (
    <div className="min-w-0">
      <p className="mb-1 text-hint text-muted-foreground">{title}</p>
      <BarList
        items={facets.slice(0, 5).map((f) => {
          const drawn = item(f)
          return {
            key: f.value,
            mark: drawn.mark,
            mono: false,
            label: drawn.label,
            color: drawn.color,
            value: total ? `${Math.round((f.count / total) * 100)}%` : f.count.toLocaleString(),
            share: total ? f.count / Math.max(...facets.map((x) => x.count), 1) : 0,
            hint: plural(f.count, "person", "people"),
          }
        })}
        emptyLabel="Not recorded."
      />
    </div>
  )
}

/**
 * One person: the browser over its system, the device and the address, the
 * pages they opened as a trail, and when. The trail is the server's journey —
 * the distinct pages in the order they were first opened — so "/ → /pricing →
 * /signup" reads as a visit rather than as three rows of a path list.
 */
function VisitorRow({
  visitor,
  onFilterClient,
}: {
  visitor: RequestVisitor
  onFilterClient: (ip: string) => void
}) {
  const agent = visitor.agent ? parseAgent(visitor.agent) : undefined
  const device = agent && agent.device !== "program" ? DEVICE[agent.device] : undefined
  const network = networkOf(visitor.client)
  const Place = NETWORK_GLYPH[network.kind]
  const site = visitor.referer ? refererProduct(visitor.referer) : undefined
  const trail = visitor.journey.slice(0, 4)
  const more = visitor.journey.length - trail.length
  const badge = agent?.product && agent.osProduct !== agent.product ? agent.osProduct : undefined
  return (
    <button
      type="button"
      onClick={() => onFilterClient(visitor.client)}
      aria-label={`Show every request from ${visitor.client}`}
      title={`First seen ${timestamp(visitor.first)} · last seen ${timestamp(visitor.last)}`}
      className="grid w-full min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 rounded-sm px-2 py-2 text-left focus-ring-inset transition-colors hover:bg-row-hover lg:grid-cols-[auto_minmax(12rem,16rem)_minmax(0,1fr)_auto]"
    >
      <BadgedLogo
        id={agent?.product ?? agent?.osProduct}
        badge={badge}
        fallback={device?.glyph ?? DesktopDevice}
      />
      <span className="flex min-w-0 flex-col">
        <span className="truncate text-xs font-medium">
          {agent ? (agent.os ? `${agent.client} on ${agent.os}` : agent.client) : "Unknown browser"}
        </span>
        <span className="flex min-w-0 items-center gap-1.5 text-micro text-muted-foreground">
          {device && (
            <>
              <device.glyph className="size-3 shrink-0" style={{ color: device.color }} />
              <span className="shrink-0">{device.label}</span>
              <span aria-hidden className="text-muted-foreground/50">
                ·
              </span>
            </>
          )}
          {network.product ? (
            <ProductGlyph id={network.product} className="size-3" />
          ) : (
            <Place aria-hidden className="size-3 shrink-0" />
          )}
          <Address ip={visitor.client} className="text-micro" />
        </span>
      </span>
      <span className="col-span-3 flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs max-lg:order-last max-lg:pl-9 lg:col-span-1">
        {visitor.referer && (
          <span className="inline-flex shrink-0 items-center gap-1 text-muted-foreground">
            {site ? (
              <ProductGlyph id={site} className="size-3" />
            ) : (
              <Globe aria-hidden className="size-3" />
            )}
            <span className="max-w-32 truncate">{visitor.referer}</span>
            <ArrowRight aria-hidden className="size-3 text-muted-foreground/60" />
          </span>
        )}
        {trail.map((path, i) => (
          <span key={path} className="inline-flex min-w-0 items-center gap-1.5">
            {i > 0 && (
              <ArrowRight aria-hidden className="size-3 shrink-0 text-muted-foreground/60" />
            )}
            <RequestPath path={path} className="max-w-48" />
          </span>
        ))}
        {more > 0 && <span className="numeric text-muted-foreground">+{more}</span>}
      </span>
      <span className="flex shrink-0 flex-col items-end text-right">
        <span className="numeric text-xs">
          <span className="font-medium">{visitor.pages}</span>
          <span className="text-muted-foreground"> {visitor.pages === 1 ? "page" : "pages"}</span>
        </span>
        <span
          className={cn(
            "numeric text-micro text-muted-foreground",
            visitor.errors > 0 && "text-destructive",
          )}
        >
          {visitor.errors > 0
            ? `${visitor.errors} failed · ${relativeTime(visitor.last)}`
            : relativeTime(visitor.last)}
        </span>
      </span>
    </button>
  )
}
