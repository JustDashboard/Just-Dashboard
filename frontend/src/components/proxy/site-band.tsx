"use client"

import { Connection } from "@/components/icons"
import { BarReading } from "@/components/bar-reading"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { DOT_COLOR } from "@/components/status-dot"
import { NumberTicker } from "@/components/ui/number-ticker"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { siteProduct } from "@/components/proxy/marks"
import { activeOwner } from "@/components/proxy/site-details"
import { ProjectFavicon, ProjectMarkOf } from "@/components/project-mark"
import {
  certCounts,
  certRows,
  certTone,
  errorTone,
  siteHue,
  trafficShares,
  type SiteApp,
} from "@/components/proxy/site-apps"
import type { SiteChip } from "@/components/proxy/site-filters"
import { sitePath } from "@/components/proxy/site-verbs"
import { upstreamLabel, upstreamTone } from "@/components/proxy/upstream-health"
import { compactCount, plural } from "@/lib/format"
import type { Certificate, SiteUpstreamHealth, SitesTraffic, VHost } from "@/lib/types"
import { cn } from "@/lib/utils"

/** How many things each block names; the rest are counted at its strip's end. */
const SHOWN = 5

/**
 * Where the host's sites stand, as three bars over the list: who carried the
 * last hour's requests, how each certificate stands, and the applications the
 * sites stand in front of with whether each answers.
 *
 * It replaced four tiles — sites, on TLS, plain HTTP, disabled — that each
 * gave one count and none of which said *which*. Here every site is drawn in
 * its own hue, the one its hour bar carries, every application as its own
 * logo, and a press opens the site it is about. Each block is a bar and one
 * line, so it is one height with a reading or without, and whether Traffic is
 * there at all is decided by the engine rather than by the summary having
 * answered, so the list under the band does not move as readings land. They
 * are polled, so nothing here is marked live.
 */
export function SiteBand({
  hosts,
  appOf,
  healthOf,
  traffics,
  traffic,
  trafficError,
  certs,
  certsLoading,
  onChip,
}: {
  hosts: VHost[]
  appOf: (v: VHost) => SiteApp | undefined
  healthOf: (v: VHost) => SiteUpstreamHealth[]
  /** This host's engine keeps the access records the summary reads. */
  traffics: boolean
  traffic?: SitesTraffic
  trafficError?: Error
  certs?: Certificate[]
  certsLoading: boolean
  onChip: (chip: SiteChip) => void
}) {
  return (
    <div
      data-slot="site-band"
      className={cn("grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2", traffics && "xl:grid-cols-3")}
    >
      {traffics && (
        <TrafficBlock hosts={hosts} traffic={traffic} error={trafficError} appOf={appOf} />
      )}
      <CertificatesBlock hosts={hosts} certs={certs} loading={certsLoading} onChip={onChip} />
      <ApplicationsBlock hosts={hosts} appOf={appOf} healthOf={healthOf} onChip={onChip} />
    </div>
  )
}

/** A block's figure in its header: a number that glides to each new reading, and its word. */
function HeadFigure({ value, word }: { value: number; word: string }) {
  return (
    <span className="numeric flex h-7 items-center gap-1 text-hint whitespace-nowrap text-muted-foreground">
      <span className="font-medium text-foreground">
        <NumberTicker value={value} />
      </span>
      {word}
    </span>
  )
}

/** Words for the end of a block's strip, cut to what fits with the whole of them on a pointer. */
function Note({ parts }: { parts: (string | false)[] }) {
  const words = parts.filter(Boolean).join(" · ")
  if (!words) return null
  return (
    <span className="min-w-0 truncate" title={words}>
      {words}
    </span>
  )
}

function TrafficBlock({
  hosts,
  traffic,
  error,
  appOf,
}: {
  hosts: VHost[]
  traffic?: SitesTraffic
  error?: Error
  appOf: (v: VHost) => SiteApp | undefined
}) {
  const shares = trafficShares(hosts, traffic)
  const shown = (shares?.busy ?? []).slice(0, SHOWN)
  const total = shares?.total ?? 0
  const rest = total - shown.reduce((sum, s) => sum + s.reading.requests, 0)
  return (
    <BarReading
      title="Traffic"
      label="Traffic"
      list="Busiest sites"
      className="lg:col-span-2 xl:col-span-1"
      trail
      rest={rest}
      restLabel="Other sites"
      format={(value) => `${compactCount(value)} requests`}
      figure={shares && <HeadFigure value={total} word="requests in the hour" />}
      message={
        shares ? (
          total === 0 ? (
            "No site served a request in the last hour."
          ) : undefined
        ) : !traffic && !error ? (
          <TextShimmer>{"Reading the sites' access logs…"}</TextShimmer>
        ) : (
          "The traffic summary did not answer."
        )
      }
      note={
        shares && (
          <Note
            parts={[
              shares.busy.length > shown.length &&
                `${shares.busy.length - shown.length} more served requests`,
              shares.quiet > 0 && `${plural(shares.quiet, "site")} served none`,
              shares.unread > 0 && `${plural(shares.unread, "log")} not readable`,
            ]}
          />
        )
      }
      parts={shown.map(({ vhost, reading }) => {
        const tone = errorTone(reading.errorRate)
        const app = appOf(vhost)
        return {
          key: vhost.name,
          value: reading.requests,
          color: siteHue(vhost.name),
          mark: (
            <ProjectFavicon
              projectId={activeOwner(vhost)?.projectId}
              className="size-3.5"
              fallback={<ProductGlyph id={app?.product ?? siteProduct(vhost)} />}
            />
          ),
          name: vhost.name,
          detail: tone && (
            <span
              title="Answered with a 5xx"
              className={tone === "danger" ? "text-destructive" : "text-warning"}
            >
              {(reading.errorRate * 100).toFixed(1)}% 5xx
            </span>
          ),
          figure: compactCount(reading.requests),
          label: `${vhost.name} ${compactCount(reading.requests)} requests`,
          title: `${reading.requests.toLocaleString()} requests in the last hour`,
          href: { label: `Open ${vhost.name}`, to: sitePath(vhost.name) },
        }
      })}
    />
  )
}

/** A count in the strip, standing for a standing rather than for one thing. */
function CountMark({ count }: { count: number }) {
  return <span className="numeric text-hint font-medium">{count}</span>
}

type CertStanding = keyof ReturnType<typeof certCounts>

function certStanding(cert: Certificate): CertStanding {
  const tone = certTone(cert)
  return tone === "danger" ? "expired" : tone === "warning" ? "due" : "valid"
}

/**
 * Each standing's span and word, the worst first: a fixed bar's first part is
 * the one its line reads at rest, and that is the one worth reading.
 */
const CERT_STANDINGS: { standing: CertStanding; color: string; word: string }[] = [
  { standing: "expired", color: "var(--destructive)", word: "expired" },
  { standing: "due", color: "var(--warning)", word: "renewal due" },
  { standing: "valid", color: "var(--success)", word: "valid" },
]

/** How long a certificate has left, in the few words the line's figure has room for. */
function daysLeft(cert: Certificate) {
  if (cert.expired || cert.daysLeft < 0) return "expired"
  return cert.daysLeft === 0 ? "ends today" : `${cert.daysLeft} d left`
}

/**
 * A press in a band's note, which narrows the list to what it names. The note
 * cuts what leaves its box, so a press box grown past the words would be cut
 * with it: the button is the full 28px of the strip's line instead, and the
 * words sit where they did.
 */
const NOTE_BUTTON =
  "inline-flex h-7 shrink-0 items-center rounded-sm focus-ring hover:underline hover:underline-offset-2"

function CertificatesBlock({
  hosts,
  certs,
  loading,
  onChip,
}: {
  hosts: VHost[]
  certs?: Certificate[]
  loading: boolean
  onChip: (chip: SiteChip) => void
}) {
  const { rows, plain, unknown, tls } = certRows(hosts, certs)
  const counts = certCounts(rows)
  const enabled = hosts.filter((v) => v.enabled).length
  // In the note it is the line's full height; in the message it is words in a
  // sentence, which keep the sentence's own line.
  const plainButton = (inNote: boolean) =>
    plain.length > 0 && (
      <button
        type="button"
        onClick={() => onChip("plain")}
        className={cn(
          inNote
            ? NOTE_BUTTON
            : "shrink-0 rounded-sm focus-ring hover:underline hover:underline-offset-2",
          "text-warning",
        )}
      >
        {plural(plain.length, "site")} on plain HTTP
      </button>
    )
  return (
    <BarReading
      title="Certificates"
      label="Certificates"
      list="Certificates by expiry"
      fixed
      figure={<HeadFigure value={tls} word={`of ${enabled} on TLS`} />}
      message={
        rows.length > 0 ? undefined : tls === 0 ? (
          <>No enabled site is on TLS. {plainButton(false)}</>
        ) : !certs && loading ? (
          <TextShimmer>Reading the certificate inventory…</TextShimmer>
        ) : !certs ? (
          "The certificate inventory did not answer."
        ) : (
          "The certificate inventory has none of the certificates these sites name."
        )
      }
      note={
        <>
          {plainButton(true)}
          <Note
            parts={[unknown > 0 && `${unknown} with a certificate the inventory does not read`]}
          />
        </>
      }
      parts={CERT_STANDINGS.filter(({ standing }) => counts[standing] > 0).map(
        ({ standing, color, word }) => {
          // The rows run soonest first, so this is the one to read about.
          const { vhost, cert } = rows.find((row) => certStanding(row.cert) === standing)!
          const count = counts[standing]
          const tone = certTone(cert)
          return {
            key: standing,
            value: count,
            color,
            mark: <CountMark count={count} />,
            name: `${count} ${word}`,
            detail: vhost.name,
            figure: (
              <span
                className={cn(
                  "text-hint font-normal",
                  tone === "danger"
                    ? "text-destructive"
                    : tone === "warning"
                      ? "text-warning"
                      : "text-muted-foreground",
                )}
              >
                {daysLeft(cert)}
              </span>
            ),
            label: `${count} ${word}`,
            title: `${vhost.name}: ${daysLeft(cert)}`,
            href: { label: `Open ${vhost.name}`, to: sitePath(vhost.name) },
          }
        },
      )}
    />
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
  for (const vhost of hosts) {
    if (!vhost.enabled) continue
    const app = appOf(vhost)
    if (!app) continue
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
  const more = fronted.length - shown.length
  const down = fronted.filter((f) => f.health && upstreamTone(f.health.state) === "danger").length
  return (
    <BarReading
      title="Applications"
      label="Applications"
      list="Applications behind the sites"
      // Those that do not answer lead, and the line reads the worst at rest.
      fixed
      rest={more}
      restLabel="Other applications"
      format={(value) => plural(value, "application")}
      figure={<HeadFigure value={fronted.length} word="behind the sites" />}
      message={shown.length === 0 ? "No enabled site proxies to an application." : undefined}
      note={
        <>
          {down > 0 && (
            <button
              type="button"
              onClick={() => onChip("down")}
              className={cn(NOTE_BUTTON, "text-destructive")}
            >
              {down === 1 ? "1 refuses connections" : `${down} refuse connections`}
            </button>
          )}
          <Note parts={[more > 0 && `${more} more`]} />
        </>
      }
      parts={shown.map(({ app, sites, health }) => {
        const name = app.name ?? app.address
        const tone = health ? upstreamTone(health.state) : undefined
        const reading = health ? upstreamLabel(health) : "not checked"
        return {
          key: app.address,
          value: 1,
          color: tone ? DOT_COLOR[tone] : "var(--muted-foreground)",
          mark: (
            <ProjectMarkOf
              projectId={sites.map(activeOwner).find(Boolean)?.projectId}
              size="xs"
              className="size-3.5"
              fallback={
                hasProductLogo(app.product) ? (
                  <ProductGlyph id={app.product} />
                ) : (
                  <Connection aria-hidden className="size-3.5 text-muted-foreground" />
                )
              }
            />
          ),
          name,
          detail: sites.length === 1 ? sites[0].name : `${sites[0].name} +${sites.length - 1}`,
          figure: (
            <span
              className={cn(
                "text-hint",
                tone === "danger"
                  ? "text-destructive"
                  : tone === "warning"
                    ? "text-warning"
                    : "font-normal text-muted-foreground",
              )}
            >
              {reading}
            </span>
          ),
          label: `${name} ${reading}`,
          title: app.name ? app.address : undefined,
          href: { label: `Open ${sites[0].name}`, to: sitePath(sites[0].name) },
        }
      })}
    />
  )
}
