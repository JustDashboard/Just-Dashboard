"use client"

import { useMemo, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import {
  CheckCircle,
  CloudUpload,
  Copy,
  Inspect,
  RefreshClockwise,
  ShieldOff,
  Trash,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { ApiError, del, get, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { calendarDate } from "@/lib/format"
import type { Certificate, CertbotState, DNSProvider, Job } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { JobConsole, RecentJobs, useJobConsole } from "@/components/job-console"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
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
import { ExpiryStatus } from "@/components/proxy/expiry-status"
import { ImportDialog } from "@/components/proxy/import-dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

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

      <Panel>
        <PanelHeader
          title="certbot"
          actions={
            admin && (
              <>
                <RecentJobs kinds={["certbot."]} onOpen={console_.open} />
                {!certbotGone && (
                  <Button
                    size="sm"
                    onClick={() => setIssue({ open: true, domains: undefined, staging: true })}
                  >
                    Issue certificate
                  </Button>
                )}
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
            <div className="animate-rise">
              <CertbotLineages
                state={certbot.data}
                admin={admin}
                busy={busy}
                onRenew={renew}
                onRevoke={revoke}
              />
            </div>
          ) : null}
        </PanelBody>
      </Panel>

      <Panel>
        <PanelHeader
          title="Installed certificates"
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
            <div className="min-w-0 animate-rise group-data-[plain]/panel:-mx-4">
              <CertTable
                certs={certs.data}
                onScan={(d) => router.push(`/proxy/tls?domain=${encodeURIComponent(d)}`)}
                canScan={admin}
              />
            </div>
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

      <Panel>
        <PanelHeader
          title="Watched domains"
          actions={
            watched.data &&
            watched.data.length > 0 && (
              <Button variant="outline" size="sm" onClick={() => watched.refresh()}>
                <RefreshClockwise className="size-3.5" />
                Re-check now
              </Button>
            )
          }
        />
        {admin && (
          <PanelToolbar>
            <Input
              value={domain}
              onChange={(e) => setDomain(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && domain.trim() && addDomain()}
              placeholder="example.com, or mail.example.com:993"
              aria-label="Domain to watch"
              className="h-8 w-full text-body sm:w-80"
            />
            <Button size="sm" onClick={addDomain} disabled={!domain.trim()}>
              Watch
            </Button>
          </PanelToolbar>
        )}
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
            <div className="min-w-0 animate-rise group-data-[plain]/panel:-mx-4">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-full">Domain</TableHead>
                    <TableHead>Issuer</TableHead>
                    <TableHead>Expires</TableHead>
                    <TableHead>Live check</TableHead>
                    <TableHead className="w-px" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {watched.data.map((row) => (
                    <TableRow key={row.id} className="group">
                      <TableCell>
                        <span className="text-body font-medium">{row.domain}</span>
                        {row.port !== 443 && (
                          <span className="numeric ml-1.5 font-mono text-hint text-muted-foreground">
                            :{row.port}
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {row.certificate?.issuer ?? "—"}
                      </TableCell>
                      <TableCell>
                        {row.certificate?.notAfter ? calendarDate(row.certificate.notAfter) : "—"}
                      </TableCell>
                      <TableCell>
                        <ExpiryStatus cert={row.certificate} />
                      </TableCell>
                      <TableCell>
                        {admin && (
                          <VerbActions
                            dim
                            verbs={[
                              {
                                key: "scan",
                                label: "TLS report",
                                icon: Inspect,
                                inline: true,
                                run: () =>
                                  router.push(
                                    `/proxy/tls?domain=${encodeURIComponent(row.domain)}`,
                                  ),
                              },
                              {
                                key: "remove",
                                label: "Stop watching",
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
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
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

function CertTable({
  certs,
  canScan,
  onScan,
}: {
  certs: Certificate[]
  canScan: boolean
  onScan: (domain: string) => void
}) {
  const verbsFor = (cert: Certificate): Verb[] => {
    const domain = cert.domains.find((d) => !d.startsWith("*"))
    const verbs: Verb[] = [
      {
        key: "copy",
        label: "Copy path",
        icon: Copy,
        inline: true,
        run: () => void copyText(cert.path, "Path copied"),
      },
    ]
    if (domain && canScan) {
      verbs.push({
        key: "scan",
        label: "TLS report",
        icon: Inspect,
        run: () => onScan(domain),
      })
    }
    return verbs
  }
  return (
    <Table containerClassName="max-h-[28rem]">
      <TableHeader className={stickyTableHeader}>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead className="w-full">Domains</TableHead>
          <TableHead>Used by</TableHead>
          <TableHead>Issuer</TableHead>
          <TableHead>Expires</TableHead>
          <TableHead>Status</TableHead>
          <TableHead className="w-px" />
        </TableRow>
      </TableHeader>
      <TableBody>
        {certs.map((cert) => (
          <TableRow key={cert.path || cert.name} className="group">
            <TableCell>
              <div className="max-w-[16rem] min-w-0">
                <div className="flex min-w-0 items-baseline gap-2">
                  <span className="truncate text-body font-medium">{cert.name}</span>
                  <Tag>{cert.source.startsWith("nginx:") ? "site" : cert.source}</Tag>
                  {cert.selfSigned && <Tag tone="warning">self-signed</Tag>}
                </div>
                <p className="truncate font-mono text-hint text-muted-foreground">{cert.path}</p>
              </div>
            </TableCell>
            <TableCell className="max-w-xs truncate">
              {cert.domains.join(", ") || <span className="text-muted-foreground">—</span>}
            </TableCell>
            <TableCell className="max-w-[12rem] truncate text-muted-foreground">
              {cert.usedBy.length > 0 ? cert.usedBy.join(", ") : "no site"}
            </TableCell>
            <TableCell className="max-w-[12rem] truncate text-muted-foreground">
              {cert.issuer || "—"}
            </TableCell>
            <TableCell>
              {cert.notAfter && !cert.error ? calendarDate(cert.notAfter) : "—"}
            </TableCell>
            <TableCell>
              <ExpiryStatus cert={cert} />
            </TableCell>
            <TableCell>
              <VerbActions dim verbs={verbsFor(cert)} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
