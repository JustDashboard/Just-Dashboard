"use client"

import { createContext, useCallback, useContext, useEffect, useRef, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import {
  CheckCircle,
  CloudUpload,
  Code,
  Copy,
  Eye,
  Inspect,
  Pencil,
  RefreshClockwise,
  ShieldCheck,
  Trash,
  Warning,
} from "@/components/icons"
import { ApiError, del, errorMessage, get, post } from "@/lib/api"
import {
  afterReload,
  certbotRunning,
  certSource,
  renewIfDueLabel,
  replacingTestCertificate,
  runningCertbotJob,
  sameNames,
  sslDirectives,
  stillServingTest,
  testCertificateReplaced,
  testRunPassed,
} from "@/lib/certificates"
import { copyText } from "@/lib/clipboard"
import { notify } from "@/lib/toast"
import type {
  CertbotState,
  Certificate,
  DNSProvider,
  Job,
  LocalCA,
  ServedCertificate,
  SigningRequest,
} from "@/lib/types"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm, type ConfirmRequest } from "@/components/confirm-dialog"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import type { Verb } from "@/components/verbs"
import { JobIssuanceProblems } from "@/components/proxy/issuance-problems"
import { useProxy, useProxyRead } from "@/components/proxy/proxy-context"
import { forceRenewal, IssueDialog, useRenew } from "@/components/proxy/certbot-panel"
import { PemDecoder } from "@/components/proxy/cert-detail"
import { CsrDialog } from "@/components/proxy/csr-dialog"
import { ImportDialog } from "@/components/proxy/import-dialog"
import { PrivateCertificateDialog, type PrivateKind } from "@/components/proxy/private-cert-dialog"

type IssueState = {
  open: boolean
  domains?: string
  staging: boolean
  /** A lineage whose names the form changes. */
  target?: string
}

export type CertificateWork = {
  admin: boolean
  hasNginx: boolean
  certs: PollState<Certificate[]>
  certbot: PollState<CertbotState>
  /** certbot is not on this host: nothing here can issue or renew through it. */
  certbotGone: boolean
  /** A staging directory is configured: nothing issued here is a certificate browsers accept. */
  testAuthority: boolean
  providers: PollState<DNSProvider[]>
  requests: PollState<SigningRequest[]>
  localCA: PollState<LocalCA>
  /** The job on screen, whatever started it. */
  job: Job | null
  /** The certbot run holding certbot's lock, wherever it was started, or else the one on screen. */
  lockJob: Job | null
  /** A certbot run holds certbot's one lock: every certbot verb waits for it. */
  certbotBusy: boolean
  /** Puts the running certbot job in the console. */
  openCertbotJob?: () => void
  /** Changes each time a job on screen ends, so a page refreshes its own reads. */
  ended: string
  /** The certificate a renewal asked from here is running for, or `*` for all. */
  renewing: string
  renew: (name: string, dryRun: boolean, force?: boolean) => void
  attach: (job: Job) => void
  openJob: (id: string) => Promise<void>
  /** Opens certbot's issuance; absent where nobody here can issue. */
  issue?: (options?: { domains?: string; staging?: boolean; target?: string }) => void
  /** Opens the import, on a name to replace its pair; absent for a reader. */
  importPair?: (name?: string) => void
  issuePrivate?: (kind: PrivateKind) => void
  requestSigning?: () => void
  /** Decodes a pasted certificate; anyone signed in may. */
  decode: () => void
  confirm: (request: ConfirmRequest) => void
  /** Bumped when a name is added to the watched domains, so the list reads again. */
  watchedAdded: number
  /** Deletes a certbot lineage or an import, naming the sites that use it first. */
  remove: (source: string, name: string, onDone?: () => void) => void
  /** Revokes a certbot lineage and deletes it; it asks first. */
  revoke: (name: string, onDone?: () => void) => void
  /** The verbs a certificate has, for its row's menu and its page. */
  verbsFor: (cert: Certificate, options?: { onDeleted?: () => void }) => Verb[]
  /** Whether certbot is replacing this certificate right now. */
  replacing: (cert: Certificate) => boolean
}

const Work = createContext<CertificateWork | null>(null)

export function useCertificateWork(): CertificateWork {
  const work = useContext(Work)
  if (!work) throw new Error("useCertificateWork outside CertificateWorkProvider")
  return work
}

/**
 * What the Certificates page and a certificate's own page share: certbot's
 * state, the job console every certbot run reports into, and the forms that
 * issue, import and decode. It is the section's certificates layout, so a
 * renewal started on a certificate's page is still on screen when the reader
 * goes back to the list, and the forms open over either page.
 *
 * It was all the list page's, which was right while the certificate was a
 * sheet over that list; a certificate with an address of its own needs the
 * same verbs, and two copies of the console would each miss the other's run.
 */
export function CertificateWorkProvider({ children }: { children: React.ReactNode }) {
  const { can } = useAuth()
  const { status } = useProxy()
  const router = useRouter()
  const params = useSearchParams()
  const { confirm, dialog } = useConfirm()
  const admin = can("system.admin")
  const hasNginx = status?.nginx ?? false

  const certs = useProxyRead("certs")
  const certbot = usePoll<CertbotState>(
    (signal) => get("/certificates/certbot", undefined, signal),
    300_000,
  )
  const requests = usePoll<SigningRequest[]>(
    (signal) => get("/certificates/csr", undefined, signal),
    300_000,
  )
  const localCA = usePoll<LocalCA>(
    (signal) => get("/certificates/local-ca", undefined, signal),
    300_000,
  )
  const providers = usePoll<DNSProvider[]>(
    (signal) => get("/certificates/dns-providers", undefined, signal),
    0,
    [],
    { enabled: admin },
  )

  // The list is only right once certbot has finished writing, so it is read
  // again when a job ends rather than when it starts — however it ended: a
  // renewal that failed changes the renewal record as much as one that passed.
  const console_ = useJobConsole()
  const job = console_.job
  const ended = job && job.status !== "running" ? `${job.id}:${job.status}` : ""
  const { refresh: refreshCerts } = certs
  const { refresh: refreshCertbot } = certbot
  // An install or a test changes which plugins and credentials are there.
  const { refresh: refreshProviders } = providers
  useEffect(() => {
    if (!ended) return
    refreshCerts()
    refreshCertbot()
    if (admin) refreshProviders()
  }, [ended, admin, refreshCerts, refreshCertbot, refreshProviders])
  const { busy: renewing, renew } = useRenew(console_.attach)

  // certbot's lock is the host's, not this tab's: a run started from another
  // tab, or before this page loaded, holds it just the same.
  const jobs = usePoll<Job[]>((signal) => get("/jobs/", undefined, signal), 5_000, [], {
    enabled: admin,
    key: "/jobs",
  })
  const elsewhere = runningCertbotJob(jobs.data)
  const certbotJob = certbotRunning(job) ? job : elsewhere
  // A run this console never showed ends unseen by the refresh above.
  const elsewhereId = elsewhere?.id ?? ""
  const seenElsewhere = useRef("")
  useEffect(() => {
    if (seenElsewhere.current && !elsewhereId) {
      refreshCerts()
      refreshCertbot()
    }
    seenElsewhere.current = elsewhereId
  }, [elsewhereId, refreshCerts, refreshCertbot])
  const certbotBusy = certbotJob !== null
  const lockJob = certbotJob ?? job
  const openCertbotJob = certbotJob ? () => void console_.open(certbotJob.id) : undefined

  const certbotGone =
    certbot.error instanceof ApiError && certbot.error.code === "certbot_unavailable"
  // A configured staging directory signs test certificates: nothing issued
  // from here replaces one with a certificate browsers accept.
  const testAuthority = certbot.data?.testAuthority ?? false

  // ?issue=app.example.com opens the issuance form on those names: the site
  // form and the TLS report link here when they name a certificate that does
  // not exist yet. It is a one-shot hand-off: left in the address, a reload
  // opened the form again after it had been closed or used.
  const asked = params.get("issue")
  const [issueState, setIssue] = useState<IssueState>(() => ({
    open: admin && Boolean(asked),
    domains: asked ?? undefined,
    staging: true,
  }))
  const handled = useRef(asked)
  useEffect(() => {
    if (!asked || asked === handled.current || !admin) return
    handled.current = asked
    setIssue({ open: true, domains: asked, staging: true })
  }, [asked, admin])
  const setIssueOpen = (open: boolean) => {
    setIssue((s) => ({ ...s, open }))
    if (!open && new URL(window.location.href).searchParams.has("issue")) {
      const url = new URL(window.location.href)
      url.searchParams.delete("issue")
      window.history.replaceState(null, "", `${url.pathname}${url.search}${url.hash}`)
      handled.current = null
    }
  }

  const [importing, setImporting] = useState<{ open: boolean; name?: string }>({ open: false })
  // The kind is kept while the dialog closes, so its body does not switch
  // to the other form on the way out.
  const [privateDialog, setPrivateDialog] = useState<{ open: boolean; kind: PrivateKind }>({
    open: false,
    kind: "local-ca",
  })
  const [csrOpen, setCsrOpen] = useState(false)
  const [decoding, setDecoding] = useState(false)
  const [watchedAdded, setWatchedAdded] = useState(0)

  const canIssue = admin && !certbotGone
  const issue = useCallback(
    (options?: { domains?: string; staging?: boolean; target?: string }) =>
      setIssue({
        open: true,
        domains: options?.domains,
        staging: options?.staging ?? true,
        target: options?.target,
      }),
    [],
  )

  const watch = async (domain: string) => {
    try {
      await post("/certificates/watched", { domain, port: 443 })
      notify.success(`Watching ${domain}`)
      setWatchedAdded((n) => n + 1)
    } catch (err) {
      notify.error("Could not watch domain", err)
    }
  }

  const revoke = (name: string, onDone?: () => void) =>
    confirm({
      title: `Revoke ${name}`,
      confirmLabel: "Revoke and delete",
      description: (
        <p className="text-destructive">
          The authority publishes that this certificate is no longer to be trusted and the files are
          deleted. There is no undo, and every client holding it starts refusing the site.
        </p>
      ),
      action: async (c) => {
        console_.attach(await post<Job>("/certificates/revoke", { name }, { confirm: c }))
        onDone?.()
      },
    })

  // Deleting revokes nothing. A certificate a site names is deleted only once
  // the dialog has named those sites, and only then is force sent: nginx keeps
  // serving it until its next reload, which fails for an enabled one.
  const remove = (source: string, name: string, onDone?: () => void) => {
    const usedBy = certs.data?.find((c) => c.source === source && c.name === name)?.usedBy ?? []
    const force = usedBy.length > 0
    confirm({
      title: `Delete ${name}`,
      confirmLabel: force ? "Delete anyway" : "Delete",
      description: (
        <div className="space-y-3">
          <p>
            {source === "certbot"
              ? "certbot deletes the certificate, its key and its renewal configuration, so the renewal schedule stops renewing it. Nothing is revoked: a copy elsewhere stays valid until it expires."
              : "The certificate, its key and the copies kept when it was replaced are deleted. Import the pair again to bring it back."}
          </p>
          {force && (
            <>
              <p className="text-destructive">
                {usedBy.length === 1 ? "This site names" : "These sites name"} it. nginx keeps
                serving it until its next reload, which fails for an enabled site until it names
                another certificate.
              </p>
              <ul className="space-y-1 font-mono text-hint break-all">
                {usedBy.map((site) => (
                  <li key={site}>{site}</li>
                ))}
              </ul>
            </>
          )}
        </div>
      ),
      action: async () => {
        if (source === "certbot") {
          console_.attach(await post<Job>("/certificates/delete", { names: [name], force }))
          onDone?.()
          return
        }
        await del(`/certificates/imported/${encodeURIComponent(name)}`, {
          query: { force: force ? 1 : undefined },
        })
        notify.success(`Deleted ${name}`)
        certs.refresh()
        onDone?.()
      },
    })
  }

  const busy = certbotRunning(lockJob)
  // Only certbot's own file is replaced in place; a copy a site names
  // elsewhere keeps its test certificate whatever certbot does.
  const replacing = (cert: Certificate) =>
    cert.source === "certbot" && replacingTestCertificate(lockJob, cert.domains)

  const verbsFor = (cert: Certificate, options?: { onDeleted?: () => void }): Verb[] => {
    const verbs: Verb[] = []
    const from = certSource(cert)
    const names = cert.domains.filter((domain) => !domain.startsWith("*."))
    // Caddy obtains its own from the directory its routes name; certbot
    // issuing one for the same names would change nothing Caddy serves.
    if (
      canIssue &&
      !testAuthority &&
      cert.staging &&
      cert.source !== "caddy" &&
      cert.domains.length > 0
    ) {
      verbs.push({
        key: "replace",
        label: from === "certbot" ? "Replace with a real certificate" : "Issue a real certificate",
        icon: ShieldCheck,
        inline: true,
        disabled: busy,
        run: () => issue({ domains: cert.domains.join(" "), staging: false }),
      })
    }
    if (from === "certbot" && canIssue) {
      // A test certificate renews from the staging authority its lineage
      // names, which only fetches another one browsers refuse: its verb is
      // the real issuance above.
      if (!cert.error && !cert.staging) {
        verbs.push(
          {
            key: "renew",
            label: renewIfDueLabel(cert),
            icon: RefreshClockwise,
            inline: true,
            disabled: busy,
            group: "certbot",
            run: () => renew(cert.name, false),
          },
          {
            key: "dry-run",
            label: "Dry run",
            icon: ShieldCheck,
            disabled: busy,
            group: "certbot",
            run: () => renew(cert.name, true),
          },
          {
            key: "renew-now",
            label: "Renew now",
            icon: Warning,
            disabled: busy,
            group: "certbot",
            run: () => confirm(forceRenewal(cert.name, () => renew(cert.name, false, true))),
          },
        )
      }
      if (!cert.error) {
        verbs.push({
          key: "names",
          label: "Change names",
          icon: Pencil,
          disabled: busy,
          group: "certbot",
          run: () => issue({ domains: cert.domains.join(" "), staging: true, target: cert.name }),
        })
      }
    }
    if (from === "imported" && admin) {
      verbs.push({
        key: "replace-import",
        label: "Replace",
        icon: CloudUpload,
        inline: true,
        run: () => setImporting({ open: true, name: cert.name }),
      })
    }
    if (admin) {
      for (const name of names) {
        verbs.push({
          key: `tls:${name}`,
          label: `TLS report for ${name}`,
          icon: Inspect,
          group: "Inspect",
          run: () => router.push(`/proxy/tls?domain=${encodeURIComponent(name)}`),
        })
      }
      for (const name of names) {
        verbs.push({
          key: `watch:${name}`,
          label: `Watch ${name}`,
          icon: Eye,
          group: "Inspect",
          run: () => void watch(name),
        })
      }
    }
    const keyPath =
      from === "certbot"
        ? certbot.data?.certs.find((lineage) => lineage.name === cert.name)?.keyPath
        : from === "imported"
          ? `${cert.path.slice(0, cert.path.lastIndexOf("/"))}/privkey.pem`
          : undefined
    if (keyPath) {
      verbs.push({
        key: "directives",
        label: "Copy nginx directives",
        icon: Code,
        group: "Copy",
        run: () => void copyText(sslDirectives(cert.path, keyPath), "Directives copied"),
      })
    }
    // Caddy's file is inside its container, where no site on this host reads it.
    if (from !== "caddy") {
      verbs.push({
        key: "path",
        label: "Copy path",
        icon: Copy,
        group: "Copy",
        run: () => void copyText(cert.path, "Path copied"),
      })
    }
    // Only certbot's lineages and the imports are this host's to delete: a
    // file a site names elsewhere is the site's, and Caddy renews its own.
    if (admin && (from === "imported" || (from === "certbot" && !certbotGone))) {
      verbs.push({
        key: "delete",
        label: "Delete",
        icon: Trash,
        danger: true,
        disabled: from === "certbot" && busy,
        run: () => remove(cert.source, cert.name, options?.onDeleted),
      })
    }
    if (from === "certbot" && canIssue) {
      verbs.push({
        key: "revoke",
        label: "Revoke and delete",
        icon: Trash,
        danger: true,
        disabled: busy,
        run: () => revoke(cert.name, options?.onDeleted),
      })
    }
    return verbs
  }

  const work: CertificateWork = {
    admin,
    hasNginx,
    certs,
    certbot,
    certbotGone,
    testAuthority,
    providers,
    requests,
    localCA,
    job,
    lockJob,
    certbotBusy,
    openCertbotJob,
    ended,
    renewing,
    renew,
    attach: console_.attach,
    openJob: console_.open,
    issue: canIssue ? issue : undefined,
    importPair: admin ? (name) => setImporting({ open: true, name }) : undefined,
    issuePrivate: admin ? (kind) => setPrivateDialog({ open: true, kind }) : undefined,
    requestSigning: admin ? () => setCsrOpen(true) : undefined,
    decode: () => setDecoding(true),
    confirm,
    watchedAdded,
    remove,
    revoke,
    verbsFor,
    replacing,
  }

  return (
    <Work.Provider value={work}>
      <ConsoleContext.Provider value={console_}>
        {children}
        <PemDecoder open={decoding} onOpenChange={setDecoding} />
        {admin && (
          <>
            <IssueDialog
              open={issueState.open}
              onOpenChange={setIssueOpen}
              initialDomains={issueState.domains}
              initialStaging={issueState.staging}
              initialTarget={issueState.target}
              hasNginx={hasNginx}
              plugins={
                certbot.data && !certbot.data.runtime.pluginsError
                  ? certbot.data.runtime.plugins
                  : undefined
              }
              providers={providers.data ?? []}
              directory={certbot.data?.directory}
              testAuthority={testAuthority}
              certs={certbot.data?.certs}
              certbotBusy={certbotBusy}
              onStarted={(started) => {
                console_.attach(started)
                providers.refresh()
              }}
            />
            <ImportDialog
              open={importing.open}
              onOpenChange={(open) => setImporting((s) => ({ ...s, open }))}
              initialName={importing.name}
              onDone={certs.refresh}
            />
            <PrivateCertificateDialog
              open={privateDialog.open}
              kind={privateDialog.kind}
              caExists={localCA.data?.exists ?? false}
              onOpenChange={(open) => setPrivateDialog((d) => ({ ...d, open }))}
              onDone={() => {
                certs.refresh()
                localCA.refresh()
              }}
            />
            <CsrDialog open={csrOpen} onOpenChange={setCsrOpen} onDone={requests.refresh} />
          </>
        )}
        {dialog}
      </ConsoleContext.Provider>
    </Work.Provider>
  )
}

const ConsoleContext = createContext<ReturnType<typeof useJobConsole> | null>(null)

/**
 * The run on screen and what follows from it: the console, why an issuance
 * failed, the real issuance once a test run has passed, and the reload a
 * replaced test certificate still needs. Each page draws it under its
 * identity line, where a run started from either is seen.
 */
export function CertificateRun() {
  const work = useCertificateWork()
  const console_ = useContext(ConsoleContext)
  const { admin, hasNginx, job, certs, testAuthority } = work
  // A test run that passed is the moment to issue the real one, with the
  // same names and nothing to retype.
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

  if (!console_) return null
  return (
    <>
      <JobConsole
        job={console_.job}
        lines={console_.lines}
        onDismiss={console_.dismiss}
        onCancel={console_.cancel}
      />
      {admin && <JobIssuanceProblems job={console_.job} />}

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
              onClick={() => work.issue?.({ domains: testPassed, staging: false })}
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
    </>
  )
}
