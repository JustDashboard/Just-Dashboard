"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, Box, CheckCircle } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { LANES } from "@/lib/hue"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { Certificate, ErrorReport, SiteTrafficReading } from "@/lib/types"
import { BarList, type BarListItem } from "@/components/bar-list"
import { FindingList } from "@/components/finding-list"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { Sparkline } from "@/components/metrics/sparkline"
import { ShareBar } from "@/components/procs/workloads"
import { ErrorState } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import { certificateProduct } from "@/components/proxy/marks"
import { errorFinding } from "@/components/proxy/site-errors"
import { compactCount, errorRateTone, readingHref, share } from "@/components/proxy/site-traffic"

/** How many sites and certificates a block names; the rest are one link away. */
const SHOWN = 5

/** The runway's far end: certbot issues for ninety days. */
const RUNWAY_DAYS = 90

/**
 * The readings the four tiles never gave, as three blocks under the route
 * map: which sites the hour's requests went to, how long every certificate
 * has left on one runway, and what failed. Each opens on its one figure at
 * the page's reading size, then the rows that make it up — a press opens the
 * site's requests, the certificate, the error log.
 *
 * Traffic and errors are read from every site's access record, nginx's and
 * the Docker Caddy ingress's alike, so a host where the ingress holds ports
 * 80 and 443 is read as busy as it is.
 */
export function OverviewBand({
  traffics,
  nginx,
  traffic,
  certificates,
  errors,
  productOf,
  labelOf,
}: {
  /** Some engine here writes access records the dashboard reads. */
  traffics: boolean
  /** nginx is on this host, so its error log is read too. */
  nginx: boolean
  traffic: { sites?: SiteTrafficReading[]; error?: Error }
  certificates: {
    list?: Certificate[]
    loading: boolean
    error?: Error
    onRetry: () => void
    /** certbot is on this host, so an empty list is something to issue. */
    certbot: boolean
  }
  errors: { report?: ErrorReport; error?: Error }
  /** The product a site's visitors reach, for its mark. */
  productOf: (site: string) => string | undefined
  /** The name a site's visitors type, which says more than its file's. */
  labelOf: (site: string) => string
}) {
  return (
    <div
      data-slot="overview-band"
      className={cn(
        "grid min-w-0 gap-x-10 gap-y-8 [&>*]:min-w-0",
        traffics && "lg:grid-cols-2 xl:grid-cols-3",
      )}
    >
      {traffics && <TrafficBlock {...traffic} productOf={productOf} labelOf={labelOf} />}
      <CertificatesBlock {...certificates} />
      {traffics && (
        <ErrorsBlock
          {...errors}
          nginx={nginx}
          sites={traffic.sites}
          trafficError={traffic.error}
          labelOf={labelOf}
        />
      )}
    </div>
  )
}

function HeadLink({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <Link
      href={href}
      className="flex items-center gap-1 text-hint font-medium text-muted-foreground hover:text-foreground"
    >
      {children} <ArrowRight className="size-3" />
    </Link>
  )
}

function Bones() {
  return (
    <div className="space-y-3 py-1">
      <Skeleton className="h-2.5 w-full" />
      <Skeleton className="h-4 w-48" />
      <Skeleton className="h-4 w-40" />
    </div>
  )
}

/**
 * A block's one figure, at the size a reading takes on this page, with what
 * it counts beside it and the line that qualifies it under.
 */
function Headline({
  value,
  unit,
  caption,
  tone,
}: {
  value: React.ReactNode
  unit: React.ReactNode
  caption?: React.ReactNode
  tone?: "warning" | "danger" | "success"
}) {
  return (
    <div className="min-w-0 space-y-0.5">
      <p className="flex min-w-0 items-baseline gap-2">
        <span
          className={cn(
            "numeric text-2xl leading-tight font-semibold tracking-tight",
            tone === "warning" && "text-warning",
            tone === "danger" && "text-destructive",
            tone === "success" && "text-success",
          )}
        >
          {value}
        </span>
        <span className="truncate text-hint text-muted-foreground">{unit}</span>
      </p>
      {caption && <p className="truncate text-hint text-muted-foreground">{caption}</p>}
    </div>
  )
}

/**
 * The hour's requests as one bar shared out by site, each rank a hue of its
 * own from `LANES` (none of them a state's, and no two neighbours alike, which
 * a hash of five names did not promise), then the five busiest as rows: what
 * they reach, the hour's shape, their 5xx share and their count.
 */
function TrafficBlock({
  sites,
  error,
  productOf,
  labelOf,
}: {
  sites?: SiteTrafficReading[]
  error?: Error
  productOf: (site: string) => string | undefined
  labelOf: (site: string) => string
}) {
  const router = useRouter()
  const read = (sites ?? []).filter((s) => s.status === "available")
  const ranked = [...read].sort((a, b) => b.requests - a.requests)
  const top = ranked.slice(0, SHOWN).filter((s) => s.requests > 0)
  const total = read.reduce((sum, s) => sum + s.requests, 0)
  const rest = total - top.reduce((sum, s) => sum + s.requests, 0)
  const silent = (sites ?? []).length - read.length
  const busy = ranked.filter((s) => s.requests > 0).length

  return (
    <Panel plain aria-label="Traffic">
      <PanelHeader
        title="Traffic"
        actions={<HeadLink href="/proxy/traffic">All traffic</HeadLink>}
      />
      <PanelBody className="space-y-4 pt-4">
        {error ? (
          <p className="text-hint text-muted-foreground" title={errorMessage(error)}>
            {"Couldn't read the sites' access logs."}
          </p>
        ) : !sites ? (
          <Bones />
        ) : read.length === 0 ? (
          <p className="py-2 text-body text-muted-foreground">
            No site writes an access log the dashboard can read.
          </p>
        ) : (
          <div className="animate-rise space-y-4">
            <Headline
              value={compactCount(total)}
              unit="requests this hour"
              caption={
                total === 0
                  ? `None of ${plural(read.length, "site")} answered a request`
                  : `${busy} of ${plural(read.length, "site")} answered`
              }
            />
            {total > 0 && (
              <>
                <ShareBar
                  label="Requests in the last hour"
                  capacity={total}
                  rest={rest}
                  parts={top.map((s, rank) => ({
                    key: s.site,
                    value: s.requests,
                    color: LANES[rank % LANES.length],
                    label: `${labelOf(s.site)} ${compactCount(s.requests)}`,
                  }))}
                  format={compactCount}
                />
                <ul aria-label="Busiest sites" className="-mx-2">
                  {top.map((s, rank) => {
                    const product = productOf(s.site)
                    const tone = errorRateTone(s.errorRate)
                    const label = labelOf(s.site)
                    return (
                      <li key={s.site}>
                        <button
                          type="button"
                          title={`Open ${label}'s requests`}
                          aria-label={`Open ${label}'s requests`}
                          onClick={() => router.push(readingHref(s))}
                          className="flex h-9 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body focus-ring-inset transition-colors hover:bg-row-hover"
                        >
                          <span
                            aria-hidden
                            className="h-3 w-0.5 shrink-0 rounded-full"
                            style={{ background: LANES[rank % LANES.length] }}
                          />
                          <span className="flex size-4 shrink-0 items-center justify-center">
                            {hasProductLogo(product) ? (
                              <ProductGlyph id={product} />
                            ) : (
                              <Box aria-hidden className="size-3.5 text-muted-foreground" />
                            )}
                          </span>
                          <span className="min-w-0 flex-1 truncate font-medium">{label}</span>
                          {s.errorRate > 0 && (
                            <span
                              className={cn(
                                "numeric shrink-0 text-hint",
                                tone === "danger"
                                  ? "font-medium text-destructive"
                                  : tone === "warning"
                                    ? "text-warning"
                                    : "text-muted-foreground",
                              )}
                            >
                              {share(s.errorRate)} 5xx
                            </span>
                          )}
                          <Sparkline
                            values={s.points.map((p) => p.total)}
                            color={LANES[rank % LANES.length]}
                            width={56}
                            height={16}
                            className="shrink-0 max-sm:hidden"
                            label={`${label}'s requests a minute over the last hour`}
                          />
                          <span className="numeric w-12 shrink-0 text-right font-medium">
                            {compactCount(s.requests)}
                            <span className="font-normal text-muted-foreground">/h</span>
                          </span>
                        </button>
                      </li>
                    )
                  })}
                </ul>
              </>
            )}
            {(ranked.length > top.length || silent > 0) && (
              <p className="border-t border-hairline pt-3 text-hint text-muted-foreground">
                {[
                  ranked.length > top.length && plural(ranked.length - top.length, "quieter site"),
                  silent > 0 && `${plural(silent, "site")} with no log the dashboard reads`,
                ]
                  .filter(Boolean)
                  .join(" · ")}
              </p>
            )}
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}

type Standing = "wrong" | "due" | "fine"

function standing(cert: Certificate): Standing {
  if (cert.error || cert.expired || cert.staging) return "wrong"
  return cert.expiring ? "due" : "fine"
}

const DOT: Record<Standing, string> = {
  wrong: "bg-destructive",
  due: "bg-warning",
  fine: "bg-success",
}

/**
 * Every certificate on one runway that ends ninety days out — certbot's term
 * — each a dot in its state's colour where its days left put it, so a host
 * whose certificates bunch up at the near end is seen before one is read.
 * Under it the five that run out first, each its issuer's mark and its days.
 */
function CertificatesBlock({
  list,
  loading,
  error,
  onRetry,
  certbot,
}: {
  list?: Certificate[]
  loading: boolean
  error?: Error
  onRetry: () => void
  certbot: boolean
}) {
  const router = useRouter()
  const all = list ?? []
  const wrong = all.filter((c) => standing(c) !== "fine").length
  const severe = all.some((c) => standing(c) === "wrong")
  const next = [...all].sort((a, b) => a.daysLeft - b.daysLeft)[0]
  // Life left against the longest term on this host, floored at the ninety
  // days certbot issues for, so a host whose certificates are all nearly due
  // reads as a run of short bars rather than one full bar the rest are
  // measured against.
  const horizon = Math.max(...all.map((c) => c.daysLeft), RUNWAY_DAYS)
  const soonest: BarListItem[] = [...all]
    .sort((a, b) => a.daysLeft - b.daysLeft)
    .slice(0, SHOWN)
    .map((cert) => {
      const state = standing(cert)
      const product = certificateProduct(cert)
      return {
        key: cert.path,
        label: cert.name,
        mark: product ? <ProductGlyph id={product} /> : undefined,
        mono: false,
        // The figure column is a fixed width and does not truncate, so the
        // reading is a word rather than the sentence the finding carries.
        value: cert.error
          ? "error"
          : cert.expired
            ? "expired"
            : cert.staging
              ? "test"
              : `${cert.daysLeft}d`,
        share: cert.daysLeft / horizon,
        signal: state === "fine" ? 0 : 1,
        tone: state === "wrong" ? "danger" : "warning",
        // Contract with the Certificates page, which opens the certificate
        // a ?cert= link names; the page itself is the destination either way.
        title: `Show ${cert.name} in Certificates`,
        onClick: () => router.push(`/proxy/certificates?cert=${encodeURIComponent(cert.path)}`),
        // Where it came from, because certbot renews itself and an imported
        // file does not — which is what the days left mean differently.
        hint: cert.selfSigned
          ? "self-signed"
          : cert.source.startsWith("nginx:")
            ? "site"
            : cert.source,
      }
    })

  return (
    <Panel plain aria-label="Certificates">
      <PanelHeader
        title="Certificates"
        actions={all.length > 0 && <HeadLink href="/proxy/certificates">All certificates</HeadLink>}
      />
      <PanelBody className="space-y-4 pt-4">
        {loading ? (
          <Bones />
        ) : error ? (
          <ErrorState error={error} onRetry={onRetry} className="mt-2" />
        ) : all.length === 0 ? (
          <p className="py-2 text-hint text-muted-foreground">
            {certbot
              ? "Nothing issued yet — issue a certificate from the Certificates tab."
              : "No certificates were found on this host."}
          </p>
        ) : (
          <div className="animate-rise space-y-4">
            {wrong > 0 ? (
              <Headline
                value={wrong}
                unit={wrong === 1 ? "needs attention" : "need attention"}
                caption={`of ${plural(all.length, "certificate")}`}
                tone={severe ? "danger" : "warning"}
              />
            ) : (
              next && (
                <Headline
                  value={next.daysLeft}
                  unit={next.daysLeft === 1 ? "day to the next expiry" : "days to the next expiry"}
                  caption={`${next.name} · all ${plural(all.length, "certificate")} valid`}
                />
              )
            )}
            <Runway certificates={all} />
            <BarList items={soonest} />
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * The days left as positions on a line from now to ninety days out. Dots
 * that would sit on one another step down a row, so a cluster reads as a
 * cluster rather than as one dot.
 */
function Runway({ certificates }: { certificates: Certificate[] }) {
  const placed: { cert: Certificate; at: number; row: number }[] = []
  for (const cert of [...certificates].sort((a, b) => a.daysLeft - b.daysLeft)) {
    const days = cert.error || cert.expired || cert.staging ? 0 : cert.daysLeft
    const at = Math.min(Math.max(days, 0), RUNWAY_DAYS) / RUNWAY_DAYS
    let row = 0
    while (placed.some((p) => p.row === row && Math.abs(p.at - at) < 0.035)) row += 1
    placed.push({ cert, at, row: Math.min(row, 2) })
  }
  const rows = Math.max(...placed.map((p) => p.row), 0) + 1
  return (
    <div
      role="img"
      aria-label={`Days left on each certificate: ${certificates.map((c) => `${c.name} ${c.expired ? "expired" : `${c.daysLeft} days`}`).join(", ")}`}
      className="pt-1"
    >
      <div className="relative" style={{ height: `${rows * 10 + 8}px` }}>
        <span aria-hidden className="absolute inset-x-0 bottom-0 h-1 rounded-full bg-meter-track">
          {/* The stretch certbot renews inside, in the colour a certificate
              there is drawn in. */}
          <span
            className="absolute inset-y-0 left-0 rounded-l-full bg-plot-warning"
            style={{ width: `${(30 / RUNWAY_DAYS) * 100}%` }}
          />
        </span>
        {placed.map(({ cert, at, row }) => (
          <span
            key={cert.path}
            title={`${cert.name} · ${cert.expired ? "expired" : `${cert.daysLeft} days left`}`}
            className={cn(
              "absolute size-2 -translate-x-1/2 rounded-full ring-2 ring-background",
              DOT[standing(cert)],
            )}
            style={{ left: `${Math.min(Math.max(at * 100, 1), 99)}%`, bottom: `${8 + row * 10}px` }}
          />
        ))}
      </div>
      <div className="numeric mt-1 flex justify-between text-micro text-muted-foreground">
        <span>now</span>
        <span>30d</span>
        <span>60d</span>
        <span>90d</span>
      </div>
    </div>
  )
}

/**
 * What failed this hour, in the two places a failure is written: the 5xx
 * answers in every site's access record, by site, and — where nginx is on
 * the host — its error log, grouped by what went wrong. It read nginx's
 * error log alone, which on a host where the Docker Caddy ingress answers
 * said "no errors" about requests nginx never saw.
 */
function ErrorsBlock({
  report,
  error,
  nginx,
  sites,
  trafficError,
  labelOf,
}: {
  report?: ErrorReport
  error?: Error
  nginx: boolean
  sites?: SiteTrafficReading[]
  trafficError?: Error
  labelOf: (site: string) => string
}) {
  const router = useRouter()
  const failing = (sites ?? [])
    .filter((s) => s.status === "available")
    .map((s) => ({ reading: s, failed: Math.round(s.errorRate * s.requests) }))
    .filter((s) => s.failed > 0)
    .sort((a, b) => b.failed - a.failed)
  const failed = failing.reduce((sum, s) => sum + s.failed, 0)
  const logged = report?.groups.reduce((sum, g) => sum + g.count, 0) ?? 0
  const waiting = (!sites && !trafficError) || (nginx && !report && !error)
  const quiet = failed === 0 && logged === 0

  return (
    <Panel plain aria-label="Recent errors">
      <PanelHeader
        title="Recent errors"
        actions={nginx && <HeadLink href="/proxy/traffic?view=errors">Error log</HeadLink>}
      />
      <PanelBody className="space-y-4 pt-4">
        {waiting ? (
          <Bones />
        ) : (
          <div className="animate-rise space-y-4">
            <Headline
              value={failed.toLocaleString()}
              unit={failed === 1 ? "server error this hour" : "server errors this hour"}
              tone={failed > 0 ? "danger" : undefined}
              caption={
                trafficError
                  ? "Couldn't read the sites' access logs"
                  : nginx && !error && report && !report.note
                    ? `${logged.toLocaleString()} nginx error-log ${logged === 1 ? "line" : "lines"}`
                    : failed > 0
                      ? `across ${plural(failing.length, "site")}`
                      : undefined
              }
            />
            {quiet && !trafficError && !error && !report?.note ? (
              <p className="flex items-center gap-2.5 text-body text-muted-foreground">
                <CheckCircle aria-hidden className="size-4 shrink-0 text-success" />
                Nothing failed in the last hour
              </p>
            ) : (
              failing.length > 0 && (
                <ul aria-label="Sites answering 5xx" className="-mx-2">
                  {failing.slice(0, SHOWN).map(({ reading, failed }) => {
                    const label = labelOf(reading.site)
                    return (
                      <li key={reading.site}>
                        <button
                          type="button"
                          title={`Open ${label}'s requests`}
                          onClick={() => router.push(readingHref(reading))}
                          className="flex h-9 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body focus-ring-inset transition-colors hover:bg-row-hover"
                        >
                          <span
                            aria-hidden
                            className="size-1.5 shrink-0 rounded-full bg-destructive"
                          />
                          <span className="min-w-0 flex-1 truncate font-medium">{label}</span>
                          <span className="numeric shrink-0 text-hint text-muted-foreground">
                            {share(reading.errorRate)}
                          </span>
                          <span className="numeric w-12 shrink-0 text-right font-medium text-destructive">
                            {failed.toLocaleString()}
                          </span>
                        </button>
                      </li>
                    )
                  })}
                </ul>
              )
            )}
            {nginx &&
              (error ? (
                <p className="text-hint text-muted-foreground" title={errorMessage(error)}>
                  {"Couldn't read nginx's error log."}
                </p>
              ) : report?.note ? (
                <p className="text-hint text-muted-foreground">{report.note}</p>
              ) : (
                report &&
                report.groups.length > 0 && (
                  <FindingList findings={report.groups.slice(0, 4).map(errorFinding)} />
                )
              ))}
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}
