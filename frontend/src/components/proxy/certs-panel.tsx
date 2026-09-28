"use client"

import { useMemo, useState } from "react"
import { useSearchParams } from "next/navigation"
import { CheckCircle, CloudUpload, RefreshClockwise, ShieldOff } from "@/components/icons"
import { ApiError, get, post } from "@/lib/api"
import type { Certificate, CertbotState, DNSProvider, Job } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { JobConsole, RecentJobs, useJobConsole } from "@/components/job-console"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
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
import { CertificateInventory } from "@/components/proxy/certificate-inventory"
import { ImportDialog } from "@/components/proxy/import-dialog"
import { RenewalLog } from "@/components/proxy/renewal-log"
import { WatchedDomains } from "@/components/proxy/watched-domains"
import { Button } from "@/components/ui/button"

/**
 * Inventory and lifecycle controls sit beside each other: the certificate you
 * inspect stays separate from the certbot lineage you renew. Every renewal
 * certbot ran follows them across the page's width, which a log needs. Watched
 * domains stay distinct because a live handshake can disagree with the file on
 * disk.
 */
export function CertificatesPage() {
  const { can } = useAuth()
  const { status } = useProxy()
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

  const certs = usePoll(
    (signal) => get<Certificate[]>("/certificates/", undefined, signal),
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
            value: "Off",
            hint: certbot.data.renewUnit
              ? `${certbot.data.renewUnit} is off`
              : "no timer or cron entry found",
            tone: certbot.data.certs.length > 0 ? ("danger" as const) : ("default" as const),
          }

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Proxy" title="Certificates" />

      <StatGrid columns={4} dense>
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

      <div className="grid items-start gap-8 xl:grid-cols-[minmax(0,1fr)_22rem] [&>*]:min-w-0">
        <Panel plain>
          <PanelHeader
            title="Installed on this host"
            actions={
              admin && (
                <>
                  <Button size="sm" variant="outline" onClick={() => setImportOpen(true)}>
                    <CloudUpload className="size-3.5" />
                    Import
                  </Button>
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
            {certs.loading ? (
              <LoadingRows rows={3} />
            ) : certs.error ? (
              <ErrorState error={certs.error} />
            ) : certs.data && certs.data.length > 0 ? (
              <CertificateInventory certs={certs.data} canScan={admin} />
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

        <div className="space-y-8">
          <Panel plain>
            <PanelHeader
              title="Automatic renewal"
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
                  </>
                )
              }
            />
            <PanelBody flush>
              {certbot.data && (
                <RenewalNotice state={certbot.data} admin={admin} onChanged={certbot.refresh} />
              )}
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

          {admin && providers.data && (
            <DnsProvidersPanel
              providers={providers.data}
              admin={admin}
              onChanged={providers.refresh}
            />
          )}
        </div>
      </div>

      {/* Every renewal certbot ran, not only the ones started from this
          page: the timer's runs are the ones nobody was watching. */}
      {!certbotGone && <RenewalLog certbot={certbot.data} />}

      <WatchedDomains admin={admin} />

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
