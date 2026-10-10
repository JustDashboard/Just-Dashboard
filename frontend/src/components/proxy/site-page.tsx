"use client"

import { useRef, useState } from "react"
import Link from "next/link"
import { useParams } from "next/navigation"
import { ArrowLeft, Globe, Logs, Pencil } from "@/components/icons"
import { get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { forgetSessionState, useSessionState } from "@/lib/view-state"
import type { DeploymentRequests, SiteSpec, UpstreamReport, VHost } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Page, PageContext, PageState } from "@/components/page"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { EmptyState, ErrorState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { VerbBar } from "@/components/verbs"
import { EMPTY_REQUEST_QUERY, type RequestQuery } from "@/components/deploy/requests-workspace"
import { siteProduct } from "@/components/proxy/marks"
import { useProxy, useProxyRead } from "@/components/proxy/proxy-context"
import { ServingStatus, SiteTLS } from "@/components/proxy/site-marks"
import { siteCert } from "@/components/proxy/site-filters"
import { siteLogPlan } from "@/components/proxy/site-log-plan"
import { SiteLogs, siteLogViews, type SiteLogView } from "@/components/proxy/site-logs"
import { SiteRouteMap } from "@/components/proxy/site-route-map"
import { siteVerdict, type SiteVerdict } from "@/components/proxy/site-overview"
import { activeOwner } from "@/components/proxy/site-details"
import { SiteForm } from "@/components/proxy/site-form"
import { useSiteVerbs } from "@/components/proxy/site-verbs"
import { ConfigEditor } from "@/components/proxy/config-editor"
import { SiteFileVerbs } from "@/components/proxy/vhosts-panel"
import { SiteBalancing } from "@/components/proxy/site-balancing"
import { SiteControls } from "@/components/proxy/site-controls-panel"
import { poolsOfSite } from "@/components/proxy/upstream-pools"

/** The site's file read back: the form's fields, and where it logs. */
type SiteRead = { spec: SiteSpec; managed: boolean; warnings: string[] }

/** The site's verbs its page draws: the way here is the page itself. */
const PAGE_VERBS = ["edit", "raw", "open", "scan"]

const BACK = (
  <Link
    href="/proxy/sites"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Sites
  </Link>
)

export function SitePage() {
  const { name } = useParams<{ name: string }>()
  const site = decodeURIComponent(name)
  return <SiteBody key={site} name={site} />
}

/**
 * One site, as a place: what it is and whether it is well, on the identity
 * line the section's other destinations open on, with its verbs beside the
 * way back; the way a request reaches it, drawn as the visitors, the names,
 * the engine and what answers behind it (`site-route-map.tsx`); and its logs
 * in the pane a deployment's are read in, where it is rather than on the
 * host Logs page it used to send the reader to, which knew it only as a path.
 * How it spreads its requests and what limits it sets come last: they are
 * how it is set up, read after whether it works.
 *
 * It opened on four figures over the logs — requests a minute, server
 * errors, refused, upstream failures — and lost them at the operator's
 * request. Each went where it is said better: the rate and the probes refused
 * are the route's first node; the server errors are the identity line's
 * verdict, a press of which narrows Requests to them as the tile's did; and
 * the upstream failures are the Errors tab's count.
 *
 * Which logs those are is the site's own business (`site-log-plan.ts`): an
 * nginx site's two files, wherever its directives put them; a route on the
 * shared Caddy ingress, whose requests the ingress records for it. A site
 * whose requests go nowhere of its own says so, with the form that fixes it
 * a press away, because an empty Requests view reads as a site nobody visits.
 */
function SiteBody({ name }: { name: string }) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const { status, loading, refresh: refreshStatus } = useProxy()
  const [form, setForm] = useState({ open: false, session: 0 })
  const [raw, setRaw] = useState(false)
  const [asked, setAsked] = useState<SiteLogView>()
  const [query, setQuery] = useSessionState<RequestQuery>(
    `proxy.site.${name}.requests`,
    EMPTY_REQUEST_QUERY,
  )
  const logsRef = useRef<HTMLDivElement>(null)

  // The section's own read of the sites, which the rail's marks and the
  // Sites page answer from: the page asking again on a timer of its own was
  // a second read of the same list every thirty seconds.
  const vhosts = useProxyRead("vhosts")
  const certs = useProxyRead("certs")
  const ports = useProxyRead("ports")
  const vhost = vhosts.data?.find((v) => v.name === name)
  const nginx = vhost?.kind === "nginx"
  const read = usePoll(
    (signal) => get<SiteRead>(`/proxy/sites/${encodeURIComponent(name)}`, undefined, signal),
    30_000,
    [name],
    { enabled: nginx },
  )
  // How nginx spreads the site's requests, and what it logged meeting each
  // server: read with every route's, which the server checks at most every
  // fifteen seconds whoever asks.
  const upstreams = usePoll(
    (signal) => get<UpstreamReport>("/proxy/upstreams", undefined, signal),
    30_000,
    [],
    { enabled: nginx },
  )
  const plan = vhost && siteLogPlan(vhost, read.data?.spec, status?.ingressContainer)
  // The site's last hour, for the verdict and the route's first node: the
  // record the server holds in memory, which the Requests view reads too. A
  // limit of one caps the rows, not the hour's figures.
  const hour = usePoll<DeploymentRequests>(
    (signal) =>
      get<DeploymentRequests>(
        `/proxy/sites/${encodeURIComponent(name)}/requests`,
        { since: new Date(Date.now() - 3_600_000).toISOString(), limit: 1 },
        signal,
      ),
    30_000,
    [name],
    { enabled: Boolean(plan?.requests) },
  )
  const refresh = () => {
    vhosts.refresh()
    read.refresh()
    upstreams.refresh()
    hour.refresh()
  }
  const openForm = () => setForm((f) => ({ open: true, session: f.session + 1 }))

  const verbs = useSiteVerbs({
    vhost: vhost ?? PLACEHOLDER,
    admin,
    onEdit: openForm,
    onRaw: () => setRaw(true),
    onServed: () => {},
    onDuplicate: () => {},
    onToggle: () => {},
    onDelete: () => {},
    onUnlink: () => {},
    onOverride: () => {},
    onRename: () => {},
  }).filter((verb) => PAGE_VERBS.includes(verb.key))

  // A route on the Docker Caddy ingress is read through the ingress the
  // proxy's status names, so its page waits for that as an nginx site's
  // waits for its file: drawn before, it said the ingress was not running.
  const routed = vhost?.kind === "caddy" && !vhost.path && !vhost.name.startsWith("docker-caddy:")
  if (!vhosts.data || (nginx && !read.data && !read.error) || (routed && !status && loading)) {
    return <PageState eyebrow={BACK} title={name} error={vhosts.error} onRetry={refresh} />
  }
  if (!vhost || !plan) {
    return (
      <Page>
        <PageContext eyebrow={BACK} title={name} />
        <EmptyState
          icon={Globe}
          title={`No site called ${name}`}
          description="It may have been deleted or renamed. The Sites list has every site this host serves."
          action={
            <Button size="sm" variant="outline" asChild>
              <Link href="/proxy/sites">All sites</Link>
            </Button>
          }
        />
      </Page>
    )
  }

  const spec = read.data?.spec
  const engine = nginx ? "nginx" : "Caddy"
  const pools = nginx && upstreams.data ? poolsOfSite(upstreams.data, vhost.path) : []
  const cert = siteCert(vhost, certs.data)
  const summary = hour.data?.status === "available" ? hour.data.summary : undefined
  const verdict = siteVerdict({ vhost, summary, pools, cert })

  const views = siteLogViews(plan)
  const view = asked && views.includes(asked) ? asked : views[0]
  const failedOnly = view === "requests" && query.classes.length === 1 && query.classes[0] === "5xx"
  // The verdict's figure is a question about the rows under it, as the tile
  // it replaced was: pressed, Requests narrows to what it counts and comes
  // into view; pressed again, it lets go.
  const narrowToFailed = () => {
    setQuery({ ...query, classes: failedOnly ? [] : ["5xx"] })
    setAsked("requests")
    logsRef.current?.scrollIntoView({ behavior: "smooth", block: "start" })
  }

  return (
    <Page className="animate-rise">
      <PageContext
        eyebrow={BACK}
        title={name}
        actions={
          verbs.length > 0 && <VerbBar verbs={verbs} menuLabel={`More actions for ${name}`} />
        }
      />

      {/* What the site is, as the facts the Sites card carries, laid out as
          the line the section's other destinations open on, and at its end
          whether it is well. */}
      <HostIdentity
        mark={siteProduct(vhost)}
        fallback={Globe}
        title={name}
        facts={
          <>
            <span>{kindWord(vhost, spec)}</span>
            {read.data && !read.data.managed && (
              <>
                <FactDot />
                <span>written by hand</span>
              </>
            )}
            <FactDot />
            <SiteTLS vhost={vhost} />
            {vhost.modified && (
              <>
                <FactDot />
                <span>edited {relativeTime(vhost.modified)}</span>
              </>
            )}
            <FactDot />
            <ServingStatus vhost={vhost} />
          </>
        }
        aside={
          verdict && <Verdict verdict={verdict} pressed={failedOnly} onPress={narrowToFailed} />
        }
      />

      <SiteRouteMap
        vhost={vhost}
        spec={spec}
        engine={{ product: siteProduct(vhost), name: engine }}
        summary={summary}
        recorded={Boolean(plan.requests)}
        cert={cert}
        pools={pools}
        listeners={ports.data}
      />

      {nginx && !spec && read.error ? (
        // Where an nginx site logs is its file's to say. Unread, the page
        // does not guess: the guess was nginx's shared log, under a notice
        // that the site kept no log of its own.
        <ErrorState error={read.error} onRetry={read.refresh} />
      ) : (
        <>
          {plan.unrecorded && (
            <Notice
              icon={Logs}
              title={
                plan.unrecorded === "off"
                  ? "This site keeps no access log"
                  : "This site has no access log of its own"
              }
            >
              <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
                <span>
                  {plan.unrecorded === "off"
                    ? "Nothing records the requests it serves, so there are none to read here — its errors still are."
                    : "Its requests go to nginx's shared log, syslog or a stream, where a line does not say which site answered it."}{" "}
                  Turn its access log on in the site&rsquo;s form to read its requests here.
                </span>
                {admin && (
                  <Button size="xs" variant="outline" onClick={openForm}>
                    <Pencil className="size-3" />
                    Edit
                  </Button>
                )}
              </div>
            </Notice>
          )}

          {plan.sources.length > 0 && view ? (
            // The pane leads with its strip when a verdict brings it into
            // view, rather than tucking it under the sticky chrome.
            <div ref={logsRef} data-slot="site-logs" className="scroll-mt-4">
              <SiteLogs
                key={plan.sources.map((s) => s.id).join("|")}
                name={name}
                plan={plan}
                engine={engine}
                view={view}
                onViewChange={setAsked}
                query={query}
                onQueryChange={setQuery}
              />
            </div>
          ) : (
            <EmptyState
              icon={Logs}
              title="Nothing to read for this site"
              description={
                status
                  ? "The Caddy ingress that serves this route is not running, so neither its requests nor its errors can be read. Its state is on the Proxy overview."
                  : "The proxy's state could not be read, so neither is which ingress serves this route, and its requests and errors are read through that ingress."
              }
              action={
                !status && (
                  <Button size="sm" variant="outline" onClick={refreshStatus}>
                    Try again
                  </Button>
                )
              }
            />
          )}
        </>
      )}

      {nginx && upstreams.data && (
        <SiteBalancing pools={pools} evidence={upstreams.data.evidence} />
      )}
      {nginx && <SiteControls name={name} admin={admin} />}

      <SiteForm
        open={form.open}
        editing={name}
        session={form.session}
        onOpenChange={(open) => {
          setForm((f) => ({ ...f, open }))
          if (!open) forgetSessionState("proxy.site.form.")
        }}
        onSaved={refresh}
      />
      <ConfigEditor
        open={raw && Boolean(vhost)}
        onOpenChange={(open) => !open && setRaw(false)}
        path={vhost?.path ?? ""}
        kind={vhost?.kind ?? "nginx"}
        title={vhost?.name ?? "Configuration"}
        readOnly={!admin || Boolean(vhost && activeOwner(vhost))}
        siteDisabled={vhost?.kind === "nginx" && Boolean(vhost.enabledPath) && !vhost.enabled}
        onSaved={refresh}
        actions={(busy) =>
          vhost && (
            <SiteFileVerbs
              vhost={vhost}
              admin={admin}
              busy={busy}
              onEdit={() => {
                setRaw(false)
                openForm()
              }}
              verbs={["open", "scan", "edit"]}
            />
          )
        }
      />
    </Page>
  )
}

/**
 * Whether the site is well, at the identity line's end. A verdict that
 * counts failed requests is a press away from them; the others say what is
 * wrong where nothing on the page narrows to it.
 */
function Verdict({
  verdict,
  pressed,
  onPress,
}: {
  verdict: SiteVerdict
  pressed: boolean
  onPress: () => void
}) {
  const body = (
    <span className="flex flex-col items-end gap-0.5 max-sm:items-start">
      <Status tone={verdict.tone} label={verdict.label} />
      {verdict.hint && <span className="text-hint text-muted-foreground">{verdict.hint}</span>}
    </span>
  )
  if (!verdict.narrows) return body
  return (
    <button
      type="button"
      aria-pressed={pressed}
      aria-label={pressed ? "Show every request again" : "Show the requests that failed"}
      onClick={onPress}
      className="rounded-md px-1.5 py-1 focus-ring transition-colors hover:bg-row-hover aria-pressed:bg-accent"
    >
      {body}
    </button>
  )
}

/** What the site does, in the form's words, or where a Caddy site lives. */
function kindWord(vhost: VHost, spec: SiteSpec | undefined) {
  if (vhost.kind === "caddy") {
    if (vhost.path) return "a site in the Caddyfile"
    return vhost.name.startsWith("docker-caddy:") ? "a route on the ingress" : "a deployment route"
  }
  switch (spec?.kind) {
    case "static":
      return "static files"
    case "redirect":
      return spec.permanent ? "permanent redirect" : "redirect"
    default:
      return "reverse proxy"
  }
}

const PLACEHOLDER: VHost = {
  name: "",
  kind: "nginx",
  path: "",
  enabled: false,
  formEditable: false,
  serverNames: [],
  listen: [],
  upstreams: [],
  tls: false,
  modified: "",
  size: 0,
}
