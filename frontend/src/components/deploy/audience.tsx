"use client"

import { ArrowRight, DesktopDevice, Globe, PhoneDevice, TabletDevice } from "@/components/icons"
import { cn } from "@/lib/utils"
import { plural, relativeTime, sharePercent, timestamp } from "@/lib/format"
import type { RequestAudience, RequestFacet, RequestVisitor } from "@/lib/types"
import { agentProduct, networkOf, parseAgent, refererProduct, systemProduct } from "@/lib/clients"
import { hueFor, LANES } from "@/lib/hue"
import { BadgedLogo, NETWORK_GLYPH } from "@/components/client-mark"
import { ProductGlyph } from "@/components/product-logo"
import { RankList, SplitBar } from "@/components/rank-list"
import { Address, RequestPath } from "@/components/deploy/request-marks"
import { Facet } from "@/components/deploy/traffic-facets"

const DEVICE = {
  desktop: { label: "Desktop", glyph: DesktopDevice, color: "var(--tag-blue)" },
  phone: { label: "Phone", glyph: PhoneDevice, color: "var(--tag-violet)" },
  tablet: { label: "Tablet", glyph: TabletDevice, color: "var(--tag-cyan)" },
} as const

type DeviceKind = keyof typeof DEVICE

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
  const rows = facets.slice(0, 5).map((f) => ({ facet: f, drawn: item(f) }))
  return (
    <div className="min-w-0">
      <p className="mb-2 text-hint text-muted-foreground">{title}</p>
      {rows.length > 0 && (
        <SplitBar
          className="mb-1.5"
          parts={rows.map(({ facet, drawn }) => ({
            key: facet.value,
            count: facet.count,
            color: drawn.color ?? "var(--tag-slate)",
          }))}
          label={rows
            .map(({ facet, drawn }) => `${sharePercent(facet.count / total)} ${drawn.label}`)
            .join(", ")}
        />
      )}
      <RankList
        items={rows.map(({ facet, drawn }) => ({
          key: facet.value,
          swatch: drawn.color ?? "var(--tag-slate)",
          mark: drawn.mark,
          mono: false,
          label: drawn.label,
          value: total ? sharePercent(facet.count / total) : facet.count.toLocaleString(),
          hint: plural(facet.count, "person", "people"),
        }))}
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
      className="grid w-full min-w-0 grid-cols-start-main-end items-center gap-x-3 gap-y-1 rounded-sm px-2 py-2 text-left focus-ring-inset transition-colors hover:bg-row-hover lg:grid-cols-[auto_minmax(12rem,16rem)_minmax(0,1fr)_auto]"
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
              <span aria-hidden className="text-faint">
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
