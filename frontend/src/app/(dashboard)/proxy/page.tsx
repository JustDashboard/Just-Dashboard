"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, Globe, RefreshClockwise } from "@/components/icons"
import { ApiError, errorMessage, get } from "@/lib/api"
import type { Certificate, CertbotState, Listener, StreamStatus, VHost } from "@/lib/types"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Page, PageContext, PageState } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { BarList, type BarListItem } from "@/components/bar-list"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { FindingList } from "@/components/finding-list"
import { EmptyState, ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { useNow } from "@/components/deploy/vocabulary"
import { useProxy } from "@/components/proxy/proxy-context"
import { EngineActions, EngineIdentity, useEngineUnit } from "@/components/proxy/engine"
import { ConfigEditor } from "@/components/proxy/config-editor"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { certificateProduct, siteProduct } from "@/components/proxy/marks"
import { RoutePath } from "@/components/proxy/route-path"
import { ServingStatus, SiteTLS } from "@/components/proxy/site-marks"
import {
  findingAction,
  foldProxyFindings,
  unreadableSource,
  type ProxySource,
  type UnreadableSource,
} from "@/components/proxy/attention"
import {
  oldestReading,
  reading,
  stillRefreshing,
  updatedLabel,
  type Answer,
  type Reading,
} from "@/components/proxy/freshness"
import { overviewRoutes, routeKind, routeTarget } from "@/components/proxy/overview-routes"

/**
 * What a poll last answered, or nothing when its last read failed. A source
 * that failed is unknown, not what it said before: judged from a stale answer
 * or from none, the page read "all within limits" and "nothing configured"
 * about a host it could not see.
 */
function readable<T>(poll: PollState<Reading<T>>): Reading<T> | undefined {
  return poll.error ? undefined : poll.data
}

/**
 * A tile's hint when its source failed. The reason is in Needs attention and,
 * for a pointer resting on the tile, in the hint's own tooltip.
 */
function Unread({ error }: { error: Error }) {
  return <span title={errorMessage(error)}>{"couldn't read"}</span>
}

/**
 * Readings first, then the engine and its commands. Routes own the wide column;
 * findings and expiry share the rail so a list of warnings never pushes every route off screen.
 */
export default function ProxyOverviewPage() {
  const { status, updatedAt: statusAt, error: statusError, refresh: refreshStatus } = useProxy()
  const { can } = useAuth()
  const router = useRouter()
  const admin = can("system.admin")
  const engine = useEngineUnit(status)
  // A reader's route opens its file here, read-only; see routeTarget.
  const [viewing, setViewing] = useState<VHost | null>(null)

  const vhosts = usePoll(
    (signal) => reading(get<VHost[]>("/proxy/vhosts", undefined, signal)),
    30_000,
  )
  const certs = usePoll(
    (signal) => reading(get<Certificate[]>("/certificates/", undefined, signal)),
    300_000,
  )
  const certbot = usePoll(
    (signal) => reading(get<CertbotState>("/certificates/certbot", undefined, signal)),
    300_000,
    [],
    { enabled: Boolean(status?.certbot) },
  )
  const streams = usePoll(
    (signal) => reading(get<StreamStatus>("/proxy/streams/", undefined, signal)),
    60_000,
  )
  const ports = usePoll((signal) => reading(get<Listener[]>("/ports", undefined, signal)), 30_000)

  // certbot being absent is a fact about the host, not a failure to report.
  const certbotGone =
    certbot.error instanceof ApiError && certbot.error.code === "certbot_unavailable"
  const certbotAsked = Boolean(status?.certbot)

  // Every read the page shows, by source, as Refresh waits on them: a poll
  // that is switched off is not asked and not waited for.
  const answers: Record<string, Answer> = {
    status: { at: statusAt, error: statusError },
    vhosts: { at: vhosts.data?.at, error: vhosts.error },
    certs: { at: certs.data?.at, error: certs.error },
    streams: { at: streams.data?.at, error: streams.error },
    ports: { at: ports.data?.at, error: ports.error },
    ...(engine.name ? { engine: { at: engine.fetchedAt, error: engine.error } } : {}),
    ...(certbotAsked ? { certbot: { at: certbot.data?.at, error: certbot.error } } : {}),
  }
  const [asked, setAsked] = useState<Record<string, Answer>>()
  const refreshing = asked !== undefined && stillRefreshing(asked, answers)
  // The age the page vouches for is that of its oldest reading; a source
  // that failed shows none of its reading, so it has no age to count.
  const updatedAt = oldestReading([
    statusError ? undefined : statusAt,
    engine.fetchedAt,
    readable(vhosts)?.at,
    readable(certs)?.at,
    readable(streams)?.at,
    readable(ports)?.at,
    certbotAsked && !certbotGone ? readable(certbot)?.at : undefined,
  ])
  const refreshEverything = () => {
    setAsked(answers)
    refreshStatus()
    engine.refresh()
    vhosts.refresh()
    certs.refresh()
    if (certbotAsked) certbot.refresh()
    streams.refresh()
    ports.refresh()
  }

  const sites = readable(vhosts)?.value
  const certificates = readable(certs)?.value
  const streamStatus = readable(streams)?.value
  const listeners = readable(ports)?.value
  const hosts = sites ?? []
  const routes = useMemo(() => overviewRoutes(sites ?? []), [sites])
  const onTls = hosts.filter((v) => v.tls).length
  const disabled = hosts.filter((v) => v.kind === "nginx" && !v.enabled && v.enabledPath).length
  const exposed = useMemo(() => (listeners ?? []).filter((l) => l.exposed), [listeners])
  const badCerts = useMemo(
    () => (certificates ?? []).filter((c) => c.expired || c.expiring || c.error),
    [certificates],
  )
  // The certificates as a reading rather than a count. `share` is life left
  // against the longest term on this host, floored at the ninety days certbot
  // issues for, so a host whose certificates are all nearly due reads as a run
  // of short bars instead of one full-length bar the rest are measured against.
  const certExpiry = useMemo<BarListItem[]>(() => {
    const all = certificates ?? []
    const horizon = Math.max(...all.map((c) => c.daysLeft), 90)
    return [...all]
      .sort((a, b) => a.daysLeft - b.daysLeft)
      .slice(0, 8)
      .map((cert) => {
        const wrong = Boolean(cert.error) || cert.expired
        const product = certificateProduct(cert)
        return {
          key: cert.path,
          label: cert.name,
          mark: product ? <ProductGlyph id={product} /> : undefined,
          mono: false,
          // The figure column is a fixed width and does not truncate, so the
          // reading is a word rather than the sentence the finding carries.
          value: cert.error ? "error" : cert.expired ? "expired" : `${cert.daysLeft}d`,
          share: cert.daysLeft / horizon,
          signal: wrong || cert.expiring ? 1 : 0,
          tone: wrong ? "danger" : "warning",
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
  }, [certificates, router])
  const renewal = readable(certbot)?.value
  const unreadable = useMemo(() => {
    const failed: [ProxySource, Error | undefined][] = [
      // A status that never answered is the page's own error; one that
      // answered before goes on drawing the engine from that answer.
      ["status", status ? statusError : undefined],
      ["sites", vhosts.error],
      ["certificates", certs.error],
      ["renewal", certbotGone ? undefined : certbot.error],
      ["streams", streams.error],
      ["ports", ports.error],
    ]
    return failed.flatMap(([source, error]): UnreadableSource[] =>
      error ? [{ source, message: errorMessage(error) }] : [],
    )
  }, [
    status,
    statusError,
    vhosts.error,
    certs.error,
    certbot.error,
    certbotGone,
    streams.error,
    ports.error,
  ])
  const findings = useMemo(
    () =>
      foldProxyFindings({
        certs: certificates,
        certbot: certbotGone ? null : renewal,
        vhosts: sites,
        streams: streamStatus,
        ports: listeners,
        unreadable,
      }),
    [certificates, renewal, certbotGone, sites, streamStatus, listeners, unreadable],
  )
  const retry: Record<ProxySource, () => void> = {
    status: refreshStatus,
    sites: vhosts.refresh,
    certificates: certs.refresh,
    renewal: certbot.refresh,
    streams: streams.refresh,
    ports: ports.refresh,
  }
  const settled =
    !vhosts.loading && !certs.loading && !certbot.loading && !streams.loading && !ports.loading

  // Loading until the status first answers, and its error once it fails —
  // the page used to render nothing at all.
  if (!status) {
    return (
      <PageState
        eyebrow="Advanced"
        title="Proxy & TLS"
        error={statusError}
        onRetry={refreshStatus}
      />
    )
  }

  const hasEngine = status.nginx || status.caddy
  const refreshAll = () => {
    refreshStatus()
    engine.refresh()
    vhosts.refresh()
  }

  return (
    <Page className="animate-rise">
      <PageContext
        title="Proxy & TLS"
        className="justify-end"
        actions={<Freshness at={updatedAt} refreshing={refreshing} onRefresh={refreshEverything} />}
      />

      <StatGrid columns={4} dense>
        <StatLink href="/proxy/sites" label="Sites">
          <StatTile
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Sites"
            value={<Figure settled={!vhosts.loading}>{sites ? hosts.length : undefined}</Figure>}
            hint={
              vhosts.error ? (
                <Unread error={vhosts.error} />
              ) : sites ? (
                disabled > 0 ? (
                  `${onTls} on TLS · ${disabled} disabled`
                ) : (
                  `${onTls} on TLS`
                )
              ) : undefined
            }
            tone={vhosts.error || disabled > 0 ? "warning" : "default"}
          />
        </StatLink>
        <StatLink href="/proxy/certificates" label="Certificates">
          <StatTile
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Certificates"
            value={
              <Figure settled={!certs.loading}>
                {certificates ? certificates.length : undefined}
              </Figure>
            }
            hint={
              certs.error ? (
                <Unread error={certs.error} />
              ) : certificates ? (
                badCerts.length > 0 ? (
                  `${badCerts.length} need attention`
                ) : certificates.length > 0 ? (
                  "all valid"
                ) : (
                  "none issued"
                )
              ) : undefined
            }
            tone={
              badCerts.some((c) => c.expired || c.error)
                ? "danger"
                : certs.error || badCerts.length
                  ? "warning"
                  : "default"
            }
          />
        </StatLink>
        <StatLink href="/proxy/streams" label="Streams">
          <StatTile
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Streams"
            value={
              <Figure settled={!streams.loading}>
                {streamStatus ? streamStatus.streams.length : undefined}
              </Figure>
            }
            hint={
              streams.error ? (
                <Unread error={streams.error} />
              ) : streamStatus ? (
                streamStatus.streams.length === 0 ? (
                  "nothing forwarded"
                ) : streamStatus.included ? (
                  "read by nginx"
                ) : (
                  "not read by nginx"
                )
              ) : undefined
            }
            tone={
              streams.error ||
              (streamStatus && streamStatus.streams.length > 0 && !streamStatus.included)
                ? "warning"
                : "default"
            }
          />
        </StatLink>
        <StatLink href="/proxy/ports" label="Exposed ports">
          <StatTile
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Exposed ports"
            value={
              <Figure settled={!ports.loading}>{listeners ? exposed.length : undefined}</Figure>
            }
            hint={
              ports.error ? (
                <Unread error={ports.error} />
              ) : listeners ? (
                exposed.length ? (
                  `of ${listeners.length} listening, off the machine`
                ) : (
                  "everything on loopback"
                )
              ) : undefined
            }
            tone={ports.error ? "warning" : "default"}
          />
        </StatLink>
      </StatGrid>

      <EngineIdentity
        status={status}
        unit={engine.unit}
        unitName={engine.name}
        unitError={engine.error}
        fetchedAt={engine.fetchedAt}
        certbotVersion={renewal?.version}
        renewSource={renewal ? (renewal.renewSource ?? null) : undefined}
        actions={
          admin &&
          hasEngine && (
            <EngineActions
              status={status}
              unitName={engine.name}
              unit={engine.unit}
              onChanged={refreshAll}
            />
          )
        }
      />

      {/* Route destinations need room for both ends; verdicts fit in the rail. */}
      <div className="grid items-start gap-8 xl:grid-cols-[minmax(0,1fr)_20rem] [&>*]:min-w-0">
        <Panel plain>
          <PanelHeader
            title="Routes"
            actions={
              hosts.length > 0 && (
                <div className="flex items-center gap-3">
                  {/* The eight that need the reader most, and how many the Sites page has. */}
                  {routes.total > routes.shown.length && (
                    <span className="numeric text-hint text-muted-foreground">
                      Showing {routes.shown.length} of {routes.total}
                    </span>
                  )}
                  <Link
                    href="/proxy/sites"
                    className="flex items-center gap-1 text-hint font-medium text-muted-foreground hover:text-foreground"
                  >
                    All sites <ArrowRight className="size-3" />
                  </Link>
                </div>
              )
            }
          />
          <PanelBody flush>
            {vhosts.loading ? (
              <div className="space-y-3 py-1">
                <Skeleton className="h-4 w-64" />
                <Skeleton className="h-4 w-48" />
              </div>
            ) : vhosts.error ? (
              <ErrorState error={vhosts.error} onRetry={vhosts.refresh} className="mt-2" />
            ) : hosts.length === 0 ? (
              <EmptyState
                icon={Globe}
                title="Nothing configured yet"
                description={
                  status.nginx
                    ? "Add a site to put a domain in front of something on this machine, or issue a certificate from the Certificates tab."
                    : "No nginx sites, Caddyfile or shared Caddy ingress was found on this host."
                }
                className="mt-2"
              />
            ) : (
              // Every row here is a site to open, which is the case §16 names:
              // a list of destinations becomes a `ChoiceList` and gets the edge,
              // on a reading page as much as on a flow one. It read as a listing
              // while it was the same row the tables below it use for values.
              <ChoiceList aria-label="Sites" className="animate-rise">
                {routes.shown.map((vhost) => {
                  const target = routeTarget(vhost, admin)
                  return (
                    <ChoiceRow
                      key={`${vhost.kind}:${vhost.name}:${vhost.path}`}
                      href={target.open === "page" ? target.href : undefined}
                      onSelect={target.open === "file" ? () => setViewing(vhost) : undefined}
                      disabled={target.open === "none"}
                      verb={target.verb}
                      className="gap-4 p-4"
                      leading={<ProductLogo id={siteProduct(vhost)} size="md" />}
                      title={<span className="text-title">{vhost.name}</span>}
                      description={routeKind(vhost)}
                      trailing={<ServingStatus vhost={vhost} />}
                    >
                      <RoutePath
                        source={vhost.serverNames.join(", ") || "Default host"}
                        destination={vhost.upstreams.join(", ") || "Served by configuration"}
                      />
                      <div className="flex flex-wrap items-center justify-between gap-2 text-hint text-muted-foreground">
                        <SiteTLS vhost={vhost} />
                        <span className="font-mono">{vhost.listen.join(" · ")}</span>
                      </div>
                    </ChoiceRow>
                  )
                })}
              </ChoiceList>
            )}
          </PanelBody>
        </Panel>

        <div className="min-w-0 space-y-8">
          {/* A titled list on the page, not a box, for the reason the host
          overview's health list is: the findings are the first thing to read
          after the figures, and a frame around them opened the page with a
          stack of containers. */}
          <Panel plain>
            <PanelHeader title="Needs attention" />
            <PanelBody>
              {!settled ? (
                <div className="space-y-2">
                  <Skeleton className="h-4 w-56" />
                  <Skeleton className="h-4 w-40" />
                </div>
              ) : (
                <div className="animate-rise">
                  <FindingList
                    findings={findings.map((f) => {
                      const source = unreadableSource(f)
                      return {
                        ...f,
                        action: source
                          ? { label: "Try again", onClick: retry[source] }
                          : { label: findingAction(f), onClick: () => router.push(f.href) },
                      }
                    })}
                    emptyLabel="Certificates, renewal, sites, streams and exposed ports all within limits"
                  />
                </div>
              )}
            </PanelBody>
          </Panel>

          {/* The four figures above count the certificates; none of them says
            which one runs out first, and that is the reading somebody opens a
            certificate list for. §7: the meter's track behind each name, the
            figure at the right, and a signal segment for the share that is
            wrong. */}
          <Panel plain>
            <PanelHeader
              title="Certificate expiry"
              actions={
                certExpiry.length > 0 && (
                  <Link
                    href="/proxy/certificates"
                    className="flex items-center gap-1 text-hint font-medium text-muted-foreground hover:text-foreground"
                  >
                    All certificates <ArrowRight className="size-3" />
                  </Link>
                )
              }
            />
            <PanelBody flush>
              {certs.loading ? (
                <div className="space-y-3 py-1">
                  <Skeleton className="h-4 w-48" />
                  <Skeleton className="h-4 w-56" />
                </div>
              ) : certs.error ? (
                <ErrorState error={certs.error} onRetry={certs.refresh} className="mt-2" />
              ) : (
                <BarList
                  className="animate-rise"
                  items={certExpiry}
                  emptyLabel={
                    status.certbot
                      ? "Nothing issued yet — issue a certificate from the Certificates tab."
                      : "No certificates were found on this host."
                  }
                />
              )}
            </PanelBody>
          </Panel>
        </div>
      </div>

      <ConfigEditor
        open={viewing !== null}
        onOpenChange={(open) => !open && setViewing(null)}
        path={viewing?.path ?? ""}
        kind={viewing?.kind ?? "nginx"}
        title={viewing?.name ?? "Configuration"}
        readOnly
      />
    </Page>
  )
}

/**
 * How old the page is, and the one press that reads every source again. The
 * age is the oldest reading's, so a five-minute-old certificate list is not
 * vouched for by a sites list read a moment ago; while a refresh is out it
 * says so instead, until the last source answers.
 */
function Freshness({
  at,
  refreshing,
  onRefresh,
}: {
  at: number | undefined
  refreshing: boolean
  onRefresh: () => void
}) {
  const now = useNow(1000, at !== undefined && !refreshing)
  return (
    <>
      <span className="numeric text-hint text-muted-foreground">
        {refreshing ? "Refreshing…" : at !== undefined && updatedLabel(at, now)}
      </span>
      <Button size="xs" variant="ghost" onClick={onRefresh} pending={refreshing}>
        <RefreshClockwise />
        Refresh
      </Button>
    </>
  )
}

/**
 * A tile's figure that rises once its own poll settles — swapping the key
 * between skeleton and value remounts it — so the four fill in rather than
 * flickering from bone to number.
 */
function Figure({ settled, children }: { settled: boolean; children: React.ReactNode }) {
  if (!settled) return <Skeleton className="my-1.5 h-5 w-12" />
  return (
    <span key="figure" className="inline-block animate-rise">
      {children ?? "—"}
    </span>
  )
}
