"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import {
  ArrowRight,
  CloudUpload,
  FileText,
  RefreshClockwise,
  ShieldCheck,
  ShieldOff,
} from "@/components/icons"
import { get, post } from "@/lib/api"
import { renewalReading } from "@/lib/certificates"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { Certificate, CertificateCoverage, Job } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { RecentJobs } from "@/components/job-console"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogos } from "@/components/product-logo"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { useProxy } from "@/components/proxy/proxy-context"
import {
  ALL_CERTS,
  AcmeAccountsPanel,
  CertbotLineages,
  CertbotMissing,
  CertbotRuntimeLine,
  DnsProvidersPanel,
  RenewalNotice,
} from "@/components/proxy/certbot-panel"
import { CertTransparency } from "@/components/proxy/cert-transparency"
import { CaddyEvidencePrune } from "@/components/proxy/caddy-evidence"
import { ExpiredCleanup } from "@/components/proxy/certificate-cleanup"
import { CertificateCoveragePanel } from "@/components/proxy/certificate-coverage"
import {
  CertificateInventory,
  useCertificateFilters,
} from "@/components/proxy/certificate-inventory"
import {
  certificatesHref,
  pageTab,
  sheetRedirect,
  type PageTab,
} from "@/components/proxy/certificate-paths"
import { CertificateRun, useCertificateWork } from "@/components/proxy/certificate-work"
import { RenewalLog } from "@/components/proxy/renewal-log"
import { LocalCAPanel } from "@/components/proxy/local-ca-panel"
import { certificateProduct } from "@/components/proxy/marks"
import { EmptyMark, OverviewEmpty } from "@/components/proxy/overview-empty"
import { IssueCommand } from "@/components/proxy/private-cert-dialog"
import { ReloadHookOption, RenewalRecord, RenewalRuns } from "@/components/proxy/renewal-health"
import { SigningRequests } from "@/components/proxy/signing-requests"
import { WatchedDomains } from "@/components/proxy/watched-domains"

/**
 * The width of a view that manages rather than lists: these were drawn for a
 * 22rem side column, and across a laptop's whole width a lineage's meter was
 * a thousand pixels of bar.
 */
const COLUMN = "max-w-3xl"

/** The section the Renewal view's "Show log" brings into view. */
const RUNS_ID = "renewal-runs"

/**
 * The certificates on this host, and what issues and renews them.
 *
 * It opened on four figures — how many, how many expiring, how many refused,
 * whether renewal is on — over a two-column page that did seven jobs on one
 * scroll: the certificates, signing requests, renewal, DNS providers, ACME
 * accounts and the local CA down a narrow side column, then the renewal log,
 * coverage, watched domains and transparency logs under both. It moved as each
 * of its six reads landed.
 *
 * Now it opens on what it is about, the line every proxy page opens on: the
 * authorities that signed what is here, certbot, the count, what is expiring
 * or refused and whether anything renews them, with Issue certificate at the
 * far end. Under it the jobs are views in the address (`?tab=`): the
 * certificates themselves first, then what issues and renews them, then the
 * names watched elsewhere. A certificate opens on a page of its own
 * (`/proxy/certificates/<name>`), where its sheet's contents went; the sheet's
 * old address, `?cert=<file>`, lands there.
 *
 * Every view holds its shape while it loads: the line, the strip and the
 * list's toolbar are drawn at once, and rows of the list's own height stand
 * where the certificates will.
 */
export function CertificatesPage() {
  const router = useRouter()
  const params = useSearchParams()
  const work = useCertificateWork()
  const { status } = useProxy()
  const { admin, hasNginx, certs, certbot, certbotGone } = work

  // The sheet's address, from the overview, the TLS report, the server map:
  // the certificate is a page now.
  const sheet = params.get("cert")
  const redirect = sheetRedirect(certs.data, sheet)
  useEffect(() => {
    if (redirect) router.replace(redirect)
  }, [redirect, router])

  const asked = params.get("tab")
  const [chosen, setChosen] = useState<string | null>(asked)
  // Back and Forward, and a link to another view, change the address under
  // the page; the view follows it.
  const [seen, setSeen] = useState(asked)
  if (asked !== seen) {
    setSeen(asked)
    setChosen(asked)
  }
  const tab = pageTab(chosen)
  const setTab = useCallback((next: string) => {
    setChosen(next)
    // Replaced rather than pushed: a view is not a place Back steps through.
    const url = new URL(window.location.href)
    if (next === "certificates") url.searchParams.delete("tab")
    else url.searchParams.set("tab", next)
    window.history.replaceState(null, "", `${url.pathname}${url.search}`)
  }, [])

  const coverage = usePoll<CertificateCoverage>(
    (signal) => get("/certificates/coverage", undefined, signal),
    300_000,
    [],
    { enabled: hasNginx },
  )
  const { refresh: refreshCoverage } = coverage
  useEffect(() => {
    if (work.ended && hasNginx) refreshCoverage()
  }, [work.ended, hasNginx, refreshCoverage])

  const filters = useCertificateFilters()

  // The views an account has are known from the account alone, so the strip
  // is the same while the reads are out as after.
  const views: { key: PageTab; label: string; count?: number }[] = [
    { key: "certificates", label: "Certificates", count: certs.data?.length },
    { key: "renewal", label: "Renewal" },
    ...(admin ? [{ key: "dns" as const, label: "DNS challenge" }] : []),
    ...(admin ? [{ key: "accounts" as const, label: "ACME accounts" }] : []),
    { key: "authority", label: "Local CA" },
    { key: "requests", label: "Signing requests", count: work.requests.data?.length },
    { key: "watched", label: "Watched domains" },
  ]
  const shown = views.some((view) => view.key === tab) ? tab : "certificates"
  const showRuns = () => {
    setTab("renewal")
    requestAnimationFrame(() =>
      document.getElementById(RUNS_ID)?.scrollIntoView({ behavior: "smooth", block: "start" }),
    )
  }

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Proxy" title="Certificates" />
      <CertificatesIdentity />
      <CertificateRun />

      <Tabs value={shown} onValueChange={setTab} className="gap-0">
        <TabsPrimitive.List
          aria-label="Views of the certificates"
          className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
        >
          {views.map((view) => (
            <TabsPrimitive.Trigger
              key={view.key}
              value={view.key}
              className={tabClasses(shown === view.key, "h-10")}
            >
              {view.label}
              {view.count !== undefined && view.count > 0 && <ChipCount>{view.count}</ChipCount>}
            </TabsPrimitive.Trigger>
          ))}
        </TabsPrimitive.List>

        <TabsContent value="certificates" className="space-y-8 pt-6">
          <Panel plain aria-label="Installed certificates">
            {certs.loading && !certs.data ? (
              <InventoryLoading />
            ) : certs.error && !certs.data ? (
              <ErrorState error={certs.error} onRetry={certs.refresh} />
            ) : certs.data && certs.data.length > 0 ? (
              <CertificateInventory
                certs={certs.data}
                filters={filters}
                verbsFor={work.verbsFor}
                replacing={work.replacing}
              />
            ) : (
              <EmptyState
                icon={ShieldOff}
                title="No certificates on this server"
                description="Issue one, import a pair, or point a site at one, and it is listed here."
                action={
                  admin && (
                    <IssueCommand
                      onLetsEncrypt={work.issue ? () => work.issue?.() : undefined}
                      onPrivate={(kind) => work.issuePrivate?.(kind)}
                      onRequest={() => work.requestSigning?.()}
                    />
                  )
                }
              />
            )}
            {admin && certs.data && (
              <ExpiredCleanup
                certs={certs.data}
                certbotBusy={work.certbotBusy}
                onJob={work.attach}
                onDeleted={certs.refresh}
              />
            )}
            {admin && (
              <div className="pt-4">
                <CaddyEvidencePrune onPruned={certs.refresh} />
              </div>
            )}
          </Panel>

          {hasNginx ? (
            <CertificateCoveragePanel
              coverage={coverage}
              onIssue={
                work.issue
                  ? (names) => work.issue?.({ domains: names.join(" "), staging: true })
                  : undefined
              }
            />
          ) : (
            status && (
              <Panel plain aria-label="Coverage">
                <PanelHeader title="Coverage" />
                <PanelBody flush>
                  <OverviewEmpty
                    mark={<EmptyMark product="nginx" corner={ShieldOff} />}
                    title="Coverage is read from nginx"
                    action={
                      <Button asChild size="sm" variant="outline">
                        <Link href="/proxy">
                          Set up nginx <ArrowRight />
                        </Link>
                      </Button>
                    }
                  >
                    Which site names a certificate covers, and which it misses, comes from the sites
                    nginx serves. This server has no nginx.
                  </OverviewEmpty>
                </PanelBody>
              </Panel>
            )
          )}
        </TabsContent>

        <TabsContent value="renewal" className="space-y-8 pt-6">
          {shown === "renewal" && <RenewalView onShowLog={showRuns} />}
        </TabsContent>

        <TabsContent value="dns" className={`${COLUMN} pt-6`}>
          {shown === "dns" && admin && (
            <>
              {work.providers.loading && !work.providers.data ? (
                <LoadingRows rows={4} />
              ) : work.providers.error ? (
                <Panel plain>
                  <PanelHeader title="DNS challenge providers" />
                  <PanelBody flush>
                    <ErrorState error={work.providers.error} onRetry={work.providers.refresh} />
                  </PanelBody>
                </Panel>
              ) : work.providers.data ? (
                <DnsProvidersPanel
                  providers={work.providers.data}
                  admin={admin}
                  certs={certbot.data?.certs ?? []}
                  installs={certbot.data?.installs}
                  certbotBusy={work.certbotBusy}
                  onJob={work.attach}
                  onChanged={work.providers.refresh}
                />
              ) : null}
            </>
          )}
        </TabsContent>

        <TabsContent value="accounts" className={`${COLUMN} pt-6`}>
          {shown === "accounts" &&
            admin &&
            (certbotGone ? (
              <CertbotMissing onInstall={work.attach} />
            ) : certbot.loading && !certbot.data ? (
              <LoadingRows rows={3} />
            ) : certbot.error && !certbot.data ? (
              <ErrorState error={certbot.error} onRetry={certbot.refresh} />
            ) : certbot.data?.available ? (
              <AcmeAccountsPanel certbotBusy={work.certbotBusy} onJob={work.attach} />
            ) : (
              <CertbotMissing onInstall={work.attach} />
            ))}
        </TabsContent>

        <TabsContent value="authority" className={`${COLUMN} pt-6`}>
          {shown === "authority" && <AuthorityView />}
        </TabsContent>

        <TabsContent value="requests" className={`${COLUMN} pt-6`}>
          {shown === "requests" && <RequestsView />}
        </TabsContent>

        <TabsContent value="watched" className={`${COLUMN} space-y-8 pt-6`}>
          {shown === "watched" && (
            <>
              <WatchedDomains admin={admin} added={work.watchedAdded} />
              {admin && <CertTransparency />}
            </>
          )}
        </TabsContent>
      </Tabs>
    </Page>
  )
}

/**
 * What the page is about, as one line: the authorities that signed the
 * certificates here, certbot, how many there are, what is expiring or refused
 * and whether anything renews them, and at the far end the verdict and the
 * commands. It holds its height while the reads are out, so nothing under it
 * moves when they land.
 */
function CertificatesIdentity() {
  const work = useCertificateWork()
  const { admin, certs, certbot, certbotGone } = work
  const list = certs.data
  const counts = useMemo(() => countCertificates(list ?? []), [list])
  const renewal = renewalReading(certbot.data, certbotGone)
  const marks = useMemo(() => {
    const seen = new Set<string>()
    for (const cert of list ?? []) {
      const product = certificateProduct(cert)
      if (product) seen.add(product)
    }
    return [...seen].slice(0, 3)
  }, [list])

  const more: Verb[] = [
    ...(work.importPair
      ? [{ key: "import", label: "Import", icon: CloudUpload, run: () => work.importPair?.() }]
      : []),
    { key: "decode", label: "Paste a certificate", icon: FileText, run: work.decode },
  ]

  const verdict = !list ? undefined : counts.refused > 0 ? (
    <Status verdict="critical" label={`${plural(counts.refused, "certificate")} refused`} />
  ) : counts.expiring > 0 ? (
    <Status verdict="warning" label={`${counts.expiring} expiring`} />
  ) : counts.all > 0 ? (
    <Status verdict="ok" label="all valid" />
  ) : undefined

  return (
    <HostIdentity
      className="animate-rise border-b-0 pb-0"
      logo={
        marks.length > 0 ? (
          <span className="flex h-12 shrink-0 items-center">
            <ProductLogos ids={marks} size="md" />
          </span>
        ) : undefined
      }
      fallback={ShieldCheck}
      title="Certificates"
      facts={
        !list && certs.loading ? (
          <TextShimmer>Reading the certificates…</TextShimmer>
        ) : (
          <>
            {/* On a phone the line keeps to what fits on one, so it is the
                height it was while it loaded: which certbot is the Renewal
                view's, and how soon is the list's first row. */}
            <span className="contents max-sm:hidden">
              {certbot.data ? (
                <HostFact product="lets-encrypt">
                  certbot
                  {certbot.data.version
                    ? ` ${certbot.data.version.replace(/^certbot\s*/i, "")}`
                    : ""}
                </HostFact>
              ) : certbotGone ? (
                <span>no certbot</span>
              ) : null}
              {(certbot.data || certbotGone) && <FactDot />}
            </span>
            <span className="numeric">{plural(counts.all, "certificate")}</span>
            {counts.expiring > 0 && (
              <>
                <FactDot />
                <span className="numeric font-medium text-warning">
                  {counts.expiring} expiring
                  <span className="max-sm:hidden"> in {plural(counts.soonest, "day")}</span>
                </span>
              </>
            )}
            {counts.refused > 0 && (
              <>
                <FactDot />
                <span className="numeric font-medium text-destructive" title={counts.refusedWhy}>
                  {counts.refused} refused
                </span>
              </>
            )}
            {certbot.data && !certbotGone && (
              <>
                <FactDot />
                {/* Its reading in a word, with what the record says behind it
                    on the Renewal view, where the run and its reason are. */}
                <Link
                  href={certificatesHref("renewal")}
                  title={renewal.hint}
                  data-slot="renewal-fact"
                  className={cn(
                    "rounded-sm focus-ring transition-colors hover:text-foreground hover:underline",
                    renewal.tone === "danger" && "font-medium text-destructive",
                    renewal.tone === "warning" && "font-medium text-warning",
                  )}
                >
                  renewal {renewal.value.toLowerCase()}
                </Link>
              </>
            )}
          </>
        )
      }
      aside={
        <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
          {verdict}
          <div className="flex items-center gap-2">
            {admin && (
              <IssueCommand
                onLetsEncrypt={work.issue ? () => work.issue?.() : undefined}
                onPrivate={(kind) => work.issuePrivate?.(kind)}
                onRequest={() => work.requestSigning?.()}
              />
            )}
            <VerbMenu verbs={more} label="More certificate actions" />
          </div>
        </div>
      }
    />
  )
}

/** The line's figures, read over every certificate rather than what a filter leaves. */
function countCertificates(all: Certificate[]) {
  // A test certificate is refused like an expired one, and counted there
  // rather than as expiring: its days say nothing about when it stops working.
  const readable = all.filter((c) => !c.error)
  const expiring = readable.filter((c) => c.expiring && !c.expired && !c.staging)
  const expired = readable.filter((c) => c.expired && !c.staging).length
  const test = readable.filter((c) => c.staging).length
  const unreadable = all.length - readable.length
  return {
    all: all.length,
    expiring: expiring.length,
    soonest: Math.min(...expiring.map((c) => c.daysLeft)),
    refused: expired + test + unreadable,
    refusedWhy: [
      expired > 0 && `${expired} expired`,
      test > 0 && plural(test, "test certificate"),
      unreadable > 0 && `${unreadable} unreadable`,
    ]
      .filter(Boolean)
      .join(" · "),
  }
}

/**
 * The list's shape before it has been read: the toolbar, the chips and rows
 * of a certificate row's own height, so the list lands where these stand.
 */
function InventoryLoading() {
  return (
    <div className="min-w-0 space-y-4" aria-busy="true" data-slot="cert-list-loading">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Skeleton className="h-8 w-full sm:w-72" />
        <Skeleton className="h-8 w-48 max-sm:hidden" />
      </div>
      <Skeleton className="h-7 w-24" />
      <ul className="min-w-0 space-y-2">
        {[0, 1, 2].map((i) => (
          <li key={i}>
            <Skeleton className="h-16 w-full rounded-xl" />
          </li>
        ))}
      </ul>
    </div>
  )
}

/**
 * What renews the certificates: certbot's schedule and what it did, the
 * deploy hook, every certbot lineage with its verbs, each run as certbot
 * printed it and the renewal service's journal.
 */
function RenewalView({ onShowLog }: { onShowLog: () => void }) {
  const work = useCertificateWork()
  const { admin, hasNginx, certbot, certbotGone, certbotBusy, lockJob } = work
  const [starting, setStarting] = useState(false)
  // The timer's own service, started now: systemd records the run, so the
  // renewal reading changes with it.
  const runRenewal = async () => {
    setStarting(true)
    try {
      work.attach(await post<Job>("/certificates/renewal/run"))
    } catch (err) {
      notify.error("The renewal did not start", err)
    } finally {
      setStarting(false)
    }
  }
  const lineages = certbot.data?.certs ?? []
  return (
    <>
      <Panel plain aria-label="Renewal schedule" className={COLUMN}>
        <PanelHeader
          title="Schedule"
          actions={
            admin &&
            lineages.length > 0 && (
              <Button
                size="sm"
                variant="outline"
                disabled={work.renewing !== "" || certbotBusy}
                pending={
                  work.renewing === ALL_CERTS ||
                  (lockJob?.kind === "certbot.renew" && lockJob.target === "every certificate due")
                }
                onClick={() => work.renew(ALL_CERTS, false)}
              >
                <RefreshClockwise className="size-3.5" />
                Renew all due
              </Button>
            )
          }
        />
        <PanelBody flush>
          {admin && <RecentJobs kinds={["certbot."]} onOpen={work.openJob} className="pt-3" />}
          {certbot.data && (
            <RenewalNotice state={certbot.data} admin={admin} onChanged={certbot.refresh} />
          )}
          {certbotGone ? (
            <CertbotMissing onInstall={admin ? work.attach : undefined} />
          ) : certbot.loading && !certbot.data ? (
            <LoadingRows rows={3} />
          ) : certbot.error && !certbot.data ? (
            <ErrorState error={certbot.error} onRetry={certbot.refresh} />
          ) : certbot.data ? (
            <>
              <CertbotRuntimeLine state={certbot.data} />
              <RenewalRecord
                state={certbot.data}
                admin={admin}
                certbotBusy={certbotBusy || starting || work.renewing !== ""}
                running={lockJob?.kind === "certbot.renewal"}
                onRun={runRenewal}
                onShowLog={onShowLog}
              />
              {admin && hasNginx && (
                <ReloadHookOption state={certbot.data} onChanged={certbot.refresh} />
              )}
            </>
          ) : null}
        </PanelBody>
      </Panel>

      {certbot.data && !certbotGone && (
        <Panel plain aria-label="certbot's certificates" className={COLUMN}>
          <PanelHeader
            title="certbot's certificates"
            actions={
              lineages.length > 0 && (
                <span className="numeric text-hint text-muted-foreground">{lineages.length}</span>
              )
            }
          />
          <PanelBody flush>
            <CertbotLineages
              state={certbot.data}
              admin={admin}
              busy={work.renewing}
              job={lockJob}
              onOpenJob={work.openCertbotJob}
              onRenew={work.renew}
              onReplace={
                work.testAuthority
                  ? undefined
                  : (domains) => work.issue?.({ domains, staging: false })
              }
              onRevoke={(name) => work.revoke(name)}
              onDelete={(name) => work.remove("certbot", name)}
              onShowLog={onShowLog}
            />
          </PanelBody>
        </Panel>
      )}

      {admin && !certbotGone && <RenewalRuns id={RUNS_ID} />}

      {/* Every renewal certbot ran, not only the ones started from this
          page: the timer's runs are the ones nobody was watching. */}
      {!certbotGone && <RenewalLog certbot={certbot.data} />}
    </>
  )
}

/** The local CA, or for a reader on a server with none, that there is none. */
function AuthorityView() {
  const work = useCertificateWork()
  const { admin, localCA } = work
  if (!admin && !localCA.loading && !localCA.error && !localCA.data?.exists) {
    return (
      <EmptyState
        icon={ShieldCheck}
        title="This server has no local CA"
        description="An administrator can make one to sign certificates for internal names."
      />
    )
  }
  return (
    <LocalCAPanel
      ca={localCA.data}
      error={localCA.error}
      loading={localCA.loading}
      admin={admin}
      onRetry={localCA.refresh}
      onIssue={() => work.issuePrivate?.("local-ca")}
      onChanged={localCA.refresh}
    />
  )
}

/** Requests waiting for an outside authority's answer, or the way to make one. */
function RequestsView() {
  const work = useCertificateWork()
  const { admin, requests, certs } = work
  const none = requests.data && requests.data.length === 0
  return (
    <>
      {requests.loading && !requests.data && <LoadingRows rows={3} />}
      <SigningRequests
        requests={requests.data}
        error={requests.error}
        admin={admin}
        onRetry={requests.refresh}
        onChanged={() => {
          requests.refresh()
          certs.refresh()
        }}
      />
      {none && (
        <EmptyState
          icon={FileText}
          title="No signing request is waiting"
          description="A request carries this server's key to an outside authority; its certificate is added here when it comes back."
          action={
            work.requestSigning && (
              <Button size="sm" variant="outline" onClick={work.requestSigning}>
                New signing request
              </Button>
            )
          }
        />
      )}
    </>
  )
}
