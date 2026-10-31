"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, Box, LockClosed } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { LANES } from "@/lib/hue"
import { plural, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { Certificate, ErrorReport, SiteTrafficReading } from "@/lib/types"
import { BarReading, type ReadingPart } from "@/components/bar-reading"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { certificateProduct } from "@/components/proxy/marks"
import { ProjectFavicon } from "@/components/deploy/project-mark"
import { isActionableProxyError } from "@/components/proxy/site-errors"
import { useAuth } from "@/hooks/use-auth"
import { compactCount, errorRateTone, readingHref, share } from "@/components/proxy/site-traffic"

/** How many sites and certificates a block names; the rest are one link away. */
const SHOWN = 5

/** How many of nginx's error groups Recent errors names after the sites. */
const SHOWN_GROUPS = 4

/** The runway's far end: certbot issues for ninety days. */
const RUNWAY_DAYS = 90

/**
 * The readings the four tiles never gave, as three bars under the route map:
 * which sites the hour's requests went to, where each certificate stands on a
 * ninety-day runway, and what failed. Each block is a bar and one line, so it
 * is one height with a reading or without: the blocks drawn depend only on
 * whether an engine here keeps access records, not on what those records have
 * said so far, and the routes under the band stay where they are as readings
 * land. A press opens the site's requests, the certificate, the error log.
 *
 * Traffic and errors are read from every site's access record, nginx's and
 * the Docker Caddy ingress's alike, so a host where the ingress holds ports
 * 80 and 443 is read as busy as it is. They are polled, so nothing here is
 * marked live.
 */
export function OverviewBand({
  traffics,
  nginx,
  traffic,
  certificates,
  errors,
  appOf,
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
  /** What a site's visitors reach — its product, the project it is — for its mark. */
  appOf: (site: string) => { product?: string; projectId?: number } | undefined
  /** The name a site's visitors type, which says more than its file's. */
  labelOf: (site: string) => string
}) {
  return (
    <div
      data-slot="overview-band"
      className={cn(
        "grid min-w-0 gap-x-12 gap-y-10 [&>*]:min-w-0",
        traffics && "lg:grid-cols-2 xl:grid-cols-3",
      )}
    >
      {traffics && <TrafficBlock {...traffic} appOf={appOf} labelOf={labelOf} />}
      <CertificatesBlock {...certificates} />
      {traffics && (
        <ErrorsBlock
          {...errors}
          nginx={nginx}
          sites={traffic.sites}
          trafficError={traffic.error}
          appOf={appOf}
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
      className="flex h-7 shrink-0 items-center gap-1 rounded-md text-hint font-medium whitespace-nowrap text-muted-foreground focus-ring hover:text-foreground"
    >
      {children} <ArrowRight className="size-3" />
    </Link>
  )
}

const TONE_TEXT = {
  danger: "text-destructive",
  warning: "text-warning",
  success: "text-success",
}

/** A block's figure in its head: the number, in its standing's tone, and what it counts. */
function HeadFigure({
  value,
  word,
  tone,
}: {
  value: React.ReactNode
  word: string
  tone?: keyof typeof TONE_TEXT
}) {
  return (
    <span className="numeric flex h-7 items-center gap-1 text-hint whitespace-nowrap text-muted-foreground">
      <span className={cn("font-medium", tone ? TONE_TEXT[tone] : "text-foreground")}>{value}</span>
      {word}
    </span>
  )
}

/** A site's mark: the project it is, else the product it reaches, else a plain box. */
function SiteGlyph({ app }: { app?: { product?: string; projectId?: number } }) {
  return (
    <ProjectFavicon
      projectId={app?.projectId}
      className="size-3.5"
      fallback={
        hasProductLogo(app?.product) ? (
          <ProductGlyph id={app.product} />
        ) : (
          <Box aria-hidden className="size-3.5 text-muted-foreground" />
        )
      }
    />
  )
}

/** Words for the end of a block's strip, cut to what fits with the whole of them on a pointer. */
function Note({ parts }: { parts: (string | false)[] }) {
  const words = parts.filter(Boolean).join(" · ")
  return (
    <span className="truncate" title={words}>
      {words}
    </span>
  )
}

/**
 * The hour's requests as one bar shared out by site, each rank a hue of its
 * own from `LANES` (none of them a state's, and no two neighbours alike, which
 * a hash of five names did not promise). The line reads a site's count and its
 * 5xx share, and its trail what the count has done since the page opened.
 */
function TrafficBlock({
  sites,
  error,
  appOf,
  labelOf,
}: {
  sites?: SiteTrafficReading[]
  error?: Error
  appOf: (site: string) => { product?: string; projectId?: number } | undefined
  labelOf: (site: string) => string
}) {
  const router = useRouter()
  const read = (sites ?? []).filter((s) => s.status === "available")
  const ranked = [...read].sort((a, b) => b.requests - a.requests)
  const top = ranked.slice(0, SHOWN).filter((s) => s.requests > 0)
  const total = read.reduce((sum, s) => sum + s.requests, 0)
  const rest = total - top.reduce((sum, s) => sum + s.requests, 0)
  const silent = (sites ?? []).length - read.length
  const quieter = ranked.length - top.length

  return (
    <BarReading
      title="Traffic"
      label="Traffic"
      list="Busiest sites"
      palette={LANES}
      trail
      rest={rest}
      restLabel="Quieter sites"
      format={compactCount}
      figure={
        <span className="flex h-7 items-center gap-3">
          {read.length > 0 && <HeadFigure value={compactCount(total)} word="requests this hour" />}
          <HeadLink href="/proxy/traffic">All traffic</HeadLink>
        </span>
      }
      message={
        error ? (
          <span title={errorMessage(error)}>{"Couldn't read the sites' access logs."}</span>
        ) : !sites ? (
          <TextShimmer>{"Reading the sites' access logs…"}</TextShimmer>
        ) : read.length === 0 ? (
          "No site writes an access log the dashboard can read."
        ) : total === 0 ? (
          `None of ${plural(read.length, "site")} answered a request this hour.`
        ) : undefined
      }
      note={
        (quieter > 0 || silent > 0) && (
          <Note
            parts={[
              quieter > 0 && plural(quieter, "quieter site"),
              silent > 0 && `${plural(silent, "site")} with no log the dashboard reads`,
            ]}
          />
        )
      }
      parts={top.map((s) => {
        const label = labelOf(s.site)
        const tone = errorRateTone(s.errorRate)
        return {
          key: s.site,
          value: s.requests,
          mark: <SiteGlyph app={appOf(s.site)} />,
          name: label,
          detail: s.errorRate > 0 && (
            <span
              className={cn(
                tone === "danger" && "text-destructive",
                tone === "warning" && "text-warning",
              )}
            >
              {share(s.errorRate)} 5xx
            </span>
          ),
          figure: (
            <>
              {compactCount(s.requests)}
              <span className="font-normal text-muted-foreground">/h</span>
            </>
          ),
          label: `${label} ${compactCount(s.requests)}`,
          title: `${s.requests.toLocaleString()} requests in the last hour`,
          press: {
            label: `Open ${label}'s requests`,
            opens: true,
            onPress: () => router.push(readingHref(s)),
          },
        }
      })}
    />
  )
}

type Standing = "wrong" | "due" | "fine"

function standing(cert: Certificate): Standing {
  if (cert.error || cert.expired || cert.staging) return "wrong"
  return cert.expiring ? "due" : "fine"
}

const STANDING_COLOR: Record<Standing, string> = {
  wrong: "var(--destructive)",
  due: "var(--warning)",
  fine: "var(--success)",
}

/**
 * Where a certificate sits on the runway. One that browsers would refuse has
 * no days worth counting however far off its date is, so it sits at the near
 * end and is among the first named.
 */
function runwayDays(cert: Certificate) {
  return standing(cert) === "wrong" ? 0 : cert.daysLeft
}

/**
 * The certificates on one runway that ends ninety days out — certbot's term —
 * each a tick in its standing's colour where its days left put it, so a host
 * whose certificates bunch up at the near end is seen before one is read. The
 * five that run out first are on it, each its issuer's mark and its days.
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
  const { can } = useAuth()
  const all = list ?? []
  const soonest = [...all].sort((a, b) => runwayDays(a) - runwayDays(b) || a.daysLeft - b.daysLeft)
  const named = soonest.slice(0, SHOWN)
  const wrong = all.filter((c) => standing(c) === "wrong").length
  const due = all.filter((c) => standing(c) === "due").length
  const next = soonest[0]

  return (
    <BarReading
      title="Certificates"
      label="Certificates"
      ticks
      figure={
        loading || error ? undefined : all.length === 0 ? (
          <span className="flex h-7 items-center">
            <Button asChild size="xs" variant="outline">
              <Link href="/proxy/certificates">
                {certbot && can("system.admin") ? "Issue a certificate" : "Open Certificates"}
                <ArrowRight />
              </Link>
            </Button>
          </span>
        ) : (
          <span className="flex h-7 items-center gap-3">
            {wrong > 0 ? (
              <HeadFigure value={wrong} word="not valid" tone="danger" />
            ) : due > 0 ? (
              <HeadFigure value={due} word="due for renewal" tone="warning" />
            ) : (
              <HeadFigure
                value={next.daysLeft}
                word={next.daysLeft === 1 ? "day to next expiry" : "days to next expiry"}
                tone="success"
              />
            )}
            <HeadLink href="/proxy/certificates">All certificates</HeadLink>
          </span>
        )
      }
      message={
        loading ? (
          <TextShimmer>Reading the certificates…</TextShimmer>
        ) : error ? (
          <span className="inline-flex items-center gap-2">
            <span title={errorMessage(error)}>{"Couldn't read the certificates."}</span>
            <Button size="xs" variant="outline" onClick={onRetry}>
              Try again
            </Button>
          </span>
        ) : all.length === 0 ? (
          certbot ? (
            "Every site is on plain HTTP. Issue one from Let's Encrypt and certbot renews it before it runs out."
          ) : (
            "No certificates were found on this host. Install certbot to issue free ones, or bring your own."
          )
        ) : undefined
      }
      note={all.length > named.length && `${all.length - named.length} more`}
      parts={named.map((cert) => {
        const product = certificateProduct(cert)
        const state = standing(cert)
        // A word rather than the sentence a finding carries: the line's figure
        // keeps to its one row.
        const reading = cert.error
          ? "error"
          : cert.expired
            ? "expired"
            : cert.staging
              ? "test"
              : `${cert.daysLeft}d`
        return {
          key: cert.path,
          value: 1,
          at: runwayDays(cert) / RUNWAY_DAYS,
          color: STANDING_COLOR[state],
          mark: product ? (
            <ProductGlyph id={product} />
          ) : (
            <LockClosed aria-hidden className="size-3.5 text-muted-foreground" />
          ),
          name: cert.name,
          // Where it came from, because certbot renews itself and an imported
          // file does not — which is what the days left mean differently.
          detail: cert.selfSigned
            ? "self-signed"
            : cert.source.startsWith("nginx:")
              ? "site"
              : cert.source,
          figure: (
            <span
              className={cn(
                state === "wrong" && "text-destructive",
                state === "due" && "text-warning",
              )}
            >
              {reading}
            </span>
          ),
          label: `${cert.name} ${reading}`,
          title: `${cert.name} · ${cert.error ?? (cert.expired ? "expired" : `${cert.daysLeft} days left`)}`,
          // Contract with the Certificates page, which opens the certificate
          // a ?cert= link names; the page itself is the destination either way.
          press: {
            label: `Show ${cert.name} in Certificates`,
            opens: true,
            onPress: () => router.push(`/proxy/certificates?cert=${encodeURIComponent(cert.path)}`),
          },
        }
      })}
    />
  )
}

/**
 * What failed this hour, in the two places a failure is written: the 5xx
 * answers in every site's access record, by site, and — where nginx is on
 * the host — its error log, grouped by what went wrong. It read nginx's
 * error log alone, which on a host where the Docker Caddy ingress answers
 * said "no errors" about requests nginx never saw. A group's advice is on the
 * error log's page, which each of its parts opens.
 */
function ErrorsBlock({
  report,
  error,
  nginx,
  sites,
  trafficError,
  appOf,
  labelOf,
}: {
  report?: ErrorReport
  error?: Error
  nginx: boolean
  sites?: SiteTrafficReading[]
  trafficError?: Error
  appOf: (site: string) => { product?: string; projectId?: number } | undefined
  labelOf: (site: string) => string
}) {
  const router = useRouter()
  const read = (sites ?? []).filter((s) => s.status === "available")
  const failing = read
    .map((s) => ({ reading: s, failed: Math.round(s.errorRate * s.requests) }))
    .filter((s) => s.failed > 0)
    .sort((a, b) => b.failed - a.failed)
  const failed = failing.reduce((sum, s) => sum + s.failed, 0)
  const waiting = (!sites && !trafficError) || (nginx && !report && !error)
  // A 5xx is counted from an access log. With none read anywhere the count
  // is unknown, not zero: the block said "0 server errors this hour" and
  // "Nothing failed" about a host whose requests it could not see.
  const counted = !trafficError && read.length > 0
  const errorLog = nginx && !error && report && !report.note
  // Only groups worth acting on are drawn: a log whose every line was a notice
  // has nothing to say beyond the block's all-clear.
  const groups = errorLog ? report.groups.filter(isActionableProxyError) : []
  const logged = groups.reduce((sum, g) => sum + g.count, 0)
  const quiet = failed === 0 && logged === 0

  // What stopped the error log being read, said after whatever else is said.
  const logTrouble =
    nginx &&
    (error ? (
      <span title={errorMessage(error)}>{"Couldn't read nginx's error log."}</span>
    ) : (
      report?.note
    ))
  const unread = trafficError
    ? "Couldn't read the sites' access logs, so their server errors (5xx) are unknown."
    : "Server errors (5xx) are not counted: no site writes an access log."

  const message = waiting ? (
    <TextShimmer>{"Reading this hour's errors…"}</TextShimmer>
  ) : !counted && !errorLog ? (
    <>
      {trafficError
        ? "Couldn't read the sites' access logs, so this hour's server errors are unknown."
        : "Server errors are counted from access logs, and no site here writes one the dashboard reads."}
      {logTrouble && <> {logTrouble}</>}
    </>
  ) : quiet ? (
    counted ? (
      <>
        Nothing failed in the last hour: no 5xx answers from {plural(read.length, "site")}
        {errorLog ? ", and nginx's error log is quiet." : "."}
        {logTrouble && <> {logTrouble}</>}
      </>
    ) : (
      // Without an access log the 5xx count is unknown, so this vouches only
      // for what it read: the error log.
      <>nginx logged no errors this hour. {unread}</>
    )
  ) : undefined

  const figure =
    message !== undefined ? undefined : counted ? (
      <HeadFigure
        value={failed.toLocaleString()}
        word={failed === 1 ? "server error this hour" : "server errors this hour"}
        tone={failed > 0 ? "danger" : undefined}
      />
    ) : (
      <HeadFigure
        value={logged.toLocaleString()}
        word={logged === 1 ? "nginx error-log line this hour" : "nginx error-log lines this hour"}
        tone="danger"
      />
    )

  const siteParts: ReadingPart[] = failing.slice(0, SHOWN).map(({ reading, failed }) => {
    const label = labelOf(reading.site)
    return {
      key: `site:${reading.site}`,
      value: failed,
      color: "var(--destructive)",
      mark: <SiteGlyph app={appOf(reading.site)} />,
      name: label,
      detail: share(reading.errorRate),
      figure: <span className="text-destructive">{failed.toLocaleString()}</span>,
      label: `${label} ${failed.toLocaleString()} server errors`,
      title: `${failed.toLocaleString()} of ${reading.requests.toLocaleString()} requests answered with a 5xx`,
      press: {
        label: `Open ${label}'s requests`,
        opens: true,
        onPress: () => router.push(readingHref(reading)),
      },
    }
  })
  const groupParts: ReadingPart[] = groups.slice(0, SHOWN_GROUPS).map((group) => {
    const name = group.title || group.pattern
    return {
      key: `log:${group.level}:${group.pattern}:${group.upstream ?? ""}`,
      value: group.count,
      color: group.level === "warn" ? "var(--warning)" : "var(--destructive)",
      mark: <ProductGlyph id="nginx" />,
      name,
      detail: `last ${relativeTime(group.last)}${group.upstream ? ` · ${group.upstream}` : ""}`,
      figure: group.count.toLocaleString(),
      label: `${name} ${group.count.toLocaleString()} in nginx's error log`,
      title: group.advice,
      press: {
        label: `Open ${name} in the error log`,
        opens: true,
        onPress: () => router.push("/proxy/traffic?view=errors"),
      },
    }
  })

  return (
    <BarReading
      title="Recent errors"
      label="Recent errors"
      list={failing.length > 0 ? "Sites answering 5xx" : "What nginx logged"}
      className="lg:col-span-2 xl:col-span-1"
      fixed
      figure={
        nginx ? (
          <HeadLink href="/proxy/traffic?view=errors">{figure ?? "Error log"}</HeadLink>
        ) : (
          figure
        )
      }
      message={message}
      note={
        message === undefined &&
        (failing.length > SHOWN || logTrouble || !counted) && (
          <span
            className="truncate"
            title={!counted ? unread : error ? errorMessage(error) : report?.note}
          >
            {[
              failing.length > SHOWN && plural(failing.length - SHOWN, "more site"),
              !counted && (trafficError ? "5xx unknown" : "5xx not counted"),
              logTrouble && "error log not read",
            ]
              .filter(Boolean)
              .join(" · ")}
          </span>
        )
      }
      parts={message === undefined ? [...siteParts, ...groupParts] : []}
    />
  )
}
