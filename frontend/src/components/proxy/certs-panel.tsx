"use client"

import { useEffect, useMemo, useState } from "react"
import { useSearchParams } from "next/navigation"
import { CheckCircle, CloudUpload, RefreshClockwise, ShieldOff } from "@/components/icons"
import { ApiError, errorMessage, get, post } from "@/lib/api"
import {
  afterReload,
  certbotRunning,
  renewalReading,
  sameNames,
  stillServingTest,
  testCertificateReplaced,
  testRunPassed,
} from "@/lib/certificates"
import { notify } from "@/lib/toast"
import type { Certificate, CertbotState, DNSProvider, Job, ServedCertificate } from "@/lib/types"
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
import { ReloadHookOption, RenewalLogPanel, RenewalRecord } from "@/components/proxy/renewal-health"
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
  // refreshed when a job ends rather than when it starts — however it ended:
  // a renewal run that failed changes the renewal record as much as one
  // that passed.
  const console_ = useJobConsole()
  const ended =
    console_.job && console_.job.status !== "running"
      ? `${console_.job.id}:${console_.job.status}`
      : ""
  const { refresh: refreshCerts } = certs
  const { refresh: refreshCertbot } = certbot
  useEffect(() => {
    if (!ended) return
    refreshCerts()
    refreshCertbot()
  }, [ended, refreshCerts, refreshCertbot])
  const { busy, renew } = useRenew(console_.attach)
  const certbotBusy = certbotRunning(console_.job)
  const [logOpen, setLogOpen] = useState(false)
  const [starting, setStarting] = useState(false)
  // The timer's own service, started now: systemd records the run, so the
  // renewal reading changes with it.
  const runRenewal = async () => {
    setStarting(true)
    try {
      console_.attach(await post<Job>("/certificates/renewal/run"))
    } catch (err) {
      notify.error("The renewal did not start", err)
    } finally {
      setStarting(false)
    }
  }
  const certbotGone =
    certbot.error instanceof ApiError && certbot.error.code === "certbot_unavailable"

  const counts = useMemo(() => {
    const all = certs.data ?? []
    // A test certificate is refused like an expired one, and counted there
    // rather than as expiring: its days say nothing about when it stops working.
    const readable = all.filter((c) => !c.error)
    return {
      all: all.length,
      certbot: all.filter((c) => c.source === "certbot").length,
      imported: all.filter((c) => c.source === "imported").length,
      expiring: readable.filter((c) => c.expiring && !c.expired && !c.staging).length,
      unreadable: all.length - readable.length,
      expired: readable.filter((c) => c.expired && !c.staging).length,
      test: readable.filter((c) => c.staging).length,
    }
  }, [certs.data])
  const refused = counts.unreadable + counts.expired + counts.test

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

  // ?issue= is a one-shot hand-off from the site form. Left in the address,
  // a reload opened the form again after it had been closed or used.
  const setIssueOpen = (open: boolean) => {
    setIssue((s) => ({ ...s, open }))
    if (!open && params.get("issue") !== null) {
      const url = new URL(window.location.href)
      url.searchParams.delete("issue")
      window.history.replaceState(null, "", `${url.pathname}${url.search}${url.hash}`)
    }
  }

  // A test run that passed is the moment to issue the real one, with the
  // same names and nothing to retype.
  const job = console_.job
  const testPassed = testRunPassed(job)

  // A test certificate replaced on disk reaches browsers once nginx reads it
  // again. certonly reloads nothing itself, but a certbot deploy hook may
  // have, and the job on screen may be a day old: the sites that name it are
  // asked what they serve, and the reload is offered only while one still
  // answers with the test certificate.
  const replacedNames = testCertificateReplaced(job)
  const replaced = replacedNames
    ? certs.data?.find((c) => c.source === "certbot" && sameNames(c.domains, replacedNames))
    : undefined
  const askServed = Boolean(admin && hasNginx && replaced && replaced.usedBy.length > 0)
  const served = usePoll<ServedCertificate[]>(
    (signal) => get("/certificates/served", { path: replaced?.path ?? "" }, signal),
    0,
    [replaced?.path, job?.id],
    { enabled: askServed },
  )
  const staleSites = askServed ? stillServingTest(served.data) : []
  const [reloading, setReloading] = useState(false)
  const reload = async (path: string, sites: string[]) => {
    setReloading(true)
    try {
      await post("/proxy/reload", { kind: "nginx" })
    } catch (err) {
      notify.error("nginx did not reload", err)
      setReloading(false)
      return
    }
    try {
      // nginx swaps its workers a moment after the signal; settle asks
      // again until they answer, for a few seconds at most.
      const after = await get<ServedCertificate[]>("/certificates/served", { path, settle: "1" })
      const outcome = afterReload(sites, after)
      if (outcome.ok) notify.success("nginx reloaded", { description: outcome.description })
      else notify.warning("nginx reloaded", { description: outcome.description })
    } catch (err) {
      notify.warning("nginx reloaded", {
        description: `What the sites serve could not be checked: ${errorMessage(err)}`,
      })
    } finally {
      served.refresh()
      setReloading(false)
    }
  }
  // A configured staging directory signs test certificates: nothing issued
  // from here replaces one with a certificate browsers accept.
  const testAuthority = certbot.data?.testAuthority ?? false

  const renewal = renewalReading(certbot.data, certbotGone)

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
          label="Refused"
          value={certs.data ? refused : "—"}
          tone={refused > 0 ? "danger" : "default"}
          hint={
            refused > 0
              ? [
                  counts.expired > 0 && `${counts.expired} expired`,
                  counts.test > 0 &&
                    `${counts.test} test certificate${counts.test === 1 ? "" : "s"}`,
                  counts.unreadable > 0 && `${counts.unreadable} unreadable`,
                ]
                  .filter(Boolean)
                  .join(" · ")
              : "nothing refused"
          }
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
        <Notice tone="success" icon={CheckCircle} title={`The test run for ${testPassed} passed`}>
          <div className="flex flex-wrap items-center gap-2">
            <span>
              {testAuthority
                ? "The authority went through the whole exchange and nothing was saved. What it issues is a test certificate: JD_ACME_DIRECTORY names a staging authority."
                : "The authority went through the whole exchange and nothing was saved, so the real issuance should pass too."}
            </span>
            <Button
              size="xs"
              variant="outline"
              onClick={() => setIssue({ open: true, domains: testPassed, staging: false })}
            >
              {testAuthority ? "Issue the certificate" : "Issue the real certificate"}
            </Button>
          </div>
        </Notice>
      )}

      {replacedNames && replaced && staleSites.length > 0 && (
        <Notice
          tone="warning"
          icon={RefreshClockwise}
          title="nginx is still serving the test certificate"
        >
          <div className="flex flex-wrap items-center gap-2">
            <span>
              The real certificate for {replacedNames.join(", ")} is on disk.{" "}
              {staleSites.join(", ")} {staleSites.length === 1 ? "keeps" : "keep"} serving the test
              one until nginx reloads.
            </span>
            <Button
              size="xs"
              variant="outline"
              pending={reloading}
              onClick={() => reload(replaced.path, staleSites)}
            >
              Reload nginx
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
              <CertificateInventory
                certs={certs.data}
                canScan={admin}
                job={job}
                onReplace={
                  admin && !certbotGone && !testAuthority
                    ? (domains) => setIssue({ open: true, domains, staging: false })
                    : undefined
                }
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

        <div className="space-y-8">
          <Panel plain>
            <PanelHeader
              title="Automatic renewal"
              actions={
                admin &&
                certbot.data &&
                certbot.data.certs.length > 0 && (
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={busy !== "" || certbotBusy}
                    pending={
                      busy === ALL_CERTS ||
                      (certbotBusy &&
                        job?.kind === "certbot.renew" &&
                        job.target === "every certificate due")
                    }
                    onClick={() => renew(ALL_CERTS, false)}
                  >
                    <RefreshClockwise className="size-3.5" />
                    Renew all due
                  </Button>
                )
              }
            />
            <PanelBody flush>
              {/* In the body, not the header: the column is narrow beside the
                  inventory, and a row of runs there pushed the header's
                  button out of it. Here they wrap. */}
              {admin && <RecentJobs kinds={["certbot."]} onOpen={console_.open} className="pt-3" />}
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
                <>
                  <RenewalRecord
                    state={certbot.data}
                    admin={admin}
                    certbotBusy={certbotBusy || starting || busy !== ""}
                    running={certbotBusy && job?.kind === "certbot.renewal"}
                    onRun={runRenewal}
                    onShowLog={() => setLogOpen(true)}
                  />
                  {admin && hasNginx && (
                    <ReloadHookOption state={certbot.data} onChanged={certbot.refresh} />
                  )}
                  <CertbotLineages
                    state={certbot.data}
                    admin={admin}
                    busy={busy}
                    job={console_.job}
                    onRenew={renew}
                    onReplace={
                      testAuthority
                        ? undefined
                        : (domains) => setIssue({ open: true, domains, staging: false })
                    }
                    onRevoke={revoke}
                    onShowLog={() => setLogOpen(true)}
                  />
                </>
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
          {admin && providers.error && (
            <Panel plain>
              <PanelHeader title="DNS challenge providers" />
              <PanelBody flush>
                <ErrorState error={providers.error} onRetry={providers.refresh} />
              </PanelBody>
            </Panel>
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
            onOpenChange={setIssueOpen}
            initialDomains={issue.domains}
            initialStaging={issue.staging}
            hasNginx={hasNginx}
            providers={providers.data ?? []}
            directory={certbot.data?.directory}
            testAuthority={testAuthority}
            certbotBusy={certbotBusy}
            onStarted={(job) => {
              console_.attach(job)
              providers.refresh()
            }}
          />
          <ImportDialog open={importOpen} onOpenChange={setImportOpen} onDone={certs.refresh} />
          <RenewalLogPanel open={logOpen} onOpenChange={setLogOpen} />
        </>
      )}
      {dialog}
    </Page>
  )
}
