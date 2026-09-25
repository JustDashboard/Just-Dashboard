"use client"

import { useMemo, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import {
  CheckCircle,
  CloudUpload,
  Copy,
  Globe,
  Inspect,
  RefreshClockwise,
  ShieldCheck,
  ShieldOff,
  Trash,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { ApiError, del, get, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { calendarDate } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { Certificate, CertbotState, DNSProvider, Job } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { JobConsole, RecentJobs, useJobConsole } from "@/components/job-console"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { Row, RowList, ROW_BLEED } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { VerbActions, type Verb } from "@/components/verbs"
import { useProxy } from "@/components/proxy/proxy-context"
import {
  ALL_CERTS,
  CertbotLineages,
  CertbotMissing,
  DnsProvidersPanel,
  IssueDialog,
  RenewalNotice,
  useRenew,
} from "@/components/proxy/certbot-panel"
import { CertLife, ExpiryStatus } from "@/components/proxy/expiry-status"
import { ImportDialog } from "@/components/proxy/import-dialog"
import { certificateProduct } from "@/components/proxy/marks"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

type Watched = { id: number; domain: string; port: number; certificate?: Certificate }

/**
 * Every certificate on the host and everything done to one: four readings,
 * certbot's lineages with their verbs, what is installed and which site uses
 * it, the domains checked with a live handshake — which catches one renewed
 * on disk but never reloaded, the failure that is invisible in a file — and
 * the DNS plugins a wildcard needs.
 */
export function CertificatesPage() {
  const { can } = useAuth()
  const { status } = useProxy()
  const router = useRouter()
  const params = useSearchParams()
  const { confirm, dialog } = useConfirm()
  const admin = can("system.admin")
  const hasNginx = status?.nginx ?? false

  // ?issue=app.example.com opens the issuance form on those names: the site
  // form links here when it names a certificate that does not exist yet.
  const [issue, setIssue] = useState<{ open: boolean; domains?: string; staging: boolean }>(() => ({
    open: Boolean(params.get("issue")),
    domains: params.get("issue") ?? undefined,
    staging: true,
  }))
  const [importOpen, setImportOpen] = useState(false)
  const [domain, setDomain] = useState("")
  // One shape per width for the installed list, chosen once (see `CertList`).
  const wide = useMediaQuery("(min-width: 1024px)")

  const certs = usePoll(
    (signal) => get<Certificate[]>("/certificates/", undefined, signal),
    300_000,
  )
  const watched = usePoll(
    (signal) => get<Watched[]>("/certificates/watched", undefined, signal),
    300_000,
  )
  const certbot = usePoll<CertbotState>(
    (signal) => get("/certificates/certbot", undefined, signal),
    300_000,
  )
  const providers = usePoll<DNSProvider[]>(
    (signal) => get("/certificates/dns-providers", undefined, signal),
    0,
    [],
    { enabled: admin },
  )
  // The list is only right once certbot has finished writing, so it is
  // refreshed when a job ends rather than when it starts.
  const console_ = useJobConsole({
    onSuccess: () => {
      certs.refresh()
      certbot.refresh()
    },
  })
  const { busy, renew } = useRenew(console_.attach)
  const certbotGone =
    certbot.error instanceof ApiError && certbot.error.code === "certbot_unavailable"

  const counts = useMemo(() => {
    const all = certs.data ?? []
    return {
      all: all.length,
      certbot: all.filter((c) => c.source === "certbot").length,
      imported: all.filter((c) => c.source === "imported").length,
      expiring: all.filter((c) => c.expiring && !c.expired && !c.error).length,
      bad: all.filter((c) => c.expired || Boolean(c.error)).length,
    }
  }, [certs.data])

  const revoke = (name: string) =>
    confirm({
      title: `Revoke ${name}`,
      phrase: `revoke ${name}`,
      confirmLabel: "Revoke and delete",
      description: (
        <p className="text-destructive">
          The authority publishes that this certificate is no longer to be trusted and the files are
          deleted. There is no undo, and every client holding it starts refusing the site.
        </p>
      ),
      action: async (c) => {
        const job = await post<Job>("/certificates/revoke", { name }, { confirm: c })
        console_.attach(job)
      },
    })

  const addDomain = async () => {
    // "host:port" for the services that answer TLS off 443: a mail server
    // on 993, a database on 5432 with TLS required.
    const [host, port] = domain.trim().split(":")
    try {
      await post("/certificates/watched", { domain: host, port: Number(port) || 443 })
      setDomain("")
      watched.refresh()
    } catch (err) {
      notify.error("Could not watch domain", err)
    }
  }

  // A staging run that passed is the moment to issue the real one, with the
  // same names and nothing to retype.
  const job = console_.job
  const testPassed =
    job &&
    job.kind === "certbot.issue" &&
    job.status === "succeeded" &&
    job.title.startsWith("Test issuance for")
      ? job.target
      : undefined

  const renewal = certbotGone
    ? { value: "No certbot", hint: "install it to issue and renew", tone: "default" as const }
    : !certbot.data
      ? { value: "—", hint: undefined, tone: "default" as const }
      : certbot.data.autoRenew
        ? {
            value: "Scheduled",
            hint: `via ${certbot.data.renewSource}`,
            tone: "success" as const,
          }
        : {
            value: "Not scheduled",
            hint: certbot.data.renewUnit
              ? `${certbot.data.renewUnit} is off`
              : "no timer or cron entry found",
            tone: certbot.data.certs.length > 0 ? ("danger" as const) : ("default" as const),
          }

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Proxy" title="Certificates" />

      <StatGrid columns={4}>
        <StatTile
          label="Certificates"
          value={certs.data ? counts.all : "—"}
          hint={
            certs.data
              ? [
                  counts.certbot > 0 && `${counts.certbot} certbot`,
                  counts.imported > 0 && `${counts.imported} imported`,
                  counts.all - counts.certbot - counts.imported > 0 &&
                    `${counts.all - counts.certbot - counts.imported} referenced by a site`,
                ]
                  .filter(Boolean)
                  .join(" · ") || "none on this host"
              : undefined
          }
        />
        <StatTile
          label="Expiring soon"
          value={certs.data ? counts.expiring : "—"}
          tone={counts.expiring > 0 ? "warning" : "default"}
          hint={counts.expiring > 0 ? "inside the 30-day renewal window" : "none within 30 days"}
        />
        <StatTile
          label="Expired or unreadable"
          value={certs.data ? counts.bad : "—"}
          tone={counts.bad > 0 ? "danger" : "default"}
          hint={counts.bad > 0 ? "browsers refuse these now" : "nothing refused"}
        />
        <StatTile label="Renewal" value={renewal.value} hint={renewal.hint} tone={renewal.tone} />
      </StatGrid>

      <JobConsole
        job={console_.job}
        lines={console_.lines}
        onDismiss={console_.dismiss}
        onCancel={console_.cancel}
      />

      {testPassed && admin && (
        <Notice
          tone="success"
          icon={CheckCircle}
          title={`The test issuance for ${testPassed} passed`}
        >
          <div className="flex flex-wrap items-center gap-2">
            <span>
              Let&rsquo;s Encrypt&rsquo;s staging authority signed it, so the real one will work.
            </span>
            <Button
              size="xs"
              variant="outline"
              onClick={() => setIssue({ open: true, domains: testPassed, staging: false })}
            >
              Issue the real certificate
            </Button>
          </div>
        </Notice>
      )}

      {certbot.data && (
        <RenewalNotice state={certbot.data} admin={admin} onChanged={certbot.refresh} />
      )}

      {/* Four plain sections, each a title and a hairline (§15 pass 1), in
          the order a certificate's life runs: issued here, installed on the
          host, checked from outside, and the DNS plugins an issuance can
          need. The frames around them opened the page with a stack of four
          boxes under four figures. */}
      <Panel plain>
        <PanelHeader
          title="Issued by certbot"
          actions={
            admin && (
              <>
                <RecentJobs kinds={["certbot."]} onOpen={console_.open} />
                {certbot.data && certbot.data.certs.length > 0 && (
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={busy !== ""}
                    pending={busy === ALL_CERTS}
                    onClick={() => renew(ALL_CERTS, false)}
                  >
                    <RefreshClockwise className="size-3.5" />
                    Renew all due
                  </Button>
                )}
                {!certbotGone && (
                  <Button
                    size="sm"
                    onClick={() => setIssue({ open: true, domains: undefined, staging: true })}
                  >
                    Issue certificate
                  </Button>
                )}
              </>
            )
          }
        />
        <PanelBody flush>
          {certbotGone ? (
            <CertbotMissing />
          ) : certbot.loading ? (
            <LoadingRows rows={3} />
          ) : certbot.error ? (
            <ErrorState error={certbot.error} />
          ) : certbot.data ? (
            <CertbotLineages
              state={certbot.data}
              admin={admin}
              busy={busy}
              onRenew={renew}
              onRevoke={revoke}
            />
          ) : null}
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader
          title="Installed on this host"
          actions={
            admin && (
              <Button size="sm" variant="outline" onClick={() => setImportOpen(true)}>
                <CloudUpload className="size-3.5" />
                Import
              </Button>
            )
          }
        />
        <PanelBody flush>
          {certs.loading ? (
            <LoadingRows rows={3} />
          ) : certs.error ? (
            <ErrorState error={certs.error} />
          ) : certs.data && certs.data.length > 0 ? (
            <CertList
              certs={certs.data}
              wide={wide}
              onScan={(d) => router.push(`/proxy/tls?domain=${encodeURIComponent(d)}`)}
              canScan={admin}
            />
          ) : (
            <EmptyState
              icon={ShieldOff}
              title="No certificates found"
              description="certbot's live directory, imported certificates and every certificate a site names are all listed here once one exists."
              className="mt-2"
            />
          )}
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader
          title="Checked from outside"
          actions={
            <>
              {admin && (
                <span className="flex items-center gap-2">
                  <Input
                    value={domain}
                    onChange={(e) => setDomain(e.target.value)}
                    onKeyDown={(e) => e.key === "Enter" && domain.trim() && addDomain()}
                    placeholder="example.com, or mail.example.com:993"
                    aria-label="Domain to watch"
                    className="h-8 w-56 text-body sm:w-72"
                  />
                  <Button size="sm" onClick={addDomain} disabled={!domain.trim()}>
                    Watch
                  </Button>
                </span>
              )}
              {watched.data && watched.data.length > 0 && (
                <Button variant="outline" size="sm" onClick={() => watched.refresh()}>
                  <RefreshClockwise className="size-3.5" />
                  Re-check now
                </Button>
              )}
            </>
          }
        />
        <PanelBody flush>
          {watched.loading ? (
            <LoadingRows rows={2} />
          ) : watched.error ? (
            <ErrorState error={watched.error} />
          ) : !watched.data?.length ? (
            <p className="py-2 text-body text-muted-foreground">
              Nothing watched yet. A watched domain is checked with a real handshake every five
              minutes, which is what catches a certificate renewed on disk and never reloaded.
            </p>
          ) : (
            <RowList className="animate-rise">
              {watched.data.map((row) => (
                <Row
                  key={row.id}
                  leading={
                    <ProductLogo
                      id={certificateProduct(row.certificate)}
                      size="sm"
                      fallback={Globe}
                    />
                  }
                  title={
                    <>
                      {row.domain}
                      {row.port !== 443 && (
                        <span className="numeric ml-1.5 font-mono text-hint text-muted-foreground">
                          :{row.port}
                        </span>
                      )}
                    </>
                  }
                  subtitle={
                    row.certificate
                      ? [
                          row.certificate.issuer,
                          row.certificate.notAfter &&
                            `until ${calendarDate(row.certificate.notAfter)}`,
                        ]
                          .filter(Boolean)
                          .join(" · ")
                      : "not checked yet"
                  }
                  trailing={
                    <>
                      <span className="flex flex-col items-end gap-1">
                        <ExpiryStatus cert={row.certificate} />
                        {row.certificate && !row.certificate.error && (
                          <CertLife cert={row.certificate} />
                        )}
                      </span>
                      {admin && (
                        <VerbActions
                          dim
                          menuLabel={`More actions for ${row.domain}`}
                          verbs={[
                            {
                              key: "scan",
                              label: "TLS report",
                              detail: "Grade what a visitor gets: protocols, chain and headers.",
                              icon: Inspect,
                              inline: true,
                              run: () =>
                                router.push(`/proxy/tls?domain=${encodeURIComponent(row.domain)}`),
                            },
                            {
                              key: "remove",
                              label: "Stop watching",
                              detail: "Drop it from the list. Nothing on the host changes.",
                              icon: Trash,
                              danger: true,
                              run: async () => {
                                await del(`/certificates/watched/${row.id}`)
                                watched.refresh()
                              },
                            },
                          ]}
                        />
                      )}
                    </>
                  }
                  className="py-2.5"
                />
              ))}
            </RowList>
          )}
        </PanelBody>
      </Panel>

      {admin && providers.data && (
        <DnsProvidersPanel providers={providers.data} admin={admin} onChanged={providers.refresh} />
      )}

      {admin && (
        <>
          <IssueDialog
            open={issue.open}
            onOpenChange={(open) => setIssue((s) => ({ ...s, open }))}
            initialDomains={issue.domains}
            initialStaging={issue.staging}
            hasNginx={hasNginx}
            providers={providers.data ?? []}
            onStarted={(job) => {
              console_.attach(job)
              providers.refresh()
            }}
          />
          <ImportDialog open={importOpen} onOpenChange={setImportOpen} onDone={certs.refresh} />
        </>
      )}
      {dialog}
    </Page>
  )
}

/**
 * Every certificate on the host as a row drawn as who signed it — Let's
 * Encrypt as itself, anything else by its glyph — with the file it is, the
 * names it covers and the sites that use it, and at the right how long it
 * has: the verdict over a meter of its term. A reading, not a choice: a row
 * opens nothing, and its two verbs are the path to copy and the report to run.
 *
 * On a wide screen the sites and the expiry date stand in fixed measures
 * beside the name so a column of them reads down; on a phone they go under
 * it, and nothing is dropped.
 */
function CertList({
  certs,
  wide,
  canScan,
  onScan,
}: {
  certs: Certificate[]
  wide: boolean
  canScan: boolean
  onScan: (domain: string) => void
}) {
  const verbsFor = (cert: Certificate): Verb[] => {
    const domain = cert.domains.find((d) => !d.startsWith("*"))
    const verbs: Verb[] = [
      {
        key: "copy",
        label: "Copy path",
        detail: "The certificate's path on disk, for a site's TLS field.",
        icon: Copy,
        inline: true,
        run: () => void copyText(cert.path, "Path copied"),
      },
    ]
    if (domain && canScan) {
      verbs.push({
        key: "scan",
        label: "TLS report",
        detail: `Grade what a visitor to ${domain} actually gets.`,
        icon: Inspect,
        run: () => onScan(domain),
      })
    }
    return verbs
  }
  // Worst first: an expired or unreadable certificate above one running out,
  // above the rest by how soon they do.
  const ordered = [...certs].sort((a, b) => {
    const rank = (c: Certificate) => (c.error ? 0 : c.expired ? 1 : c.expiring ? 2 : 3)
    return rank(a) - rank(b) || a.daysLeft - b.daysLeft
  })
  return (
    <ul data-slot="cert-list" className="animate-rise divide-y divide-hairline">
      {ordered.map((cert) => {
        const usedBy = cert.usedBy.length > 0 ? cert.usedBy.join(", ") : "no site"
        const expires = cert.notAfter && !cert.error ? calendarDate(cert.notAfter) : "—"
        return (
          <li
            key={cert.path || cert.name}
            data-slot="row"
            className={cn(
              "group flex min-w-0 items-start gap-3 py-3 transition-colors hover:bg-row-hover",
              ROW_BLEED,
            )}
          >
            <ProductLogo id={certificateProduct(cert)} size="sm" fallback={ShieldCheck} />
            <div className="min-w-0 flex-1 space-y-0.5">
              <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
                <span className="truncate text-body font-medium">{cert.name}</span>
                <Tag>{cert.source.startsWith("nginx:") ? "site" : cert.source}</Tag>
                {cert.selfSigned && <Tag tone="warning">self-signed</Tag>}
              </div>
              <p className="truncate text-hint text-muted-foreground">
                {cert.domains.join(", ") || "no names"}
              </p>
              <p className="truncate font-mono text-hint text-muted-foreground">{cert.path}</p>
              {!wide && (
                <p className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 pt-1 text-hint text-muted-foreground">
                  <span className="truncate">{usedBy}</span>
                  <span className="whitespace-nowrap">expires {expires}</span>
                </p>
              )}
            </div>
            {wide && (
              <>
                <span
                  className="w-40 truncate pt-0.5 text-hint text-muted-foreground"
                  title={usedBy}
                >
                  {usedBy}
                </span>
                <span className="numeric w-24 pt-0.5 text-hint whitespace-nowrap text-muted-foreground">
                  {expires}
                </span>
              </>
            )}
            <span className="flex w-24 shrink-0 flex-col items-end gap-1 pt-0.5">
              <ExpiryStatus cert={cert} />
              {!cert.error && <CertLife cert={cert} />}
            </span>
            <VerbActions dim verbs={verbsFor(cert)} menuLabel={`More actions for ${cert.name}`} />
          </li>
        )
      })}
    </ul>
  )
}
