"use client"

import { Fragment, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, Bell, Globe, RefreshClockwise } from "@/components/icons"
import { ApiError, errorMessage, get, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type {
  CertbotState,
  DefaultSite,
  ErrorReport,
  SiteTrafficSummary,
  UpstreamReport,
} from "@/lib/types"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Page, PageContext, PageState } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { FactDot } from "@/components/metrics/host-identity"
import { Status, type DotTone } from "@/components/status-dot"
import { EmptyState, ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { useNow } from "@/components/deploy/vocabulary"
import { useProxy } from "@/components/proxy/proxy-context"
import {
  EngineActions,
  EngineExtras,
  EngineIdentity,
  useEngineUnit,
} from "@/components/proxy/engine"
import { EngineLog } from "@/components/proxy/engine-log"
import { useEngineControl } from "@/components/proxy/engine-control"
import { EngineFailure } from "@/components/proxy/engine-failure"
import { PARTICIPLE, engineRun } from "@/components/proxy/engine-lifecycle"
import { LiveTraffic } from "@/components/proxy/live-metrics"
import { useServedDrift } from "@/components/proxy/served-drift"
import { engineProduct } from "@/components/proxy/marks"
import { RouteMark, ServingStatus, SiteTLS } from "@/components/proxy/site-marks"
import { isParked } from "@/components/proxy/site-order"
import {
  CONFIG_TEST,
  SERVED_STALE,
  findingAction,
  foldProxyFindings,
  unreadableSource,
  type ProxySource,
  type UnreadableSource,
} from "@/components/proxy/attention"
import {
  noAnswerLabel,
  oldestReading,
  reading,
  REFRESH_DEADLINE_MS,
  unanswered,
  updatedLabel,
  type Answer,
  type Reading,
} from "@/components/proxy/freshness"
import { overviewRoutes, routeKind, routeTarget } from "@/components/proxy/overview-routes"
import { upstreamLabel, upstreamsOf, upstreamTone } from "@/components/proxy/upstream-health"
import { trafficLabel } from "@/components/proxy/site-traffic"
import { AlertsPanel } from "@/components/proxy/alerts-panel"
import { ProxyFindings } from "@/components/proxy/finding-triage"
import { RecentChanges } from "@/components/proxy/activity"
import { frontDoorLine, frontDoors } from "@/components/proxy/front-door"
import { unproxiedApps } from "@/components/proxy/suggest"
import { foldDualStack, privateNetworksHint, tallyReach } from "@/components/proxy/ports"
import { portsHref } from "@/components/proxy/ports-list"
import { routeMap, routeVerdict, siteBackend } from "@/components/proxy/route-map"
import { RoutePicture } from "@/components/proxy/route-picture"
import { OverviewBand } from "@/components/proxy/overview-band"

/**
 * What a poll last answered, or nothing when its last read failed. A source
 * that failed is unknown, not what it said before: judged from a stale answer
 * or from none, the page read "all within limits" and "nothing configured"
 * about a host it could not see.
 */
function readable<T>(poll: PollState<Reading<T>>): Reading<T> | undefined {
  return poll.error ? undefined : poll.data
}

/** One of the engine line's facts about what it serves. */
type Fact = {
  key: string
  href: string
  text?: string
  title?: string
  tone?: "warning" | "danger"
  error?: Error
}

const RUN_TONE: Record<ReturnType<typeof engineRun>, DotTone> = {
  running: "running",
  stopped: "stopped",
  changing: "warning",
}

/**
 * The engine first, as the line every reading page opens on, then the proxy
 * drawn as what it does: the domains it answers for wired through it to the
 * applications behind them, each the product it is, the wires carrying the
 * hour's traffic. Under the picture the readings — where the hour's requests
 * went, how long every certificate has left, what nginx complained about, and
 * nginx's own counters — then the routes in the wide column with what needs
 * attention in the rail. The engine's own log closes the page, read here
 * rather than on the Logs page, because this is where "why is the proxy
 * unhappy" is asked.
 *
 * It opened on four tiles — sites, certificates, streams, internet-facing
 * ports — over a column of routes that each drew the engine's logo, and the
 * operator found it still. Each figure went where it is said better (§15,
 * pass 2): all four are facts in the engine's line, each a link to its page
 * and each saying "couldn't read" with the reason when its source failed;
 * the sites are the picture's Domains lane and the Routes head's count, the
 * certificates the Certificates block's runway and its count of those that
 * need attention, and the internet-facing ports the front door under the
 * engine in the picture.
 */
export default function ProxyOverviewPage() {
  const {
    status,
    hasNginx,
    updatedAt: statusAt,
    error: statusError,
    refresh: refreshStatus,
    reads: { vhosts, certs, streams, ports },
    configTest,
  } = useProxy()
  const { can } = useAuth()
  const router = useRouter()
  const admin = can("system.admin")
  const engine = useEngineUnit(status)
  const [alertsOpen, setAlertsOpen] = useState(false)

  const certbot = usePoll(
    (signal) => reading(get<CertbotState>("/certificates/certbot", undefined, signal)),
    300_000,
    [],
    { enabled: Boolean(status?.certbot) },
  )
  // Whether each route's upstream answers. The server checks at most every
  // fifteen seconds whoever asks; a host it cannot check answers 404 or 503,
  // and the routes then show no reading rather than a failure of their own.
  const upstreams = usePoll(
    (signal) => get<UpstreamReport>("/proxy/upstreams", undefined, signal),
    30_000,
    [],
    { enabled: Boolean(status?.nginx) },
  )
  // Each site's last hour and nginx's recent errors, read from the logs on
  // this host. A digest beside the routes rather than a source of findings:
  // a failed read says so in its own panel and nowhere else.
  const traffic = usePoll(
    (signal) => get<SiteTrafficSummary>("/proxy/traffic", undefined, signal),
    60_000,
    [],
    { enabled: Boolean(status?.nginx) },
  )
  const nginxErrors = usePoll(
    (signal) => get<ErrorReport>("/proxy/errors", { window: "1h" }, signal),
    60_000,
    [],
    { enabled: Boolean(status?.nginx) },
  )
  const [checking, setChecking] = useState(false)
  const checkUpstreams = async () => {
    setChecking(true)
    try {
      await post<UpstreamReport>("/proxy/upstreams/check")
      upstreams.refresh()
    } catch (err) {
      notify.error("Couldn't check the upstreams", err)
    } finally {
      setChecking(false)
    }
  }
  // The engine's last config test (the layout's, which `t` runs), for an
  // account that may run one: its warnings stay in Needs attention until a
  // test is clean.
  // A reload or a service verb changes what the engine line, the status and
  // the sites say, and runs a config test; each reads them again once it lands.
  const refreshAll = () => {
    refreshStatus()
    engine.refresh()
    vhosts.refresh()
    configTest.refreshLast()
  }
  const control = useEngineControl({ status, unit: engine, onChanged: refreshAll })
  const defaultSite = usePoll<DefaultSite>(
    (signal) => get("/proxy/default-site", undefined, signal),
    300_000,
    [],
    { enabled: hasNginx },
  )
  const drift = useServedDrift({ onReloaded: refreshAll })

  // certbot being absent is a fact about the host, not a failure to report.
  const certbotGone =
    status?.certbot === false ||
    (certbot.error instanceof ApiError && certbot.error.code === "certbot_unavailable")
  const certbotAsked = Boolean(status?.certbot)

  // Every read the page shows, as Refresh waits on them, by the name the
  // header gives one that does not answer in time: a poll that is switched
  // off is not asked and not waited for.
  const answers: Record<string, Answer> = {
    "the proxy status": { at: statusAt, error: statusError },
    sites: { at: vhosts.data?.at, error: vhosts.error },
    certificates: { at: certs.data?.at, error: certs.error },
    streams: { at: streams.data?.at, error: streams.error },
    ports: { at: ports.data?.at, error: ports.error },
    ...(engine.name ? { [engine.name]: { at: engine.fetchedAt, error: engine.error } } : {}),
    ...(certbotAsked ? { renewal: { at: certbot.data?.at, error: certbot.error } } : {}),
  }
  // What each source had said when Refresh was pressed, and whether the wait
  // for the rest has run out.
  const [asked, setAsked] = useState<{ answers: Record<string, Answer>; overdue: boolean }>()
  useEffect(() => {
    if (!asked || asked.overdue) return
    const timer = setTimeout(
      () => setAsked((current) => (current === asked ? { ...asked, overdue: true } : current)),
      REFRESH_DEADLINE_MS,
    )
    return () => clearTimeout(timer)
  }, [asked])
  const waiting = asked ? unanswered(asked.answers, answers) : []
  const refreshing = waiting.length > 0 && !asked?.overdue
  const late = asked?.overdue ? waiting : []
  // The age the page vouches for is that of its oldest reading. A source
  // that failed shows none of its reading, so it has no age to count — but
  // the status goes on drawing the engine line from its last answer, so that
  // answer's age still counts.
  const updatedAt = oldestReading([
    statusAt,
    engine.fetchedAt,
    readable(vhosts)?.at,
    readable(certs)?.at,
    readable(streams)?.at,
    readable(ports)?.at,
    certbotAsked && !certbotGone ? readable(certbot)?.at : undefined,
  ])
  const refreshEverything = () => {
    setAsked({ answers, overdue: false })
    refreshStatus()
    engine.refresh()
    vhosts.refresh()
    certs.refresh()
    if (certbotAsked) certbot.refresh()
    streams.refresh()
    ports.refresh()
    upstreams.refresh()
    traffic.refresh()
    nginxErrors.refresh()
    configTest.refreshLast()
  }

  const sites = readable(vhosts)?.value
  const certificates = readable(certs)?.value
  const streamStatus = readable(streams)?.value
  const streamDirError =
    streams.error instanceof ApiError && streams.error.code === "stream_dir_unreadable"
      ? streams.error
      : undefined
  const listeners = readable(ports)?.value
  const upstreamReport = upstreams.error ? undefined : upstreams.data
  const siteTraffic = traffic.error ? undefined : traffic.data?.sites
  const trafficBySite = new Map(
    (siteTraffic ?? []).filter((t) => t.status === "available").map((t) => [t.site, t]),
  )
  const errorReport = nginxErrors.error ? undefined : nginxErrors.data
  const hosts = sites ?? []
  const routes = useMemo(() => overviewRoutes(sites ?? []), [sites])
  const doors = useMemo(() => (listeners ? frontDoors(listeners) : undefined), [listeners])
  // Only once every list it compares has answered: an app judged against a
  // sites list that failed would be offered a domain it already has.
  const suggestions = useMemo(
    () =>
      listeners && sites && streamStatus
        ? unproxiedApps({ ports: listeners, vhosts: sites, streams: streamStatus })
        : [],
    [listeners, sites, streamStatus],
  )
  const onTls = hosts.filter((v) => v.tls).length
  const disabled = hosts.filter(isParked).length
  // The ports page's own split, so the figure clicked through is on that page.
  const reach = useMemo(() => tallyReach(foldDualStack(listeners ?? [])), [listeners])
  const badCerts = useMemo(
    () => (certificates ?? []).filter((c) => c.expired || c.expiring || c.error || c.staging),
    [certificates],
  )
  // The proxy drawn as what it does, and each route's far end as the thing
  // that answers there. A port list that failed only leaves those unnamed.
  const picture = useMemo(
    () =>
      routeMap({
        vhosts: sites ?? [],
        listeners: listeners ?? [],
        upstreams: upstreamReport,
        traffic: siteTraffic,
      }),
    [sites, listeners, upstreamReport, siteTraffic],
  )
  const backends = useMemo(
    () =>
      new Map((sites ?? []).map((v) => [v.name, siteBackend(v, listeners ?? [], upstreamReport)])),
    [sites, listeners, upstreamReport],
  )
  const verdict = useMemo(() => routeVerdict(sites ?? [], upstreamReport), [sites, upstreamReport])
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
  const lastTest = configTest.last
  const testEngine = configTest.engine
  const findings = useMemo(
    () =>
      foldProxyFindings({
        certs: certificates,
        certbot: certbotGone ? null : renewal,
        vhosts: sites,
        streams: streamStatus,
        // A failed read of the streams is already an unreadable source
        // below; only a directory nginx cannot be read from has a finding
        // of its own, since its fix is on the Streams page.
        streamsError: streamDirError,
        ports: listeners,
        unreadable,
        lastTest: lastTest && { engine: testEngine, record: lastTest },
        defaultSite: defaultSite.data,
        drift: drift.report,
        upstreams: upstreamReport,
      }),
    [
      certificates,
      renewal,
      certbotGone,
      sites,
      streamStatus,
      streamDirError,
      listeners,
      unreadable,
      lastTest,
      testEngine,
      defaultSite.data,
      drift.report,
      upstreamReport,
    ],
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
  // Only the verbs that change whether it runs are said in place of its state.
  const underWay =
    control.pending === "start" || control.pending === "restart" || control.pending === "stop"
      ? PARTICIPLE[control.pending]
      : undefined
  // The engine's state in its tile's corner in the picture: a unit's, or a
  // running ingress container's, and nothing claimed for one not read.
  const engineTone: DotTone = engine.unit
    ? engine.unit.activeState === "failed"
      ? "danger"
      : RUN_TONE[engineRun(engine.unit)]
    : status.ingressContainer && !engine.error
      ? "running"
      : "unknown"

  // The four figures the tiles held, as facts after the engine's own: each
  // opens its page, and one whose source failed says so with the reason.
  const certTone: Fact["tone"] = badCerts.some((c) => c.expired || c.error || c.staging)
    ? "danger"
    : badCerts.length > 0
      ? "warning"
      : undefined
  const facts: Fact[] = [
    {
      key: "sites",
      href: "/proxy/sites",
      error: vhosts.error,
      text: sites && `${plural(hosts.length, "site")}, ${onTls} on TLS`,
    },
    ...(disabled > 0 && sites
      ? [
          {
            key: "disabled",
            href: "/proxy/sites",
            text: `${disabled} disabled`,
            tone: "warning" as const,
          },
        ]
      : []),
    {
      key: "certificates",
      href: "/proxy/certificates",
      error: certs.error,
      text:
        certificates &&
        (badCerts.length > 0
          ? `${plural(certificates.length, "certificate")}, ${badCerts.length} ${badCerts.length === 1 ? "needs" : "need"} attention`
          : plural(certificates.length, "certificate")),
      tone: certTone,
    },
    {
      key: "streams",
      href: "/proxy/streams",
      error: streams.error,
      text:
        streamStatus &&
        (streamStatus.streams.length > 0 && !streamStatus.included
          ? `${plural(streamStatus.streams.length, "stream")}, not read by nginx`
          : plural(streamStatus.streams.length, "stream")),
      tone:
        streamStatus && streamStatus.streams.length > 0 && !streamStatus.included
          ? ("warning" as const)
          : undefined,
    },
    {
      key: "ports",
      // The ports page's own split, so the figure clicked through is on that page.
      href: portsHref({ reach: "internet" }),
      error: ports.error,
      text: listeners && `${plural(reach.internet, "port")} internet-facing`,
      title: listeners && privateNetworksHint(reach),
    },
  ]
  const inventory = facts.filter((fact) => fact.error || fact.text)

  return (
    <Page className="animate-rise">
      <PageContext
        title="Proxy & TLS"
        className="justify-end"
        actions={
          <>
            <Freshness
              at={updatedAt}
              refreshing={refreshing}
              late={late}
              onRefresh={refreshEverything}
            />
            {admin && (
              <Button size="xs" variant="ghost" onClick={() => setAlertsOpen(true)}>
                <Bell />
                Alerts
              </Button>
            )}
          </>
        }
      />
      {admin && <AlertsPanel open={alertsOpen} onOpenChange={setAlertsOpen} />}

      <EngineIdentity
        status={status}
        unit={engine.unit}
        unitName={engine.name}
        unitError={engine.error}
        fetchedAt={engine.fetchedAt}
        statusAt={statusAt}
        pending={underWay}
        onStartAtBoot={admin ? () => control.run("enable") : undefined}
        serviceBusy={control.pending}
        certbotVersion={renewal?.version}
        renewSource={renewal ? (renewal.renewSource ?? null) : undefined}
        inventory={inventory.map((fact) => (
          <Fragment key={fact.key}>
            <FactDot />
            {fact.error ? (
              <span className="font-medium text-warning" title={errorMessage(fact.error)}>
                {`couldn't read ${fact.key}`}
              </span>
            ) : (
              <Link
                href={fact.href}
                title={fact.title}
                className={cn(
                  "rounded-sm focus-ring transition-colors hover:text-foreground hover:underline",
                  fact.tone === "danger" && "font-medium text-destructive",
                  fact.tone === "warning" && "font-medium text-warning",
                )}
              >
                {fact.text}
              </Link>
            )}
          </Fragment>
        ))}
        verdict={verdict && <Status verdict={verdict.tone} label={verdict.label} />}
        actions={
          admin &&
          hasEngine && (
            <EngineActions
              status={status}
              unitName={engine.name}
              unit={engine.unit}
              control={control}
              testing={configTest.running}
              onTest={configTest.run}
              onReloadTested={configTest.showReload}
              onChanged={refreshAll}
            />
          )
        }
      />

      <EngineExtras status={status} admin={admin} onChanged={refreshAll} />

      {engine.unit?.activeState === "failed" && !underWay && (
        <EngineFailure
          engine={control.engine}
          unit={engine.unit}
          onClear={admin ? () => control.run("reset-failed") : undefined}
          busy={control.pending}
        />
      )}

      {/* The picture needs the sites; while they are read it holds its
          height, and a failure is said once, where the routes are. */}
      {hasEngine &&
        (vhosts.loading ? (
          <Skeleton className="h-72 w-full" />
        ) : (
          picture.domains.length > 0 && (
            <RoutePicture
              map={picture}
              engine={status.nginx ? "nginx" : "Caddy"}
              engineMark={engineProduct(status)}
              engineTone={engineTone}
              engineHint={doors && <span className="font-mono">{frontDoorLine(doors)}</span>}
            />
          )
        ))}

      <OverviewBand
        nginx={status.nginx}
        traffic={{ sites: siteTraffic, error: traffic.error }}
        certificates={{
          list: certificates,
          loading: certs.loading,
          error: certs.error,
          onRetry: certs.refresh,
          certbot: status.certbot,
        }}
        errors={{ report: errorReport, error: nginxErrors.error }}
        productOf={(site) => backends.get(site)?.product}
      />

      {status.nginx && <LiveTraffic admin={admin} />}

      {/* Route destinations need room for both ends; verdicts fit in the rail. */}
      <div className="grid items-start gap-8 xl:grid-cols-[minmax(0,1fr)_20rem] [&>*]:min-w-0">
        <Panel plain>
          <PanelHeader
            title="Routes"
            actions={
              hosts.length > 0 && (
                <div className="flex items-center gap-3">
                  {admin && upstreamReport && (
                    <Button size="xs" variant="ghost" onClick={checkUpstreams} pending={checking}>
                      Check upstreams
                    </Button>
                  )}
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
              // on a reading page as much as on a flow one. Each leads with
              // what its visitors reach, the engine in the tile's corner.
              <ChoiceList aria-label="Sites" className="animate-rise">
                {routes.shown.map((vhost) => {
                  const target = routeTarget(vhost)
                  const reached = upstreamsOf(upstreamReport, vhost.path)
                  const load = vhost.kind === "nginx" ? trafficBySite.get(vhost.name) : undefined
                  const backend = backends.get(vhost.name) ?? siteBackend(vhost, [])
                  return (
                    <ChoiceRow
                      key={`${vhost.kind}:${vhost.name}:${vhost.path}`}
                      href={target.href}
                      verb={target.verb}
                      className="gap-3 p-3.5"
                      leading={<RouteMark vhost={vhost} backend={backend} />}
                      title={<span className="text-title">{vhost.name}</span>}
                      description={
                        backend.kind === "app"
                          ? `${routeKind(vhost)} → ${backend.name}`
                          : routeKind(vhost)
                      }
                      trailing={<ServingStatus vhost={vhost} />}
                    >
                      <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 text-hint text-muted-foreground">
                        <SiteTLS vhost={vhost} />
                        <span className="flex min-w-0 items-center gap-1.5 font-mono">
                          <span className="truncate">
                            {vhost.serverNames.join(", ") || "Default host"}
                          </span>
                          <ArrowRight aria-hidden className="size-3 shrink-0" />
                          <span className="truncate text-foreground">
                            {vhost.upstreams.join(", ") ||
                              (backend.kind === "files"
                                ? backend.address
                                : "Served by configuration")}
                          </span>
                        </span>
                        {load && <span className="numeric">{trafficLabel(load)}</span>}
                        <span className="ml-auto font-mono">{vhost.listen.join(" · ")}</span>
                      </div>
                      {reached.length > 0 && (
                        <ul aria-label="Upstreams" className="space-y-1 text-hint">
                          {reached.map((t) => (
                            <li
                              key={`${t.line}:${t.address}`}
                              className="flex flex-wrap items-center gap-x-2"
                            >
                              <span className="font-mono text-muted-foreground">{t.address}</span>
                              <Status tone={upstreamTone(t.state)} label={upstreamLabel(t)} />
                              {t.owner && <span className="text-muted-foreground">{t.owner}</span>}
                            </li>
                          ))}
                        </ul>
                      )}
                    </ChoiceRow>
                  )
                })}
              </ChoiceList>
            )}
          </PanelBody>
        </Panel>

        <div className="min-w-0 space-y-8">
          {/* A titled list on the page, not a box, for the reason the host
          overview's health list is: a frame around it would open the rail
          with a container. */}
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
                  <ProxyFindings
                    findings={findings.map((f) => {
                      const source = unreadableSource(f)
                      return {
                        ...f,
                        action: source
                          ? { label: "Try again", onClick: retry[source] }
                          : f.id === CONFIG_TEST
                            ? { label: "Open test", onClick: configTest.showLast }
                            : admin && f.id.startsWith(SERVED_STALE)
                              ? {
                                  label: drift.reloading ? "Reloading nginx…" : "Reload nginx",
                                  onClick: drift.reload,
                                }
                              : { label: findingAction(f), onClick: () => router.push(f.href) },
                      }
                    })}
                    emptyLabel="Certificates, renewal, sites, upstreams, streams and exposed ports all within limits"
                    onRemedied={(remedy) =>
                      remedy.kind === "enable-site" ? refreshAll() : certbot.refresh()
                    }
                  />
                </div>
              )}
            </PanelBody>
          </Panel>

          {admin && status.nginx && suggestions.length > 0 && (
            <Panel plain>
              <PanelHeader title="Not behind a domain" />
              <PanelBody flush>
                <ul aria-label="Apps on loopback" className="animate-rise space-y-2 pt-1">
                  {suggestions.slice(0, 5).map((app) => (
                    <li key={app.port} className="flex min-w-0 items-center justify-between gap-3">
                      <span className="min-w-0 truncate text-body">
                        <span className="numeric font-mono">:{app.port}</span>{" "}
                        <span className="text-muted-foreground">{app.process || "unknown"}</span>
                      </span>
                      <Button size="xs" variant="outline" asChild>
                        <Link
                          href={`/proxy/sites?new=1&upstream=${encodeURIComponent(app.upstream)}`}
                        >
                          Put a domain in front of this
                        </Link>
                      </Button>
                    </li>
                  ))}
                </ul>
              </PanelBody>
            </Panel>
          )}

          {admin && <RecentChanges />}
        </div>
      </div>

      <EngineLog status={status} />
      {control.dialog}
    </Page>
  )
}

/**
 * How old the page is, and the one press that reads every source again. The
 * age is the oldest reading's, so a five-minute-old certificate list is not
 * vouched for by a sites list read a moment ago; while a refresh is out it
 * says so instead, until the last source answers. A source still out once
 * the wait runs out is named, and Refresh can be pressed again to ask it
 * afresh.
 */
function Freshness({
  at,
  refreshing,
  late,
  onRefresh,
}: {
  at: number | undefined
  refreshing: boolean
  /** The sources that have not answered a refresh in time, by name. */
  late: string[]
  onRefresh: () => void
}) {
  const now = useNow(1000, at !== undefined && !refreshing && late.length === 0)
  return (
    <>
      {late.length > 0 ? (
        <span className="text-hint font-medium text-warning" title={late.join(", ")}>
          {noAnswerLabel(late)}
        </span>
      ) : (
        <span className="numeric text-hint text-muted-foreground">
          {refreshing ? "Refreshing…" : at !== undefined && updatedLabel(at, now)}
        </span>
      )}
      <Button size="xs" variant="ghost" onClick={onRefresh} pending={refreshing}>
        <RefreshClockwise />
        Refresh
      </Button>
    </>
  )
}
