"use client"

import { INLINE_LINK } from "@/components/inline-link"
import { Fragment, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, Bell, Globe } from "@/components/icons"
import { ApiError, errorMessage, get, post } from "@/lib/api"
import { ago, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type {
  CertbotState,
  ErrorReport,
  ProxyAlerts,
  SiteTrafficSummary,
  UpstreamReport,
  UpstreamTarget,
} from "@/lib/types"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Page, PageContext, PageState } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ChoiceList, ChoiceListSkeleton, ChoiceRow } from "@/components/choice-row"
import { Fact } from "@/components/fact-line"
import { Status, type DotTone } from "@/components/status-dot"
import { EmptyState, ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { useNow } from "@/hooks/use-now"
import type { Verb } from "@/components/verbs"
import { useProxy } from "@/components/proxy/proxy-context"
import {
  EngineExtras,
  EngineIdentity,
  EngineMenu,
  ModulesFact,
  useEngineCommands,
  usePackageInstall,
  useEngineUnit,
} from "@/components/proxy/engine"
import { EngineLog } from "@/components/proxy/engine-log"
import { useEngineControl } from "@/components/proxy/engine-control"
import { EngineFailure } from "@/components/proxy/engine-failure"
import { EngineSetup } from "@/components/proxy/engine-setup"
import { PARTICIPLE, engineRun } from "@/components/proxy/engine-lifecycle"
import { LiveTraffic } from "@/components/proxy/live-metrics"
import { engineProduct } from "@/components/proxy/marks"
import { HourWords, RouteMark, ServingStatus, SiteTLS } from "@/components/proxy/site-marks"
import { siteTitle } from "@/components/proxy/site-details"
import { isParked } from "@/components/proxy/site-order"
import {
  noAnswerLabel,
  oldestReading,
  reading,
  REFRESH_DEADLINE_MS,
  unanswered,
  type Answer,
  type Reading,
} from "@/components/proxy/freshness"
import {
  isDefaultServer,
  listenPorts,
  overviewRoutes,
  routeKind,
  routeNames,
  routeTarget,
} from "@/components/proxy/overview-routes"
import {
  isDown,
  upstreamLabel,
  upstreamsOf,
  upstreamTone,
} from "@/components/proxy/upstream-health"
import { RecentChanges } from "@/components/proxy/activity"
import { frontDoorLine, frontDoors } from "@/components/proxy/front-door"
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
type InventoryFact = {
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
 * The engine first, as the line a project opens on — its mark, its name, its
 * state, one command and the rest behind `⋯` — then the proxy drawn as what
 * it does: the domains it answers for wired through it to the
 * applications behind them, each the product it is, the wires carrying the
 * hour's traffic. Under the picture the readings — where the hour's requests
 * went, how long every certificate has left, what nginx complained about, and
 * nginx's own counters — then the configured routes. The engine's own log closes the page, read here
 * rather than on the Logs page, because this is where "why is the proxy
 * unhappy" is asked.
 *
 * It opened on four tiles — sites, certificates, streams, internet-facing
 * ports — over a column of routes that each drew the engine's logo, and the
 * operator found it still. Each figure went where it is said better (§15,
 * pass 2): all four are facts in the engine's line, each a link to its page
 * and each saying "couldn't read" with the reason when its source failed;
 * the sites are the picture's Domains lane and the Routes head's count, the
 * certificates the Certificates block's runway, and the internet-facing ports the front door under the
 * engine in the picture.
 */
export default function ProxyOverviewPage() {
  const {
    status,
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
  // What is firing, for the Alerts link's count: the rules themselves are
  // their own page now, where they were a sheet over this one.
  const alerts = usePoll(
    (signal) => get<ProxyAlerts>("/proxy/alerts", undefined, signal),
    60_000,
    [],
    { enabled: admin },
  )

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
  // Each site's last hour — nginx's files and the Docker Caddy ingress's
  // routes — and nginx's recent errors. A digest beside the routes rather
  // than a source of findings: a failed read says so in its own panel and
  // nowhere else. The server reads only what each log gained since the last
  // ask, so Live traffic's quarter-minute cadence is cheap.
  const traffics = Boolean(status?.nginx || status?.ingressContainer)
  const traffic = usePoll(
    (signal) => get<SiteTrafficSummary>("/proxy/traffic", undefined, signal),
    15_000,
    [],
    { enabled: traffics },
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
  // A reload or a service verb changes what the engine line, the status and
  // the sites say, and runs a config test; each reads them again once it lands.
  const refreshAll = () => {
    refreshStatus()
    engine.refresh()
    vhosts.refresh()
    configTest.refreshLast()
  }
  const control = useEngineControl({ status, unit: engine, onChanged: refreshAll })
  const installer = usePackageInstall(refreshAll)
  const commands = useEngineCommands({
    status,
    enabled: admin && Boolean(status?.nginx || status?.caddy),
    unitName: engine.name,
    unit: engine.unit,
    control,
    testing: configTest.running,
    onTest: configTest.run,
    onReloadTested: configTest.showReload,
    onChanged: refreshAll,
  })

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

  // Loading until the status first answers, and its error once it fails —
  // the page used to render nothing at all.
  if (!status) {
    return <PageState title="Proxy & TLS" error={statusError} onRetry={refreshStatus} />
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
  const certTone: InventoryFact["tone"] = badCerts.some((c) => c.expired || c.error || c.staging)
    ? "danger"
    : badCerts.length > 0
      ? "warning"
      : undefined
  const facts: InventoryFact[] = [
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
          ? `${plural(certificates.length, "certificate")}, ${badCerts.length} not valid`
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
  // With no engine, "0 sites, 0 on TLS · 0 certificates · 0 streams" says
  // what the setup under the line says better; the open ports still matter.
  // With one, a zero count of certificates or streams is dropped too: the
  // Certificates block says "No certificates yet" with the way to issue one,
  // and a host with no streams has nothing to open on Streams.
  const none = (fact: InventoryFact) =>
    (fact.key === "certificates" && certificates?.length === 0) ||
    (fact.key === "streams" && streamStatus?.streams.length === 0)
  // The facts that want action lead: on a phone the line is one line that
  // clips what does not fit, and "2 certificates, 2 not valid" was clipped
  // behind "3 sites, 2 on TLS". Danger first, then a source that failed and
  // warnings, then the expected counts; each keeps its order within its rank.
  const urgency = (fact: InventoryFact) =>
    fact.tone === "danger" ? 0 : fact.error || fact.tone === "warning" ? 1 : 2
  const inventory = facts
    .filter((fact) => hasEngine || fact.key === "ports")
    .filter((fact) => fact.error || (fact.text && !none(fact)))
    .map((fact, at) => ({ fact, at }))
    .sort((a, b) => urgency(a.fact) - urgency(b.fact) || a.at - b.at)
    .map(({ fact }) => fact)
  // What wants action goes in the line's urgent slot, which leads the engine's
  // own facts (ingress, certbot): "2 certificates, 2 not valid" before
  // "certbot", so a phone's one line of facts still shows it.
  const wanting = inventory.filter((fact) => urgency(fact) < 2)
  const counted = inventory.filter((fact) => urgency(fact) === 2)
  const inventoryFact = (fact: InventoryFact) =>
    fact.error ? (
      <Fact key={fact.key} className="font-medium text-warning" title={errorMessage(fact.error)}>
        {`couldn't read ${fact.key}`}
      </Fact>
    ) : (
      <Fact key={fact.key}>
        <Link
          href={fact.href}
          title={fact.title}
          className={cn(
            // A fact that opens its page is a link on the line: underlined at
            // rest (INLINE_LINK) and a finger's press wide (hit-area).
            INLINE_LINK,
            "hit-area transition-colors hover:text-foreground",
            fact.tone === "danger" && "font-medium text-destructive",
            fact.tone === "warning" && "font-medium text-warning",
          )}
        >
          {fact.text}
        </Link>
      </Fact>
    )
  // The way to the Alerts page, which is a rule's own page now; what fires
  // is said on the line itself, where it is seen without opening the menu.
  const alertVerbs: Verb[] = admin
    ? [{ key: "alerts", label: "Alerts", icon: Bell, run: () => router.push("/proxy/alerts") }]
    : []
  // A route's file name is the dashboard's; the name its visitors type is
  // what the Traffic and Errors rows lead with.
  const labelOf = (site: string) => {
    const host = hosts.find((v) => v.name === site)
    return host ? siteTitle(host) : site
  }

  return (
    <Page rise>
      <PageContext title="Proxy & TLS" />
      {statusError && <ErrorState error={statusError} onRetry={refreshStatus} />}

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
        urgent={
          <>
            {/* What is firing is the most urgent fact the line has. */}
            {firingFact(admin && !alerts.error ? alerts.data : undefined)}
            {wanting.map(inventoryFact)}
          </>
        }
        inventory={
          <>
            {counted.map(inventoryFact)}
            {/* "Updated just now" would spend the line's width on the one
                reading that is nearly always fine; the menu says it beside
                Refresh. Hidden as a whole fact, so it takes its dot with it. */}
            <Fact
              className={late.length > 0 ? "font-medium text-warning" : "sr-only"}
              title={late.length > 0 ? late.join(", ") : undefined}
            >
              <Freshness at={updatedAt} refreshing={refreshing} late={late} />
            </Fact>
          </>
        }
        modules={status.nginx && <ModulesFact admin={admin} installer={installer} />}
        verdict={verdict && <Status verdict={verdict.tone} label={verdict.label} />}
        command={commands.command}
        menu={
          <EngineMenu
            label={hasEngine ? `More ${control.engine} actions` : "More actions"}
            lead={[...commands.test, ...alertVerbs]}
            refresh={{ at: updatedAt, refreshing, run: refreshEverything }}
            service={commands.service}
          />
        }
      />

      <EngineExtras status={status} admin={admin} installer={installer} />

      {engine.unit?.activeState === "failed" && !underWay && (
        <EngineFailure
          engine={control.engine}
          unit={engine.unit}
          onClear={admin ? () => control.run("reset-failed") : undefined}
          busy={control.pending}
        />
      )}

      {!hasEngine ? (
        <EngineSetup status={status} admin={admin} onInstalled={refreshAll} />
      ) : (
        <>
          {/* The picture needs the sites; while they are read it waits at the
          shape it will have (a grey block of a guessed height stood here, and
          everything under it moved when they landed), and a failure is said
          once, where the routes are. */}
          {hasEngine && (vhosts.loading || picture.domains.length > 0) && (
            <RoutePicture
              map={picture}
              loading={vhosts.loading}
              engine={status.nginx ? "nginx" : "Caddy"}
              engineMark={engineProduct(status)}
              engineTone={engineTone}
              engineHint={
                doors && (
                  // A block, so the line is the height of the grey one that
                  // waits for it: mono set inline in the sans stood 2px taller.
                  <span className="block font-mono">
                    {frontDoorLine(doors, status.nginx ? "nginx" : "caddy")}
                  </span>
                )
              }
            />
          )}

          {traffics && (
            <LiveTraffic
              admin={admin}
              nginx={status.nginx}
              sites={siteTraffic}
              error={traffic.error}
              onRetry={traffic.refresh}
            />
          )}

          <OverviewBand
            traffics={traffics}
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
            appOf={(site) => backends.get(site)}
            labelOf={labelOf}
          />

          {/* Route destinations need room for both ends; verdicts fit in the rail. */}
          <div className="grid items-start gap-8 xl:grid-cols-[minmax(0,1fr)_20rem] [&>*]:min-w-0">
            <Panel plain>
              {/* The section's one verb as a small outline button on the
                  title line (D6), the count, and the way on. The head keeps
                  a control's height before the upstream check answers, so
                  the rows under it do not step down when the button lands. */}
              <PanelHeader
                title="Routes"
                className="[&>div:last-child]:min-h-7"
                count={
                  // The eight that need the reader most, and how many the Sites page has.
                  hosts.length > 0 &&
                  routes.total > routes.shown.length &&
                  `Showing ${routes.shown.length} of ${routes.total}`
                }
                actions={
                  hosts.length > 0 &&
                  admin &&
                  upstreamReport && (
                    <Button size="xs" variant="outline" onClick={checkUpstreams} pending={checking}>
                      Check upstreams
                    </Button>
                  )
                }
                link={hosts.length > 0 ? { href: "/proxy/sites", label: "All sites" } : undefined}
              />
              <PanelBody flush>
                {vhosts.loading ? (
                  // Rows of a route's own height, so the log under the list
                  // moves by the difference in their number and not by a list
                  // arriving where two lines of text stood.
                  <ChoiceListSkeleton rows={3} mark="md" lines={2} />
                ) : vhosts.error ? (
                  <ErrorState error={vhosts.error} onRetry={vhosts.refresh} className="mt-2" />
                ) : hosts.length === 0 ? (
                  <EmptyState
                    icon={Globe}
                    title="Nothing configured yet"
                    description={
                      status.nginx
                        ? "Add a site to put a domain in front of an app, a container or a folder on this machine."
                        : "No nginx sites, Caddyfile or shared Caddy ingress was found on this host."
                    }
                    action={
                      admin &&
                      status.nginx && (
                        <Button asChild size="sm">
                          <Link href="/proxy/sites">
                            Add a site <ArrowRight />
                          </Link>
                        </Button>
                      )
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
                      const load = trafficBySite.get(vhost.name)
                      const backend = backends.get(vhost.name) ?? siteBackend(vhost, [])
                      const ports = listenPorts(vhost.listen)
                      // The names are said once: where they are the route's own
                      // name, the title already says them.
                      const names = routeNames(vhost)
                      const title = siteTitle(vhost)
                      const kind = routeKind(vhost)
                      const sayNames = names !== title
                      const destination =
                        vhost.upstreams.join(", ") ||
                        (backend.kind === "files" ? backend.address : "served by configuration")
                      return (
                        <ChoiceRow
                          key={`${vhost.kind}:${vhost.name}:${vhost.path}`}
                          href={target.href}
                          verb={target.verb}
                          leading={<RouteMark vhost={vhost} backend={backend} />}
                          title={title}
                          description={
                            // One line: what serves it, then the way a request
                            // goes. The route sat on a second line of its own
                            // with the listen addresses pushed to the far edge,
                            // a card twice the height of what it said.
                            <span className="flex min-w-0 items-center gap-1.5">
                              <span className="shrink-0">
                                {/* The application by name, where it has one the
                                    address after it does not already say. */}
                                {backend.kind === "app" &&
                                !destination.includes(backend.name) &&
                                // A deployment's route already says its project.
                                !kind.includes(backend.name)
                                  ? `${kind} → ${backend.name}`
                                  : kind}
                              </span>
                              <span aria-hidden className="shrink-0">
                                ·
                              </span>
                              <span
                                className="flex min-w-0 items-center gap-1.5 font-mono"
                                title={`${names} → ${destination}`}
                              >
                                {sayNames && (
                                  <>
                                    <span
                                      className={cn(
                                        "truncate",
                                        // nginx's catch-all, said in words rather than as its `_`.
                                        isDefaultServer(vhost) &&
                                          names === "any host" &&
                                          "font-sans",
                                      )}
                                    >
                                      {names}
                                    </span>
                                    <ArrowRight aria-hidden className="size-3 shrink-0" />
                                  </>
                                )}
                                <span className="truncate text-foreground">{destination}</span>
                              </span>
                            </span>
                          }
                          trailing={
                            <span className="flex shrink-0 items-center gap-4">
                              {load && <HourWords hour={load} className="max-md:hidden" />}
                              <UpstreamWord targets={reached} />
                              {ports.length > 0 && (
                                <span
                                  className="font-mono text-hint text-muted-foreground max-lg:hidden"
                                  title={vhost.listen.join(" · ")}
                                >
                                  {ports.map((port) => `:${port}`).join(" ")}
                                </span>
                              )}
                              <span className="max-sm:hidden">
                                <SiteTLS vhost={vhost} />
                              </span>
                              <ServingStatus vhost={vhost} />
                            </span>
                          }
                        />
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

              {admin && <RecentChanges />}
            </div>
          </div>

          <EngineLog status={status} />
        </>
      )}
      {control.dialog}
      {commands.dialog}
    </Page>
  )
}

/**
 * Whether what a route forwards to answers, on the route's own line: the
 * one address's reading, or how many of several are down, each of them in
 * the tooltip. They were a list under the row, so a route nginx forwards
 * stood a line taller than one a folder or the ingress serves, and the rows
 * under it moved when the check answered.
 */
function UpstreamWord({ targets }: { targets: UpstreamTarget[] }) {
  if (targets.length === 0) return null
  const down = targets.filter(isDown)
  // Worst first, so the first is the one to say.
  const worst = targets[0]
  const label =
    targets.length === 1
      ? upstreamLabel(worst)
      : down.length > 0
        ? `${down.length} of ${targets.length} down`
        : `${targets.length} up`
  return (
    <span
      // On a phone the line has room for what is wrong, not for what is fine.
      className={down.length === 0 ? "max-sm:hidden" : undefined}
      title={targets
        .map((t) => [t.address, upstreamLabel(t), t.owner].filter(Boolean).join(" "))
        .join("\n")}
    >
      <Status tone={upstreamTone(worst.state)} label={label} />
    </span>
  )
}

/**
 * How old the page is, for a reader who cannot see the menu that says it, and
 * the sources that have not answered a refresh in time, by name. The age is
 * the oldest reading's, so a five-minute-old certificate list is not vouched
 * for by a sites list read a moment ago; while a refresh is out it says so
 * instead, until the last source answers. A source still out once the wait
 * runs out is named, and Refresh can be pressed again to ask it afresh.
 */
function Freshness({
  at,
  refreshing,
  late,
}: {
  at: number | undefined
  refreshing: boolean
  /** The sources that have not answered a refresh in time, by name. */
  late: string[]
}) {
  const now = useNow(1000, at !== undefined && !refreshing && late.length === 0)
  if (late.length > 0) return noAnswerLabel(late)
  return refreshing ? "Refreshing…" : at !== undefined && `Updated ${ago(at, now, { units: 2 })}`
}

/**
 * What is firing, said on the engine's line and leading to the Alerts page: a
 * way to that page with nothing beside it said nothing about whether anybody
 * was being told something right now. Nothing firing is nothing to say.
 */
function firingFact(alerts?: ProxyAlerts) {
  const subjects = alerts?.subjects ?? []
  const firing = subjects.filter((s) => s.state === "firing").length
  const holding = subjects.filter((s) => s.state === "pending").length
  if (firing === 0 && holding === 0) return null
  return (
    <Fact>
      <Link
        href="/proxy/alerts"
        className={cn(
          "numeric hit-area font-medium",
          INLINE_LINK,
          firing > 0 ? "text-destructive" : "text-warning",
        )}
      >
        {firing > 0 ? `${plural(firing, "alert")} firing` : `${plural(holding, "alert")} holding`}
      </Link>
    </Fact>
  )
}
