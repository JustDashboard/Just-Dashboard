"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, Box } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { LANES } from "@/lib/hue"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { Certificate, ErrorReport, SiteTrafficReading } from "@/lib/types"
import { BarList, type BarListItem } from "@/components/bar-list"
import { FindingList } from "@/components/finding-list"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { ShareBar } from "@/components/procs/workloads"
import { ErrorState } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import { certificateProduct } from "@/components/proxy/marks"
import { errorFinding } from "@/components/proxy/site-errors"
import { compactCount, errorRateTone, share, trafficHref } from "@/components/proxy/site-traffic"

/** How many sites and certificates a block names; the rest are one link away. */
const SHOWN = 5

/** The runway's far end: certbot issues for ninety days. */
const RUNWAY_DAYS = 90

/**
 * The readings the four tiles never gave, as three blocks under the route
 * map: where the hour's requests went, how long every certificate has left
 * on one runway, and what nginx complained about. Each is a destination's
 * digest — a press opens the site's traffic, the certificate, the error log.
 */
export function OverviewBand({
  nginx,
  traffic,
  certificates,
  errors,
  productOf,
}: {
  /** Traffic and errors are read from nginx's logs; a Caddy host has neither. */
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
}) {
  return (
    <div
      data-slot="overview-band"
      className={cn(
        "grid min-w-0 gap-x-10 gap-y-8 [&>*]:min-w-0",
        nginx && "lg:grid-cols-2 xl:grid-cols-3",
      )}
    >
      {nginx && <TrafficBlock {...traffic} productOf={productOf} />}
      <CertificatesBlock {...certificates} />
      {nginx && <ErrorsBlock {...errors} />}
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
 * The hour's requests as one bar shared out by site, each rank a hue of its
 * own from `LANES` (none of them a state's, and no two neighbours alike, which
 * a hash of five names did not promise), then the five busiest as rows with
 * what they reach and their 5xx share.
 */
function TrafficBlock({
  sites,
  error,
  productOf,
}: {
  sites?: SiteTrafficReading[]
  error?: Error
  productOf: (site: string) => string | undefined
}) {
  const router = useRouter()
  const read = (sites ?? []).filter((s) => s.status === "available")
  const ranked = [...read].sort((a, b) => b.requests - a.requests)
  const top = ranked.slice(0, SHOWN).filter((s) => s.requests > 0)
  const total = read.reduce((sum, s) => sum + s.requests, 0)
  const rest = total - top.reduce((sum, s) => sum + s.requests, 0)
  const silent = (sites ?? []).length - read.length

  return (
    <Panel plain aria-label="Traffic">
      <PanelHeader
        title="Traffic"
        actions={
          <div className="flex items-center gap-3">
            {sites && total > 0 && (
              <span className="numeric text-hint text-muted-foreground">
                {compactCount(total)} req in the hour
              </span>
            )}
            <HeadLink href="/proxy/traffic">All traffic</HeadLink>
          </div>
        }
      />
      <PanelBody className="space-y-3 pt-4">
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
        ) : total === 0 ? (
          <p className="py-2 text-body text-muted-foreground">No requests in the last hour.</p>
        ) : (
          <div className="animate-rise space-y-3">
            <ShareBar
              label="Requests in the last hour"
              capacity={total}
              rest={rest}
              parts={top.map((s, rank) => ({
                key: s.site,
                value: s.requests,
                color: LANES[rank % LANES.length],
                label: `${s.site} ${compactCount(s.requests)}`,
              }))}
              format={compactCount}
            />
            <ul aria-label="Busiest sites" className="-mx-2">
              {top.map((s, rank) => {
                const product = productOf(s.site)
                const tone = errorRateTone(s.errorRate)
                return (
                  <li key={s.site}>
                    <button
                      type="button"
                      title={`Open ${s.site}'s traffic`}
                      aria-label={`Open ${s.site}'s traffic`}
                      onClick={() => router.push(trafficHref(s.site))}
                      className="flex h-8 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body focus-ring-inset transition-colors hover:bg-row-hover"
                    >
                      <span
                        aria-hidden
                        className="h-2.5 w-0.5 shrink-0 rounded-full"
                        style={{ background: LANES[rank % LANES.length] }}
                      />
                      <span className="flex size-4 shrink-0 items-center justify-center">
                        {hasProductLogo(product) ? (
                          <ProductGlyph id={product} />
                        ) : (
                          <Box aria-hidden className="size-3.5 text-muted-foreground" />
                        )}
                      </span>
                      <span className="min-w-0 truncate font-medium">{s.site}</span>
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
                      <span className="numeric ml-auto shrink-0 font-medium">
                        {compactCount(s.requests)}
                        <span className="font-normal text-muted-foreground">/h</span>
                      </span>
                    </button>
                  </li>
                )
              })}
            </ul>
            {(ranked.length > top.length || silent > 0) && (
              <p className="border-t border-hairline pt-3 text-hint text-muted-foreground">
                {[
                  ranked.length > top.length && plural(ranked.length - top.length, "more site"),
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
        actions={
          <div className="flex items-center gap-3">
            {list && all.length > 0 && (
              <span
                className={cn(
                  "numeric text-hint",
                  wrong === 0
                    ? "text-muted-foreground"
                    : severe
                      ? "font-medium text-destructive"
                      : "font-medium text-warning",
                )}
              >
                {wrong > 0
                  ? `${wrong} of ${all.length} ${wrong === 1 ? "needs" : "need"} attention`
                  : `${all.length} valid`}
              </span>
            )}
            {all.length > 0 && <HeadLink href="/proxy/certificates">All certificates</HeadLink>}
          </div>
        }
      />
      <PanelBody className="space-y-3 pt-4">
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
          <div className="animate-rise space-y-3">
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

/** nginx's error log over the hour, grouped by what went wrong. */
function ErrorsBlock({ report, error }: { report?: ErrorReport; error?: Error }) {
  const total = report?.groups.reduce((sum, g) => sum + g.count, 0) ?? 0
  return (
    <Panel plain aria-label="Recent nginx errors">
      <PanelHeader
        title="Recent nginx errors"
        actions={
          <div className="flex items-center gap-3">
            {total > 0 && (
              <span className="numeric text-hint text-muted-foreground">
                {total.toLocaleString()} in the hour
              </span>
            )}
            <HeadLink href="/proxy/traffic?view=errors">Error log</HeadLink>
          </div>
        }
      />
      <PanelBody>
        {error ? (
          <p className="text-hint text-muted-foreground" title={errorMessage(error)}>
            {"Couldn't read nginx's error log."}
          </p>
        ) : !report ? (
          <Bones />
        ) : (
          <div className="animate-rise">
            {report.note ? (
              <p className="text-hint text-muted-foreground">{report.note}</p>
            ) : (
              <FindingList
                findings={report.groups.slice(0, 4).map(errorFinding)}
                emptyLabel="No errors in the last hour"
              />
            )}
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}
