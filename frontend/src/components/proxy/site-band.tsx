"use client"

import Link from "next/link"
import { Connection } from "@/components/icons"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, ProductLogo, ProductLogos } from "@/components/product-logo"
import { ShareBar } from "@/components/procs/workloads"
import { Status } from "@/components/status-dot"
import { NumberTicker } from "@/components/ui/number-ticker"
import { certificateProduct, siteProduct } from "@/components/proxy/marks"
import {
  certRunway,
  errorTone,
  placeOnRunway,
  runwayTone,
  siteHue,
  trafficShares,
  type SiteApp,
} from "@/components/proxy/site-apps"
import type { SiteChip } from "@/components/proxy/site-filters"
import { sitePath } from "@/components/proxy/site-verbs"
import { upstreamLabel, upstreamTone } from "@/components/proxy/upstream-health"
import { plural } from "@/lib/format"
import type { Certificate, SiteUpstreamHealth, SitesTraffic, VHost } from "@/lib/types"
import { cn } from "@/lib/utils"

/** How many rows each block names; the rest are counted under it. */
const SHOWN = 5

const compact = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 })

/**
 * A site as the things it is made of: the application it hands requests to,
 * drawn as itself, tucked over the engine that serves it. A site that
 * proxies nothing — files, a redirect — or whose application this host
 * cannot name is its engine alone, never a guessed logo (§14).
 */
export function SiteMark({
  vhost,
  app,
  size = "md",
}: {
  vhost: VHost
  app?: SiteApp
  size?: "sm" | "md"
}) {
  const engine = siteProduct(vhost)
  if (!app?.product || app.product === engine) return <ProductLogo id={engine} size={size} />
  return <ProductLogos ids={[app.product, engine]} size={size} ring="ring-(--choice-surface)" />
}

/**
 * Where the host's sites stand, as three blocks over the list: who carried
 * the last hour's requests, how long each certificate has left, and the
 * applications the sites stand in front of with whether each answers.
 *
 * It replaced four tiles — sites, on TLS, plain HTTP, disabled — that each
 * gave one count and none of which said *which*. Here every site is drawn in
 * the hue its card's edge carries, every application as its own logo, and a
 * row opens the site it is about.
 */
export function SiteBand({
  hosts,
  appOf,
  healthOf,
  traffic,
  certs,
  onChip,
}: {
  hosts: VHost[]
  appOf: (v: VHost) => SiteApp | undefined
  healthOf: (v: VHost) => SiteUpstreamHealth[]
  /** Absent where the traffic summary does not answer: the block is not drawn. */
  traffic?: SitesTraffic
  certs?: Certificate[]
  onChip: (chip: SiteChip) => void
}) {
  const shares = trafficShares(hosts, traffic)
  return (
    <div
      data-slot="site-band"
      className={cn("grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2", shares && "xl:grid-cols-3")}
    >
      {shares && <TrafficBlock shares={shares} appOf={appOf} />}
      <CertificatesBlock hosts={hosts} certs={certs} onChip={onChip} wide={!shares} />
      <ApplicationsBlock hosts={hosts} appOf={appOf} healthOf={healthOf} onChip={onChip} />
    </div>
  )
}

/** The key that finds a site's span on a bar, in the hue its card carries. */
function HueKey({ name }: { name: string }) {
  return (
    <span
      aria-hidden
      className="h-2.5 w-0.5 shrink-0 rounded-full"
      style={{ background: siteHue(name) }}
    />
  )
}

/** A block's figure in its header: a number that glides to each new reading, and its word. */
function HeadFigure({ value, word }: { value: number; word: string }) {
  return (
    <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
      <span className="font-medium text-foreground">
        <NumberTicker value={value} />
      </span>
      {word}
    </span>
  )
}

/** The line under a block saying what it left out, or nothing when it left nothing out. */
function Remainder({ parts }: { parts: React.ReactNode[] }) {
  const shown = parts.filter(Boolean)
  if (shown.length === 0) return null
  return (
    <p className="flex flex-wrap items-center gap-x-2 gap-y-1 border-t border-hairline pt-3 text-hint text-muted-foreground">
      {shown.map((part, i) => (
        <span key={i} className="inline-flex items-center gap-2">
          {i > 0 && <span className="text-muted-foreground/40">·</span>}
          {part}
        </span>
      ))}
    </p>
  )
}

const rowClass =
  "flex h-9 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body focus-ring-inset transition-colors hover:bg-row-hover"

function TrafficBlock({
  shares,
  appOf,
}: {
  shares: NonNullable<ReturnType<typeof trafficShares>>
  appOf: (v: VHost) => SiteApp | undefined
}) {
  const { busy, total, quiet, unread } = shares
  const shown = busy.slice(0, SHOWN)
  const rest = total - shown.reduce((sum, s) => sum + s.reading.requests, 0)
  const top = shown[0]?.reading.requests ?? 0
  return (
    <Panel plain aria-label="Traffic" className="lg:col-span-2 xl:col-span-1">
      <PanelHeader
        title="Traffic"
        actions={<HeadFigure value={total} word="requests in the hour" />}
      />
      <PanelBody className="space-y-3 pt-4">
        {total === 0 ? (
          <p className="py-2 text-body text-muted-foreground">
            No site served a request in the last hour.
          </p>
        ) : (
          <>
            <ShareBar
              label="Requests in the last hour"
              capacity={total}
              rest={rest}
              parts={shown.map(({ vhost, reading }) => ({
                key: vhost.name,
                value: reading.requests,
                color: siteHue(vhost.name),
                label: `${vhost.name} ${compact.format(reading.requests)} requests`,
              }))}
              format={(value) => `${compact.format(value)} requests`}
            />
            <ul className="-mx-2">
              {shown.map(({ vhost, reading }) => {
                const tone = errorTone(reading.errorRate)
                const app = appOf(vhost)
                return (
                  <li key={vhost.name}>
                    <Link
                      href={sitePath(vhost.name)}
                      aria-label={`Open ${vhost.name}`}
                      title={`${reading.requests.toLocaleString()} requests in the last hour`}
                      className={rowClass}
                    >
                      <HueKey name={vhost.name} />
                      <span className="flex size-4 shrink-0 items-center justify-center">
                        <ProductGlyph id={app?.product ?? siteProduct(vhost)} />
                      </span>
                      <span className="min-w-0 flex-1 truncate font-medium">{vhost.name}</span>
                      {tone && (
                        <span
                          className={cn(
                            "numeric shrink-0 text-hint",
                            tone === "danger" ? "text-destructive" : "text-warning",
                          )}
                          title="Answered with a 5xx"
                        >
                          {(reading.errorRate * 100).toFixed(1)}% 5xx
                        </span>
                      )}

                      <span className="flex w-16 shrink-0 items-center justify-end gap-2">
                        <span className="numeric font-medium">
                          {compact.format(reading.requests)}
                        </span>
                      </span>
                      <span
                        aria-hidden
                        className="hidden h-1 w-12 shrink-0 overflow-hidden rounded-full bg-meter-track sm:block"
                      >
                        <span
                          className="block h-full rounded-full transition-[width] duration-700 ease-out"
                          style={{
                            width: `${top > 0 ? (reading.requests / top) * 100 : 0}%`,
                            background: siteHue(vhost.name),
                          }}
                        />
                      </span>
                    </Link>
                  </li>
                )
              })}
            </ul>
          </>
        )}
        <Remainder
          parts={[
            busy.length > shown.length && `${busy.length - shown.length} more served requests`,
            quiet > 0 && `${plural(quiet, "site")} served none`,
            unread > 0 && `${plural(unread, "log")} not readable`,
          ]}
        />
      </PanelBody>
    </Panel>
  )
}

function CertificatesBlock({
  hosts,
  certs,
  onChip,
  wide,
}: {
  hosts: VHost[]
  certs?: Certificate[]
  onChip: (chip: SiteChip) => void
  /** The block stands where Traffic would on a host without it. */
  wide: boolean
}) {
  const { rows, plain, unknown, tls } = certRunway(hosts, certs)
  const shown = rows.slice(0, SHOWN)
  const enabled = hosts.filter((v) => v.enabled).length
  return (
    <Panel plain aria-label="Certificates" className={cn(wide && "lg:col-span-1")}>
      <PanelHeader
        title="Certificates"
        actions={<HeadFigure value={tls} word={`of ${enabled} on TLS`} />}
      />
      <PanelBody className="pt-1">
        {shown.length === 0 ? (
          <p className="py-3 text-body text-muted-foreground">
            {tls === 0
              ? "No enabled site is on TLS."
              : "The certificate inventory has none of the certificates these sites name."}
          </p>
        ) : (
          <ol aria-label="Certificate runway">
            {shown.map(({ vhost, cert }, index) => (
              <RunwayRow key={vhost.name} vhost={vhost} cert={cert} first={index === 0} />
            ))}
          </ol>
        )}
        <Remainder
          parts={[
            rows.length > shown.length && `${rows.length - shown.length} more`,
            plain.length > 0 && (
              <button
                type="button"
                onClick={() => onChip("plain")}
                className="rounded-sm text-warning focus-ring hover:underline hover:underline-offset-2"
              >
                {plural(plain.length, "site")} on plain HTTP
              </button>
            ),
            unknown > 0 && `${unknown} with a certificate the inventory does not read`,
          ]}
        />
      </PanelBody>
    </Panel>
  )
}

/**
 * One certificate on an axis from "ends now" to a full ninety days: a dot in
 * the site's hue, the stretch it has left drawn back to the start, the last
 * fortnight tinted amber so a dot that has crossed into it reads before its
 * figure does.
 */
function RunwayRow({ vhost, cert, first }: { vhost: VHost; cert: Certificate; first: boolean }) {
  const tone = runwayTone(cert)
  const left = placeOnRunway(cert.daysLeft)
  const color = siteHue(vhost.name)
  const issuer = certificateProduct(cert)
  const days =
    cert.expired || cert.daysLeft < 0
      ? "expired"
      : cert.daysLeft === 0
        ? "ends today"
        : `${cert.daysLeft} d left`
  return (
    <li className={cn("-mx-2", !first && "border-t border-hairline")}>
      <Link
        href={sitePath(vhost.name)}
        aria-label={`Open ${vhost.name}`}
        className="block rounded-md px-2 py-2 focus-ring-inset transition-colors hover:bg-row-hover"
      >
        <span className="flex min-w-0 items-center gap-2">
          <span className="min-w-0 flex-1 truncate text-body font-medium">{vhost.name}</span>
          {issuer && <ProductGlyph id={issuer} />}
          <span
            className={cn(
              "numeric shrink-0 text-hint",
              tone === "danger"
                ? "text-destructive"
                : tone === "warning"
                  ? "text-warning"
                  : "text-muted-foreground",
            )}
          >
            {days}
          </span>
        </span>
        <span
          role="img"
          aria-label={`${vhost.name}: ${days}`}
          className="relative mt-1.5 block h-2.5"
        >
          <span
            aria-hidden
            className="absolute inset-y-0 left-0 rounded-sm bg-wash-warning"
            style={{ width: `${placeOnRunway(14)}%` }}
          />
          <span aria-hidden className="absolute inset-x-0 top-1/2 h-px bg-hairline" />
          <span
            aria-hidden
            className="absolute top-1/2 left-0 h-px -translate-y-1/2 transition-[width] duration-700 ease-out"
            style={{
              width: `${left}%`,
              background: `color-mix(in oklab, ${color} 60%, transparent)`,
            }}
          />
          <span
            aria-hidden
            className="absolute top-1/2 size-2.5 -translate-x-1/2 -translate-y-1/2 rounded-full transition-[left] duration-700 ease-out"
            style={{
              left: `${left}%`,
              background: tone === "danger" ? "var(--destructive)" : color,
            }}
          />
        </span>
      </Link>
    </li>
  )
}

/** One application and the sites in front of it, keyed by where nginx dials it. */
type Fronted = { app: SiteApp; sites: VHost[]; health?: SiteUpstreamHealth }

function ApplicationsBlock({
  hosts,
  appOf,
  healthOf,
  onChip,
}: {
  hosts: VHost[]
  appOf: (v: VHost) => SiteApp | undefined
  healthOf: (v: VHost) => SiteUpstreamHealth[]
  onChip: (chip: SiteChip) => void
}) {
  const byAddress = new Map<string, Fronted>()
  let files = 0
  for (const vhost of hosts) {
    if (!vhost.enabled) continue
    const app = appOf(vhost)
    if (!app) {
      if (vhost.path) files++
      continue
    }
    const entry = byAddress.get(app.address) ?? { app, sites: [] }
    entry.sites.push(vhost)
    entry.health ??= healthOf(vhost).find((t) => t.address === app.address)
    byAddress.set(app.address, entry)
  }
  const rank = (f: Fronted) =>
    f.health
      ? upstreamTone(f.health.state) === "danger"
        ? 0
        : upstreamTone(f.health.state) === "warning"
          ? 1
          : 2
      : 3
  const fronted = [...byAddress.values()].sort(
    (a, b) =>
      rank(a) - rank(b) || (a.app.name ?? a.app.address).localeCompare(b.app.name ?? b.app.address),
  )
  const shown = fronted.slice(0, SHOWN)
  const down = fronted.filter((f) => f.health && upstreamTone(f.health.state) === "danger").length
  return (
    <Panel plain aria-label="Applications">
      <PanelHeader
        title="Applications"
        actions={<HeadFigure value={fronted.length} word="behind the sites" />}
      />
      <PanelBody className="pt-1">
        {shown.length === 0 ? (
          <p className="py-3 text-body text-muted-foreground">
            No enabled site proxies to an application.
          </p>
        ) : (
          <ul className="-mx-2">
            {shown.map(({ app, sites, health }, index) => (
              <li key={app.address} className={cn(index > 0 && "border-t border-hairline")}>
                <Link
                  href={sitePath(sites[0].name)}
                  aria-label={`Open ${sites[0].name}`}
                  className="flex min-w-0 items-center gap-2.5 rounded-md px-2 py-2 focus-ring-inset transition-colors hover:bg-row-hover"
                >
                  <ProductLogo id={app.product} size="sm" fallback={Connection} />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-body font-medium">
                      {app.name ?? app.address}
                    </span>
                    <span className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
                      {app.name && <span className="shrink-0 font-mono">{app.address}</span>}
                      {app.name && <span className="text-muted-foreground/40">·</span>}
                      <span className="inline-flex min-w-0 items-center gap-1.5">
                        <HueKey name={sites[0].name} />
                        <span className="truncate">
                          {sites.length === 1
                            ? sites[0].name
                            : `${sites[0].name} +${sites.length - 1}`}
                        </span>
                      </span>
                    </span>
                  </span>
                  <span className="shrink-0 text-hint">
                    {health ? (
                      <Status tone={upstreamTone(health.state)} label={upstreamLabel(health)} />
                    ) : (
                      <span className="text-muted-foreground">not checked</span>
                    )}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        )}
        <Remainder
          parts={[
            fronted.length > shown.length && `${fronted.length - shown.length} more`,
            down > 0 && (
              <button
                type="button"
                onClick={() => onChip("down")}
                className="rounded-sm text-destructive focus-ring hover:underline hover:underline-offset-2"
              >
                {down === 1 ? "1 refuses connections" : `${down} refuse connections`}
              </button>
            ),
            files > 0 &&
              (files === 1
                ? "1 site serves files or redirects"
                : `${files} sites serve files or redirect`),
          ]}
        />
      </PanelBody>
    </Panel>
  )
}
