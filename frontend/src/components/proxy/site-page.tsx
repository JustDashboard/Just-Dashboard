"use client"

import { useState } from "react"
import Link from "next/link"
import { useParams } from "next/navigation"
import { ArrowLeft, Globe, Logs, Pencil } from "@/components/icons"
import { get } from "@/lib/api"
import { forgetSessionState } from "@/lib/view-state"
import type { SiteSpec, VHost } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Page, PageContext, PageState } from "@/components/page"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { EmptyState, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { VerbBar } from "@/components/verbs"
import { siteProduct } from "@/components/proxy/marks"
import { useProxy } from "@/components/proxy/proxy-context"
import { ServingStatus, SiteTLS } from "@/components/proxy/site-marks"
import { siteLogPlan } from "@/components/proxy/site-log-plan"
import { SiteLogs } from "@/components/proxy/site-logs"
import { SiteForm } from "@/components/proxy/site-form"
import { useSiteVerbs } from "@/components/proxy/site-verbs"
import { ConfigEditor } from "@/components/proxy/vhosts-panel"

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
 * One site, as a place: what it is — the engine serving it, its names, where
 * they go, whose certificate — its verbs beside the way back, and its logs
 * read where it is rather than on the host Logs page it used to send the
 * reader to, which knew it only as a path.
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
  const { status } = useProxy()
  const { confirm, dialog } = useConfirm()
  const [form, setForm] = useState({ open: false, session: 0 })
  const [raw, setRaw] = useState(false)

  const vhosts = usePoll((signal) => get<VHost[]>("/proxy/vhosts", undefined, signal), 30_000)
  const vhost = vhosts.data?.find((v) => v.name === name)
  const nginx = vhost?.kind === "nginx"
  const read = usePoll(
    (signal) => get<SiteRead>(`/proxy/sites/${encodeURIComponent(name)}`, undefined, signal),
    30_000,
    [name],
    { enabled: nginx },
  )
  const refresh = () => {
    vhosts.refresh()
    read.refresh()
  }
  const openForm = () => setForm((f) => ({ open: true, session: f.session + 1 }))

  const verbs = useSiteVerbs({
    vhost: vhost ?? PLACEHOLDER,
    admin,
    onEdit: openForm,
    onRaw: () => setRaw(true),
    onDuplicate: () => {},
    onToggle: () => {},
    onDelete: () => {},
  }).filter((verb) => PAGE_VERBS.includes(verb.key))

  if (!vhosts.data || (nginx && !read.data && !read.error)) {
    return <PageState eyebrow={BACK} title={name} error={vhosts.error} onRetry={refresh} />
  }
  if (!vhost) {
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
  const plan = siteLogPlan(vhost, spec, status?.ingressContainer)
  const engine = nginx ? "nginx" : "Caddy"
  const target = spec
    ? spec.kind === "static"
      ? spec.root
      : spec.kind === "redirect"
        ? spec.redirectTo
        : spec.upstream
    : vhost.upstreams[0]

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
          the line the section's other destinations open on. */}
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
            {vhost.serverNames.length > 0 && (
              <>
                <FactDot />
                <span className="min-w-0 truncate font-mono">{vhost.serverNames.join(", ")}</span>
              </>
            )}
            {target && (
              <>
                <FactDot />
                <span className="min-w-0 truncate font-mono">→ {target}</span>
              </>
            )}
            <FactDot />
            <SiteTLS vhost={vhost} />
            <FactDot />
            <ServingStatus vhost={vhost} />
          </>
        }
      />

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

      {plan.sources.length > 0 ? (
        <SiteLogs
          key={plan.sources.map((s) => s.id).join("|")}
          name={name}
          plan={plan}
          engine={engine}
        />
      ) : (
        <EmptyState
          icon={Logs}
          title="Nothing to read for this site"
          description="The Caddy ingress that serves this route is not running, so neither its requests nor its errors can be read. Its state is on the Proxy overview."
        />
      )}

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
        vhost={raw ? vhost : null}
        admin={admin}
        confirm={confirm}
        onOpenChange={(open) => !open && setRaw(false)}
        onSaved={refresh}
        onEdit={() => {
          setRaw(false)
          openForm()
        }}
        verbs={["open", "scan", "edit"]}
      />
      {dialog}
    </Page>
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
  serverNames: [],
  listen: [],
  upstreams: [],
  tls: false,
  modified: "",
  size: 0,
}
