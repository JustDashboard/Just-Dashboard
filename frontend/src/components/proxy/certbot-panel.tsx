"use client"

import { useEffect, useState } from "react"
import {
  Check,
  Clock,
  Copy,
  Download,
  Key,
  Logs,
  Pencil,
  Play,
  RefreshClockwise,
  ShieldCheck,
  ShieldOff,
  Trash,
  Warning,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, errorMessage, get, post } from "@/lib/api"
import {
  authorityName,
  certbotRunning,
  lineageActivity,
  parseDomains,
  renewalMethod,
} from "@/lib/certificates"
import type {
  ACMEAccount,
  CertbotCert,
  CertbotState,
  DNSProvider,
  IssuePreflight,
  IssuePreview,
  Job,
  VHost,
} from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { useCopy } from "@/hooks/use-copy"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm, type ConfirmRequest } from "@/components/confirm-dialog"
import { FindingList } from "@/components/finding-list"
import { Field, OptionList, OptionRow } from "@/components/form"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ProductLogo, ProductLogos } from "@/components/product-logo"
import { Meter } from "@/components/meter"
import { ROW_BLEED } from "@/components/row-list"
import { CertLife } from "@/components/proxy/expiry-status"
import { dnsProviderProduct } from "@/components/proxy/marks"
import { EmptyState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { VerbActions, VerbBar, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { cn } from "@/lib/utils"

/**
 * Certificates, issued and renewed from the page that says they are expiring.
 *
 * The dashboard already knows a certificate has eleven days left; leaving the
 * operator to go and remember certbot's arguments is where every panel in this
 * class stops and where the work starts. The arguments are also the part that
 * is easy to get wrong expensively — --standalone on a host running nginx
 * binds port 80 and fails, and forcing renewal is how people spend their five
 * duplicate certificates a week.
 *
 * The renewal schedule gets a reading of its own because it is the real story
 * behind almost every expired certificate: not a forgotten renewal, a renewal
 * timer that stopped months ago and told nobody — and where systemd knows the
 * timer, the page turns it back on rather than only saying so.
 */

/** Busy sentinel for "renew everything", which has no certificate name. */
export const ALL_CERTS = "*"

export function useRenew(attach: (job: Job) => void) {
  const [busy, setBusy] = useState("")
  const renew = async (name: string, dryRun: boolean, force = false) => {
    setBusy(name)
    try {
      // 202 with a job: certbot's exchange with the authority is watched
      // rather than waited on, and it keeps going if this page is closed.
      const job = await post<Job>("/certificates/renew", {
        // certbot renews every due lineage when no --cert-name is given.
        name: name === ALL_CERTS ? "" : name,
        dryRun,
        force,
      })
      attach(job)
    } catch (err) {
      notify.error(dryRun ? "Dry run refused" : "Renewal refused", err)
    } finally {
      setBusy("")
    }
  }
  return { busy, renew }
}

/**
 * certbot's own lineages, each with its verbs as words: renew inline, and
 * the dry run, the forced renewal and the revocation behind the menu with a
 * sentence each — the forced one spends a rate-limited duplicate and the
 * revocation cannot be undone, which is not something a glyph can say.
 *
 * A test certificate has no renewal verbs: certbot renews from the authority
 * in the lineage's configuration, the staging one, so renewing only fetched
 * another certificate browsers refuse. Its verb is the real issuance for the
 * same names, which replaces it.
 *
 * certbot holds one lock for all of its work, so while the job on screen is
 * a certbot run every verb here waits for it, and the lineage it acts on
 * says what is happening to it.
 *
 * Each lineage says how certbot renews it — the plugin, and the folder or
 * DNS provider it names — and what stands in the way: why the last run
 * failed on it, in certbot's words, and anything that will make the next
 * one fail. Either brings the dry run out of the menu, since it is how to
 * find out whether a fix worked.
 */
/**
 * The question before a forced renewal: it spends one of the duplicate
 * certificates the authority allows per week for the same names.
 */
export function forceRenewal(name: string, run: () => void): ConfirmRequest {
  return {
    title: `Force renewal of ${name}`,
    confirmLabel: "Renew now",
    description: (
      <p>
        certbot normally refuses to renew a certificate that is not due. Forcing it spends one of
        the five duplicate certificates Let&rsquo;s Encrypt allows per week for this set of names.
      </p>
    ),
    action: async () => run(),
  }
}

/**
 * Why every certbot verb is disabled, with the way to the run holding its
 * lock: started from another tab, or before this page loaded, it is not the
 * one on screen.
 */
export function CertbotBusy({ onOpen, className }: { onOpen?: () => void; className?: string }) {
  return (
    <p className={cn("text-hint text-muted-foreground", className)}>
      certbot is busy
      {onOpen && (
        <>
          {" — "}
          <Button variant="link" size="xs" className="h-auto p-0 text-hint" onClick={onOpen}>
            open
          </Button>
        </>
      )}
    </p>
  )
}

export function CertbotLineages({
  state,
  admin,
  busy,
  job,
  onRenew,
  onReplace,
  onRevoke,
  onDelete,
  onShowLog,
  onOpenJob,
}: {
  state: CertbotState
  admin: boolean
  busy: string
  /** The running certbot job, wherever it was started, or else the one on screen. */
  job: Job | null
  onRenew: (name: string, dryRun: boolean, force?: boolean) => void
  /** Opens the real issuance for a test certificate's names; absent where none can be issued. */
  onReplace?: (domains: string) => void
  onRevoke: (name: string) => void
  /** Deletes the lineage without revoking it. */
  onDelete: (name: string) => void
  /** Opens the renewal schedule's log. */
  onShowLog: () => void
  /** Puts the running certbot job in the console. */
  onOpenJob?: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const running = certbotRunning(job)
  if (state.error) {
    return (
      <Notice tone="danger" icon={Warning} title="certbot's lineages could not be read">
        <span className="break-all">{state.error}</span>
      </Notice>
    )
  }
  if (state.certs.length === 0) {
    return (
      <EmptyState
        mark={<ProductLogos ids={["lets-encrypt"]} size="md" />}
        title="certbot manages no certificates yet"
        description="Issue one and it appears here with its renewal handled by certbot's own schedule."
        className="mt-2"
      />
    )
  }
  const waiting = busy !== "" || running
  const verbsFor = ({ name, domains, staging, lastFailure, willFail }: CertbotCert): Verb[] => {
    const troubled = Boolean(lastFailure) || Boolean(willFail?.length)
    const revoke: Verb = {
      key: "revoke",
      label: "Revoke and delete",
      icon: Trash,
      danger: true,
      disabled: waiting,
      run: () => onRevoke(name),
    }
    const remove: Verb = {
      key: "delete",
      label: "Delete",
      icon: Trash,
      danger: true,
      disabled: waiting,
      run: () => onDelete(name),
    }
    if (staging) {
      return onReplace
        ? [
            {
              key: "replace",
              label: "Replace with a real certificate",
              icon: ShieldCheck,
              inline: true,
              disabled: waiting,
              run: () => onReplace(domains.join(" ")),
            },
            remove,
            revoke,
          ]
        : [remove, revoke]
    }
    return [
      {
        key: "renew",
        label: "Renew",
        icon: RefreshClockwise,
        inline: true,
        disabled: waiting,
        run: () => onRenew(name, false),
      },
      {
        key: "dry-run",
        label: "Dry run",
        icon: ShieldCheck,
        inline: troubled,
        disabled: waiting,
        run: () => onRenew(name, true),
      },
      ...(lastFailure
        ? [{ key: "log", label: "Show log", icon: Logs, inline: true, run: onShowLog }]
        : []),
      {
        key: "force",
        label: "Force renewal",
        icon: Warning,
        disabled: waiting,
        run: () => confirm(forceRenewal(name, () => onRenew(name, false, true))),
      },
      remove,
      revoke,
    ]
  }
  return (
    <>
      {admin && running && <CertbotBusy onOpen={onOpenJob} className="pt-3 pb-1" />}
      <ul aria-label="certbot lineages" className="animate-rise divide-y divide-hairline">
        {state.certs.map((cert) => {
          const activity = lineageActivity(job, cert) ?? (busy === cert.name ? "Renewing…" : "")
          return (
            <li key={cert.name} className="space-y-3 py-4 first:pt-1">
              <div className="flex min-w-0 items-center gap-3">
                <ProductLogo
                  id={cert.staging ? undefined : "lets-encrypt"}
                  size="sm"
                  fallback={ShieldOff}
                />
                <div className="min-w-0 flex-1 basis-40">
                  <p className="truncate text-body font-medium" title={cert.name}>
                    {cert.name}
                  </p>
                  <p
                    className={cn(
                      "text-hint text-muted-foreground",
                      cert.error ? "break-words" : "break-all",
                    )}
                  >
                    {cert.error || cert.domains.join(", ")}
                  </p>
                </div>
              </div>
              <RenewalMethod cert={cert} />
              {!cert.error && <LineageLife cert={cert} />}
              {cert.lastFailure && (
                <p className="text-hint break-words text-destructive">
                  Last renewal failed: {cert.lastFailure.reason}
                </p>
              )}
              {cert.willFail && cert.willFail.length > 0 && (
                <Notice tone="warning" icon={Warning} title="The next renewal will fail">
                  <ul className="space-y-1">
                    {cert.willFail.map((reason) => (
                      <li key={reason} className="break-words">
                        {reason}
                      </li>
                    ))}
                  </ul>
                </Notice>
              )}
              {cert.staging && (
                <p className="text-hint text-muted-foreground">
                  A staging authority signed it, so browsers refuse it. A real issuance for the same
                  names replaces it.
                </p>
              )}
              <div className="flex flex-wrap items-center justify-between gap-2">
                {activity ? (
                  <Status state="activating" label={activity} />
                ) : cert.error ? (
                  <Status verdict="critical" label="unreadable" />
                ) : cert.staging ? (
                  <Status verdict="critical" label="test certificate" />
                ) : !cert.valid ? (
                  <Status verdict="critical" label="expired" />
                ) : (
                  <Status
                    verdict={cert.daysLeft <= 30 ? "warning" : "ok"}
                    label={`${cert.daysLeft}d left`}
                  />
                )}
                {admin && (
                  <VerbBar verbs={verbsFor(cert)} menuLabel={`More actions for ${cert.name}`} />
                )}
              </div>
            </li>
          )
        })}
      </ul>
      {dialog}
    </>
  )
}

/**
 * How certbot proves control when it renews the lineage: the plugin as a
 * word, then the folder the webroot plugin writes into or the DNS provider.
 */
function RenewalMethod({ cert }: { cert: CertbotCert }) {
  const method = renewalMethod(cert)
  if (!method) return null
  return (
    <p className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
      <span className="text-hint text-muted-foreground">Renews with</span>
      <Tag>{method.method}</Tag>
      {/* A folder is a path of any length: it wraps, where a tag would run
          past the column on a phone. */}
      {method.folders?.map((folder) => (
        <code key={folder} className="min-w-0 font-mono text-hint break-all">
          {folder}
        </code>
      ))}
      {method.detail && <Tag>{method.detail}</Tag>}
    </p>
  )
}

/**
 * The meter runs from the certificate's issue date to its expiry, whatever
 * the term: a ninety-day guess drew any other one wrong.
 */
function LineageLife({ cert }: { cert: CertbotCert }) {
  return (
    <CertLife
      className="w-full"
      cert={{
        notBefore: cert.notBefore,
        notAfter: cert.expiry,
        daysLeft: cert.daysLeft,
        expired: !cert.valid,
        expiring: cert.valid && cert.daysLeft <= 30,
        staging: cert.staging,
      }}
    />
  )
}

/**
 * Nothing will renew these — and, where systemd knows the timer, the button
 * that fixes it. `systemctl enable --now` is two requests here because the
 * services API exposes them as two verbs, which is also why the same switch
 * appears under Processes → Services.
 */
export function RenewalNotice({
  state,
  admin,
  onChanged,
}: {
  state: CertbotState
  admin: boolean
  onChanged: () => void
}) {
  const [busy, setBusy] = useState(false)
  if (state.autoRenew || state.certs.length === 0) return null
  const unit = state.renewUnit
  const turnOn = async () => {
    if (!unit) return
    setBusy(true)
    try {
      await post(`/systemd/${encodeURIComponent(unit)}/enable`)
      await post(`/systemd/${encodeURIComponent(unit)}/start`)
      notify.success(`${unit} is running`, {
        description: "certbot checks twice a day and renews anything inside thirty days.",
      })
      onChanged()
    } catch (err) {
      notify.error(`Could not start ${unit}`, err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Notice tone="warning" icon={Clock} title="Nothing is scheduled to renew these">
      <div className="space-y-2">
        <p>
          No active renewal schedule was found. Enable the timer before these certificates expire.
        </p>
        {unit ? (
          <div className="flex flex-wrap items-center gap-2">
            <span>
              <code className="font-mono">{unit}</code> is installed but not running.
            </span>
            {admin && (
              <Button size="xs" variant="outline" onClick={turnOn} pending={busy}>
                Turn it on
              </Button>
            )}
          </div>
        ) : (
          <p>
            Install certbot&rsquo;s timer (<code className="font-mono">certbot.timer</code> on
            Debian and Ubuntu) or a cron entry that runs{" "}
            <code className="font-mono">certbot renew</code>.
          </p>
        )}
      </div>
    </Notice>
  )
}

/**
 * Which certbot the page read and every job runs, and what it can prove
 * control with: "certbot 2.11.0 on the host · nginx, standalone, webroot".
 * The two used to be different certbots, which is how a plugin installed on
 * the host read as missing.
 */
export function CertbotRuntimeLine({ state }: { state: CertbotState }) {
  const { runtime } = state
  return (
    <p className="pt-3 text-hint break-words text-muted-foreground">
      {state.version || "certbot"} {runtime.onHost ? "on the host" : "in the dashboard's image"}
      {runtime.snap && " (snap)"}
      {runtime.pluginsError
        ? ` · its plugins could not be listed: ${runtime.pluginsError}`
        : runtime.plugins.length > 0 && ` · ${runtime.plugins.join(", ")}`}
    </p>
  )
}

/**
 * The DNS plugins certbot can drive from here, and whether each can: the
 * plugin installed, a token saved, and which lineages renew through it. The
 * token is never shown — that a credential for a whole DNS zone exists on
 * disk is the whole of what the page says about it — but it can be replaced,
 * tried with a dry run, and removed, the removal naming what stops renewing.
 */
export function DnsProvidersPanel({
  providers,
  admin,
  certs,
  installs,
  certbotBusy,
  onJob,
  onChanged,
}: {
  providers: DNSProvider[]
  admin: boolean
  /** certbot's lineages, for which of them renew through each provider. */
  certs: CertbotCert[]
  installs?: CertbotState["installs"]
  /** A certbot run is on screen: a test or an install would wait on its lock. */
  certbotBusy: boolean
  onJob: (job: Job) => void
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useState<DNSProvider | null>(null)
  const [testing, setTesting] = useState<DNSProvider | null>(null)
  const [installing, setInstalling] = useState("")
  const install = async (p: DNSProvider) => {
    setInstalling(p.plugin)
    try {
      onJob(await post<Job>("/certificates/certbot/install", { plugin: p.plugin }))
    } catch (err) {
      notify.error(`Could not install the ${p.name} plugin`, err)
    } finally {
      setInstalling("")
    }
  }
  return (
    <Panel plain>
      <PanelHeader title="DNS challenge providers" />
      <PanelBody flush>
        <ul className="divide-y divide-hairline">
          {providers.map((p) => {
            const users = certs.filter((c) => c.dnsProvider === p.name).map((c) => c.name)
            const route = installs?.[p.plugin]
            // Route 53 without a saved profile reads the machine's IAM role.
            const testable = p.installed && (p.hasCredentials || p.key === "route53")
            const verbs: Verb[] = [
              ...(!p.installed && route?.package
                ? [
                    {
                      key: "install",
                      label: "Install plugin",
                      icon: Download,
                      inline: true,
                      disabled: certbotBusy || installing !== "",
                      run: () => void install(p),
                    },
                  ]
                : []),
              ...(testable
                ? [
                    {
                      key: "test",
                      label: "Test credentials",
                      icon: Play,
                      inline: true,
                      disabled: certbotBusy,
                      run: () => setTesting(p),
                    },
                  ]
                : []),
              {
                key: "save",
                label: p.hasCredentials ? "Replace credentials" : "Save credentials",
                icon: Pencil,
                inline: !p.hasCredentials && p.installed,
                run: () => setEditing(p),
              },
              ...(p.hasCredentials
                ? [
                    {
                      key: "remove",
                      label: "Remove credentials",
                      icon: Trash,
                      danger: true,
                      run: () =>
                        confirm({
                          title: `Remove ${p.name} credentials`,
                          confirmLabel: "Remove",
                          description:
                            users.length > 0 ? (
                              <p className="text-destructive">
                                The saved token is deleted from disk, and{" "}
                                {users.length === 1 ? "this lineage stops" : "these lineages stop"}{" "}
                                renewing until credentials are saved again: {users.join(", ")}.
                              </p>
                            ) : (
                              <p>
                                The saved token is deleted from disk. No certbot lineage renews
                                through {p.name}.
                              </p>
                            ),
                          action: async () => {
                            await del(`/certificates/dns-credentials/${p.key}`)
                            onChanged()
                          },
                        }),
                    },
                  ]
                : []),
            ]
            return (
              <li
                key={p.key}
                className={cn(
                  "group flex min-w-0 flex-wrap items-center gap-3 py-4 transition-colors hover:bg-row-hover",
                  ROW_BLEED,
                )}
              >
                {/* The provider as itself where it has a mark — Cloudflare,
                    Route 53 as AWS — and a key for the ones that do not. */}
                <ProductLogo id={dnsProviderProduct(p.key)} size="sm" fallback={Key} />
                <div className="min-w-0 flex-1 basis-40">
                  <div className="flex min-w-0 flex-wrap items-baseline gap-2">
                    <span className="text-body font-medium">{p.name}</span>
                    <Tag mono>{p.plugin}</Tag>
                  </div>
                  <p className="text-hint text-muted-foreground">
                    {p.key === "route53"
                      ? "Reads the saved profile, or the machine's IAM role."
                      : `Waits ${p.defaultWait}s for the record to propagate.`}
                  </p>
                </div>
                {admin && <VerbActions dim verbs={verbs} />}
                <div className="flex w-full flex-wrap items-center gap-x-4 gap-y-2">
                  <Status
                    verdict={p.installed ? "ok" : "warning"}
                    label={
                      p.installed
                        ? "plugin installed"
                        : route?.package
                          ? `plugin missing · ${route.package}`
                          : "plugin missing"
                    }
                  />
                  <Status
                    tone={p.hasCredentials ? "running" : "stopped"}
                    label={p.hasCredentials ? "credentials saved" : "no credentials"}
                  />
                </div>
                {!p.installed && route?.reason && (
                  <p className="w-full text-hint break-words text-muted-foreground">
                    {route.reason}
                  </p>
                )}
                {users.length > 0 && (
                  <p className="w-full text-hint break-words text-muted-foreground">
                    Renews {users.join(", ")}
                  </p>
                )}
              </li>
            )
          })}
        </ul>
      </PanelBody>
      {editing && (
        <DnsCredentialsModal
          provider={editing}
          onOpenChange={(open) => !open && setEditing(null)}
          onSaved={onChanged}
        />
      )}
      {testing && (
        <DnsTestModal
          provider={testing}
          certbotBusy={certbotBusy}
          onOpenChange={(open) => !open && setTesting(null)}
          onStarted={onJob}
        />
      )}
      {dialog}
    </Panel>
  )
}

/**
 * Saves a provider's credentials, checked against the keys its plugin reads,
 * so a token pasted under the wrong name is refused here rather than by the
 * first challenge.
 */
function DnsCredentialsModal({
  provider,
  onOpenChange,
  onSaved,
}: {
  provider: DNSProvider
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const [credentials, setCredentials] = useState("")
  const [busy, setBusy] = useState(false)
  const save = async () => {
    setBusy(true)
    try {
      await post("/certificates/dns-credentials", { provider: provider.key, credentials })
      notify.success(`${provider.name} credentials saved`)
      onSaved()
      onOpenChange(false)
    } catch (err) {
      notify.error("The credentials were not saved", err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open
      onOpenChange={onOpenChange}
      title={`${provider.hasCredentials ? "Replace" : "Save"} ${provider.name} credentials`}
      description="Saved to a file only root can read, and never shown again."
      footer={
        <Button onClick={save} disabled={busy || !credentials.trim()} pending={busy}>
          Save
        </Button>
      }
    >
      <Field
        label="Credentials"
        htmlFor="dns-provider-credentials"
        hint={
          provider.hasCredentials
            ? "Replaces what is saved. Saved to a file only root can read, and never shown again."
            : "Saved to a file only root can read, and never shown again."
        }
      >
        <Textarea
          id="dns-provider-credentials"
          value={credentials}
          onChange={(e) => setCredentials(e.target.value)}
          rows={5}
          className="font-mono text-hint"
          placeholder={provider.credentials}
        />
      </Field>
    </Modal>
  )
}

/**
 * A DNS-01 dry run through the saved credentials: certbot writes the record,
 * the authority checks it, and nothing is saved.
 */
function DnsTestModal({
  provider,
  certbotBusy,
  onOpenChange,
  onStarted,
}: {
  provider: DNSProvider
  certbotBusy: boolean
  onOpenChange: (open: boolean) => void
  onStarted: (job: Job) => void
}) {
  const [domain, setDomain] = useState("")
  const [busy, setBusy] = useState(false)
  const start = async () => {
    setBusy(true)
    try {
      onStarted(
        await post<Job>(`/certificates/dns-credentials/${provider.key}/test`, {
          domain: domain.trim(),
        }),
      )
      onOpenChange(false)
    } catch (err) {
      notify.error("The test did not start", err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open
      onOpenChange={onOpenChange}
      title={`Test ${provider.name} credentials`}
      description="A dry run of a DNS challenge through the saved credentials. Nothing is saved."
      footer={
        <>
          {certbotBusy && (
            <span className="mr-auto text-hint text-muted-foreground">
              certbot is running. Wait for it to finish.
            </span>
          )}
          <Button onClick={start} disabled={busy || certbotBusy || !domain.trim()} pending={busy}>
            Run the test
          </Button>
        </>
      }
    >
      <Field
        label="Domain"
        htmlFor="dns-test-domain"
        hint={`A name in a zone ${provider.name} serves. certbot writes a challenge record there, the staging authority checks it, and the record is removed.`}
      >
        <Input
          id="dns-test-domain"
          value={domain}
          onChange={(e) => setDomain(e.target.value)}
          placeholder="example.com"
          className="font-mono text-xs"
        />
      </Field>
    </Modal>
  )
}

/**
 * The issuance form. A job comes back rather than a result: the ACME exchange
 * is watched in the console behind this dialog, which is why the dialog can
 * close.
 *
 * A test run is on by default, and the page offers the real run once one has
 * passed — the real limit is five failures an hour and it is easy to reach.
 * The test run is certbot's --dry-run: the whole exchange, nothing saved, so
 * a passing one leaves nothing behind for the real issuance to trip over.
 */
export function IssueDialog({
  open,
  onOpenChange,
  initialDomains,
  initialStaging = true,
  initialTarget,
  hasNginx,
  plugins,
  providers,
  directory,
  testAuthority = false,
  certs = [],
  certbotBusy,
  onStarted,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  initialDomains?: string
  initialStaging?: boolean
  /**
   * The lineage the form starts on: its names are then the lineage's new set,
   * which certbot's --cert-name replaces rather than adds to.
   */
  initialTarget?: string
  hasNginx: boolean
  /** certbot's lineages, which the form can add names to. */
  certs?: CertbotCert[]
  /** The authenticators the certbot that runs the job lists; absent while unknown. */
  plugins?: string[]
  providers: DNSProvider[]
  /** The ACME directory certbot orders from when it is not one of Let's Encrypt's. */
  directory?: string
  /** The configured directory signs test certificates, which browsers refuse. */
  testAuthority?: boolean
  /** A certbot run is on screen: this one would fail on its lock. */
  certbotBusy: boolean
  onStarted: (job: Job) => void
}) {
  return (
    <IssueDialogBody
      key={`${open}:${initialDomains ?? ""}:${initialStaging}:${initialTarget ?? ""}`}
      open={open}
      onOpenChange={onOpenChange}
      initialDomains={initialDomains}
      initialStaging={initialStaging}
      initialTarget={initialTarget}
      hasNginx={hasNginx}
      plugins={plugins}
      providers={providers}
      directory={directory}
      testAuthority={testAuthority}
      certs={certs}
      certbotBusy={certbotBusy}
      onStarted={onStarted}
    />
  )
}

function IssueDialogBody({
  open,
  onOpenChange,
  initialDomains,
  initialStaging,
  initialTarget,
  hasNginx,
  plugins,
  providers,
  directory,
  testAuthority,
  certs,
  certbotBusy,
  onStarted,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  initialDomains?: string
  initialStaging: boolean
  initialTarget?: string
  hasNginx: boolean
  plugins?: string[]
  providers: DNSProvider[]
  directory?: string
  testAuthority: boolean
  certs: CertbotCert[]
  certbotBusy: boolean
  onStarted: (job: Job) => void
}) {
  // A configured authority is rehearsed with itself — certbot's --dry-run
  // goes to Let's Encrypt's staging endpoint only when no other is named —
  // and Let's Encrypt's limits say nothing about it. Let's Encrypt's own
  // directories come without one: they are Let's Encrypt.
  const authority = directory ? authorityName(directory) : undefined
  const [domains, setDomains] = useState(initialDomains ?? "")
  // The account's contact fills the field until the operator types: certbot
  // asks for an email only to register an account, so with one on disk the
  // field may also stay empty.
  const account = usePoll<ACMEAccount>(
    (signal) => get("/certificates/account", undefined, signal),
    0,
    [],
    { enabled: open },
  )
  const [typedEmail, setEmail] = useState<string | null>(null)
  const email = typedEmail ?? account.data?.email ?? ""
  const hasAccount = account.data?.exists === true
  const sites = usePoll<VHost[]>((signal) => get("/proxy/vhosts", undefined, signal), 0, [], {
    enabled: open,
  })
  const siteNames = (v: VHost) => v.serverNames.filter((n) => SITE_NAME.test(n))
  const nginxSites = (sites.data ?? []).filter((v) => v.kind === "nginx" && siteNames(v).length)
  const [site, setSite] = useState("")
  // "" is a new certificate; a lineage's name adds the names to it.
  const [target, setTarget] = useState(initialTarget ?? "")
  const [newName, setNewName] = useState("")
  const certName = target || newName.trim()
  const [keyType, setKeyType] = useState<"" | "ecdsa" | "rsa">("")
  const [rsaKeySize, setRsaKeySize] = useState("2048")
  // A method the certbot that runs the job cannot use is drawn disabled with
  // the reason, rather than offered and refused by certbot a minute later.
  const unavailable: Record<string, string | undefined> = {
    nginx:
      plugins && !plugins.includes("nginx")
        ? "The certbot that runs here has no nginx plugin."
        : undefined,
    webroot:
      plugins && !plugins.includes("webroot")
        ? "The certbot that runs here lists no webroot plugin."
        : undefined,
    standalone:
      plugins && !plugins.includes("standalone")
        ? "The certbot that runs here lists no standalone plugin."
        : undefined,
    dns:
      providers.length > 0 && !providers.some((p) => p.installed)
        ? "No DNS plugin is installed for the certbot that runs here."
        : undefined,
  }
  // Through nginx where there is one to answer the challenge; standalone
  // binds port 80 itself and fails wherever nginx is already holding it.
  // The method last used starts the form, where it is still on offer.
  const [lastMethod, setLastMethod] = useViewState("proxy.certificates.issueMethod", "")
  const [pickedMethod, setMethod] = useState("")
  const remembered =
    ISSUE_METHODS.includes(lastMethod) &&
    !unavailable[lastMethod] &&
    (lastMethod !== "nginx" || hasNginx)
      ? lastMethod
      : ""
  const chosenMethod =
    pickedMethod || remembered || (hasNginx && !unavailable.nginx ? "nginx" : "webroot")
  const [webRoot, setWebRoot] = useState("/var/www/html")
  const [staging, setStaging] = useState(initialStaging)
  const [busy, setBusy] = useState(false)
  const [dnsProvider, setDnsProvider] = useState("")
  const [credentials, setCredentials] = useState("")
  const [dnsWait, setDnsWait] = useState("")

  const provider = providers.find((p) => p.key === dnsProvider)
  // A wildcard is only ever signed against a DNS challenge, so the form
  // switches to it the moment one is typed rather than after a failed
  // attempt — derived here, not synced, so the operator's own choice comes
  // back if the wildcard is removed again.
  const wantsWildcard = parseDomains(domains).some((d) => d.startsWith("*."))
  const method = wantsWildcard ? "dns" : chosenMethod
  // Route 53's plugin reads the environment and has neither a credentials
  // file nor a propagation flag.
  const fileProvider = method === "dns" && provider !== undefined && provider.key !== "route53"
  const wait = dnsWait.trim() === "" ? undefined : Number(dnsWait)
  const waitInvalid = wait !== undefined && (!Number.isInteger(wait) || wait < 1 || wait > 3600)

  // The token travels with the request and is saved by the job once it
  // starts, so a refused issuance leaves nothing on disk.
  const body = JSON.stringify({
    domains: parseDomains(domains),
    email: email.trim(),
    method,
    webRoot,
    staging,
    dnsProvider: method === "dns" ? dnsProvider : "",
    ...(fileProvider && wait !== undefined && { dnsWait: wait }),
    ...(fileProvider && credentials.trim() && { credentials }),
    ...(keyType && { keyType }),
    ...(keyType === "rsa" && { rsaKeySize: Number(rsaKeySize) }),
    ...(certName && { certName }),
  })

  // The command, from the server's own checks: what the job would run, or
  // why it would be refused. Debounced, since the answer only matters once
  // typing stops.
  const [preview, setPreview] = useState<{ plan?: IssuePreview; error?: string }>({})
  const previewReady =
    open && parseDomains(domains).length > 0 && (method !== "dns" || !!dnsProvider)
  useEffect(() => {
    const controller = new AbortController()
    const timer = setTimeout(
      () => {
        if (!previewReady) {
          setPreview({})
          return
        }
        post<IssuePreview>("/certificates/issue/preview", JSON.parse(body), {
          signal: controller.signal,
        })
          .then((plan) => setPreview({ plan }))
          .catch((err) => {
            if (!controller.signal.aborted) setPreview({ error: errorMessage(err) })
          })
      },
      previewReady ? 400 : 0,
    )
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [previewReady, body])
  const command = preview.plan ? preview.plan.args.map(shellWord).join(" ") : ""
  const { copied, copy } = useCopy()

  // What would fail the run, asked of this host before it: only the fields
  // the checks read, so typing an email does not probe the webroot again.
  const preflightBody = JSON.stringify({
    domains: parseDomains(domains),
    method,
    webRoot,
    staging,
    ...(certName && { certName }),
  })
  const [preflight, setPreflight] = useState<{
    key: string
    result?: IssuePreflight
    error?: string
  }>({ key: "" })
  const [recheck, setRecheck] = useState(0)
  const preflightReady = open && parseDomains(domains).length > 0
  useEffect(() => {
    const controller = new AbortController()
    const timer = setTimeout(
      () => {
        if (!preflightReady) {
          setPreflight({ key: "" })
          return
        }
        setPreflight((p) => ({ ...p, key: "" }))
        post<IssuePreflight>("/certificates/issue/preflight", JSON.parse(preflightBody), {
          signal: controller.signal,
        })
          .then((result) => setPreflight({ key: preflightBody, result }))
          .catch((err) => {
            if (!controller.signal.aborted) {
              setPreflight({ key: preflightBody, error: errorMessage(err) })
            }
          })
      },
      preflightReady ? 800 : 0,
    )
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [preflightReady, preflightBody, recheck])
  const checking = preflightReady && preflight.key !== preflightBody
  // The override belongs to the request it was given for: change a name or
  // the method and the new findings have to be read again.
  const [overridden, setOverridden] = useState("")
  const blocked =
    !staging && (checking || (preflight.result?.blocking === true && overridden !== preflightBody))

  const submit = async () => {
    setBusy(true)
    try {
      const job = await post<Job>("/certificates/issue", JSON.parse(body))
      setLastMethod(method)
      onStarted(job)
      onOpenChange(false)
    } catch (err) {
      notify.error("Could not start", err)
    } finally {
      setBusy(false)
    }
  }

  const needsCredentials = fileProvider && !provider.hasCredentials

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Issue a certificate"
      description={
        testAuthority
          ? `${authority ?? "Let's Encrypt's staging authority"} checks you control the domain, then signs a test certificate that browsers refuse. JD_ACME_DIRECTORY names a staging authority.`
          : authority
            ? `${authority} checks you control the domain, then signs a certificate. The renewal is automatic once the first one works.`
            : "Let's Encrypt proves you control the domain, then signs a certificate for ninety days. The renewal is automatic once the first one works."
      }
      footer={
        <>
          {certbotBusy && (
            <span className="mr-auto text-hint text-muted-foreground">
              certbot is running. Wait for it to finish.
            </span>
          )}
          <Button
            onClick={submit}
            disabled={
              busy ||
              certbotBusy ||
              Boolean(unavailable[method]) ||
              !domains.trim() ||
              (!email.trim() && !hasAccount) ||
              (method === "dns" && !dnsProvider) ||
              (needsCredentials && !credentials.trim()) ||
              (fileProvider && waitInvalid) ||
              blocked
            }
            pending={busy}
          >
            {staging ? "Run the test" : "Issue"}
          </Button>
        </>
      }
    >
      <div className="grid gap-4">
        <Field
          label="Domains"
          htmlFor="issue-domains"
          hint="Every name must already resolve to this server, or the challenge cannot reach it."
        >
          <Input
            id="issue-domains"
            value={domains}
            onChange={(e) => setDomains(e.target.value)}
            placeholder="app.example.com www.app.example.com"
            className="font-mono text-xs"
          />
        </Field>
        {nginxSites.length > 0 && (
          <Field label="From a site" hint="Fills the domains with the names the nginx site serves.">
            <Select
              value={site}
              onValueChange={(name) => {
                const picked = nginxSites.find((v) => v.name === name)
                if (!picked) return
                setSite(name)
                setDomains(siteNames(picked).join(" "))
              }}
            >
              <SelectTrigger className="w-full">
                <SelectValue placeholder="Choose a site" />
              </SelectTrigger>
              <SelectContent>
                {nginxSites.map((v) => (
                  <SelectItem key={v.name} value={v.name} hint={siteNames(v).join(" ")}>
                    {v.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        )}
        <Field
          label="Contact email"
          htmlFor="issue-email"
          hint={
            !hasAccount
              ? "Registered with the certificate authority account."
              : account.data?.email
                ? `The contact of certbot's account with ${hostOf(account.data.server)}. It may stay empty: the account is registered already.`
                : account.data?.error
                  ? `certbot has an account with ${hostOf(account.data.server)}, so this may stay empty. Its contact could not be read: ${account.data.error}`
                  : `certbot has an account with ${hostOf(account.data?.server ?? "")} with no contact, so this may stay empty.`
          }
        >
          <Input
            id="issue-email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="you@example.com"
          />
        </Field>
        <Field
          label="Certificate"
          htmlFor="issue-cert-name"
          hint={
            target
              ? `certbot issues ${target} again for exactly the names above, so keep the ones it has, and renews it this way from now on.`
              : "certbot's name for the new certificate, its folder under /etc/letsencrypt/live. The first domain when empty."
          }
        >
          <div className="grid gap-2">
            {certs.length > 0 && (
              <Select
                value={target || NEW_CERT}
                onValueChange={(name) => {
                  const cert = certs.find((c) => c.name === name)
                  setTarget(cert ? cert.name : "")
                  if (cert) {
                    setDomains([...new Set([...cert.domains, ...parseDomains(domains)])].join(" "))
                  }
                }}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={NEW_CERT}>A new certificate</SelectItem>
                  {certs.map((c) => (
                    <SelectItem key={c.name} value={c.name} hint={c.domains.join(" ")}>
                      Add these names to {c.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
            {!target && (
              <Input
                id="issue-cert-name"
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                placeholder={parseDomains(domains)[0]?.replace(/^\*\./, "") ?? "app.example.com"}
                className="font-mono text-xs"
              />
            )}
          </div>
        </Field>
        <Field
          label="Key"
          hint={
            keyType
              ? "A certificate these names already have with another key is issued again now, which counts as one of the five duplicates a week."
              : "certbot's default for a new certificate (ECDSA since certbot 2.0); one these names already have keeps its own."
          }
        >
          <div className="flex flex-wrap gap-2">
            <ToggleGroup
              type="single"
              value={keyType || "default"}
              onValueChange={(v) => v && setKeyType(v === "default" ? "" : (v as "ecdsa" | "rsa"))}
              variant="outline"
              size="sm"
              className="min-w-0 flex-1"
            >
              <ToggleGroupItem value="default" className="flex-1 text-hint">
                Default
              </ToggleGroupItem>
              <ToggleGroupItem value="ecdsa" className="flex-1 text-hint">
                ECDSA
              </ToggleGroupItem>
              <ToggleGroupItem value="rsa" className="flex-1 text-hint">
                RSA
              </ToggleGroupItem>
            </ToggleGroup>
            {keyType === "rsa" && (
              <Select value={rsaKeySize} onValueChange={setRsaKeySize}>
                <SelectTrigger size="sm" className="w-32" aria-label="RSA key size">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {["2048", "3072", "4096"].map((bits) => (
                    <SelectItem key={bits} value={bits}>
                      {bits} bits
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </div>
        </Field>
        <Field
          label="How to prove control"
          hint={
            method === "nginx"
              ? "certbot asks the running nginx to serve the challenge. The right answer when nginx is already serving these domains."
              : method === "webroot"
                ? "The challenge file is written into a folder your web server already serves."
                : method === "standalone"
                  ? hasNginx
                    ? "certbot runs its own server on port 80. It fails if nginx is holding that port, which on this host it probably is."
                    : "certbot runs its own server on port 80 for the length of the challenge."
                  : "certbot writes a record into your DNS through the provider's API. The only way to get a wildcard, and the only one that works when the domain sits behind a CDN."
          }
        >
          <ToggleGroup
            type="single"
            value={method}
            onValueChange={(v) => v && setMethod(v)}
            variant="outline"
            size="sm"
            className="w-full"
          >
            {hasNginx && (
              <ToggleGroupItem
                value="nginx"
                disabled={Boolean(unavailable.nginx)}
                className="flex-1 text-hint"
              >
                Through nginx
              </ToggleGroupItem>
            )}
            <ToggleGroupItem
              value="webroot"
              disabled={Boolean(unavailable.webroot)}
              className="flex-1 text-hint"
            >
              A folder
            </ToggleGroupItem>
            <ToggleGroupItem
              value="standalone"
              disabled={Boolean(unavailable.standalone)}
              className="flex-1 text-hint"
            >
              Standalone
            </ToggleGroupItem>
            <ToggleGroupItem
              value="dns"
              disabled={Boolean(unavailable.dns)}
              className="flex-1 text-hint"
            >
              DNS
            </ToggleGroupItem>
          </ToggleGroup>
        </Field>
        {Object.entries(unavailable).some(([m, why]) => why && (m !== "nginx" || hasNginx)) && (
          <ul className="-mt-2 space-y-1 text-hint text-muted-foreground">
            {Object.entries(unavailable).map(
              ([m, why]) =>
                why &&
                (m !== "nginx" || hasNginx) && (
                  <li key={m} className="break-words">
                    {why}
                  </li>
                ),
            )}
          </ul>
        )}
        {wantsWildcard && method !== "dns" && (
          <Notice tone="warning" icon={Warning} title="A wildcard needs the DNS challenge">
            Let&rsquo;s Encrypt will not sign <code className="font-mono">*.example.com</code>{" "}
            against an HTTP challenge, whatever the web server is doing.
          </Notice>
        )}

        {method === "dns" && (
          <Well plain className="space-y-3">
            <Field label="DNS provider">
              <Select value={dnsProvider} onValueChange={setDnsProvider}>
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="Where this domain's DNS lives" />
                </SelectTrigger>
                <SelectContent>
                  {providers.map((p) => (
                    <SelectItem key={p.key} value={p.key} disabled={!p.installed}>
                      {p.name}
                      {!p.installed && " · plugin not installed"}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            {fileProvider && (
              <Field
                label="Credentials"
                htmlFor="dns-credentials"
                hint={
                  provider.hasCredentials
                    ? `Saved to a file only root can read once the issuance starts, and never shown again. Leave empty to reuse what is already stored for ${provider.name}.`
                    : "Saved to a file only root can read once the issuance starts, and never shown again."
                }
              >
                <Textarea
                  id="dns-credentials"
                  value={credentials}
                  onChange={(e) => setCredentials(e.target.value)}
                  rows={4}
                  className="font-mono text-hint"
                  placeholder={provider.credentials}
                />
              </Field>
            )}
            {fileProvider && (
              <Field
                label="Propagation wait"
                htmlFor="dns-wait"
                hint={`Seconds certbot waits for the record to spread before ${authority ?? "Let's Encrypt"} looks; ${provider.defaultWait} when empty. A first-try failure is almost always this being too short.`}
                error={waitInvalid ? "A whole number of seconds from 1 to 3600." : undefined}
              >
                <Input
                  id="dns-wait"
                  type="number"
                  inputMode="numeric"
                  min={1}
                  max={3600}
                  value={dnsWait}
                  onChange={(e) => setDnsWait(e.target.value)}
                  placeholder={String(provider.defaultWait)}
                  className="font-mono text-xs"
                  aria-invalid={waitInvalid || undefined}
                />
              </Field>
            )}
          </Well>
        )}

        {method === "webroot" && (
          <Field
            label="Folder"
            htmlFor="issue-webroot"
            hint="Must already exist and be served at /.well-known/acme-challenge/ for these names."
          >
            <Input
              id="issue-webroot"
              value={webRoot}
              onChange={(e) => setWebRoot(e.target.value)}
              className="font-mono text-xs"
            />
          </Field>
        )}
        {preflightReady && (
          <PreflightChecks
            result={preflight.key === preflightBody ? preflight.result : undefined}
            error={preflight.key === preflightBody ? preflight.error : undefined}
            checking={checking}
            staging={staging}
            onRecheck={() => setRecheck((n) => n + 1)}
          />
        )}
        <OptionList>
          <OptionRow
            title="Test run first"
            hint={
              authority || testAuthority
                ? `certbot goes through the whole exchange with ${authority ?? "Let's Encrypt's staging authority"} and saves nothing, so a mistake is found before anything is written.`
                : "certbot goes through the whole exchange with Let's Encrypt's staging authority and saves nothing. The real limit is five failures an hour and it is easy to reach, so this is the right first attempt."
            }
            checked={staging}
            onCheckedChange={setStaging}
          />
          {!staging && !checking && preflight.result?.blocking && (
            <OptionRow
              title="Issue anyway"
              hint="A check above expects the authority to refuse. A refused real run counts as a failed validation."
              tone="danger"
              checked={overridden === preflightBody}
              onCheckedChange={(on) => setOverridden(on ? preflightBody : "")}
            />
          )}
        </OptionList>
        {!staging && testAuthority && (
          <Notice tone="warning" icon={Warning} title="This issues a test certificate">
            JD_ACME_DIRECTORY names a staging authority, and browsers refuse what it signs.
          </Notice>
        )}
        {!staging && !authority && !testAuthority && (
          <Notice tone="warning" icon={Warning} title="This counts against the rate limit">
            Five failed attempts an hour for the same set of names, and five duplicate certificates
            a week. Get a test run to pass first.
          </Notice>
        )}
        {!staging && preview.plan?.replacesKeyOf && (
          <Notice
            tone="warning"
            icon={Warning}
            title={`This issues ${preview.plan.replacesKeyOf} again now`}
          >
            Its key is of another type or size, and certbot would otherwise keep it until it is due.
          </Notice>
        )}
        {(command || preview.error) && (
          <Field
            label="Command"
            hint="What the job runs. A DNS token appears only as the path of the file certbot reads it from."
            error={preview.error}
          >
            {command && (
              <Well className="flex items-start gap-2 p-2">
                <code className="min-w-0 flex-1 break-all">{command}</code>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  aria-label="Copy the command"
                  onClick={() => copy(command)}
                >
                  {copied ? <Check /> : <Copy />}
                </Button>
              </Well>
            )}
          </Field>
        )}
      </div>
    </Modal>
  )
}

/**
 * The preflight as a checklist: what failed or needs reading, one row each,
 * and the rate limits a real run counts against as meters.
 */
function PreflightChecks({
  result,
  error,
  checking,
  staging,
  onRecheck,
}: {
  result?: IssuePreflight
  error?: string
  checking: boolean
  staging: boolean
  onRecheck: () => void
}) {
  const open = result?.checks.filter((c) => c.level !== "ok") ?? []
  const passed = (result?.checks.length ?? 0) - open.length
  const limits = result?.limits
  return (
    <Field
      label="Before the run"
      hint={
        checking
          ? "Checking DNS, CAA and the challenge path from this host…"
          : result && `${passed} of ${result.checks.length} checks passed.`
      }
      error={error}
      trailing={
        <Button
          variant="ghost"
          size="xs"
          onClick={onRecheck}
          disabled={checking}
          pending={checking}
        >
          <RefreshClockwise />
          Re-check
        </Button>
      }
    >
      {result && (
        <div className="grid gap-3">
          <FindingList
            findings={open.map((c) => ({
              id: c.id,
              level: c.level === "ok" ? "notice" : c.level,
              title: c.title,
              detail: c.detail,
              advice: c.advice,
              meta: c.name,
            }))}
            emptyLabel="Every check passed"
          />
          {limits && limits.applies && (
            <div className="grid gap-2">
              <RateMeter label="Certificates for these names this week" count={limits.duplicates} />
              {limits.registered.map((r) => (
                <RateMeter
                  key={r.domain}
                  label={`Certificates for ${r.domain} this week`}
                  count={r}
                />
              ))}
              {limits.failures.map((f) => (
                <RateMeter
                  key={f.domain}
                  label={`Failed runs for ${f.domain} this hour`}
                  count={f}
                />
              ))}
              <p className="text-hint text-muted-foreground">
                {staging
                  ? "A test run counts against the staging authority's limits, not these. "
                  : ""}
                {limits.scope}
              </p>
            </div>
          )}
        </div>
      )}
    </Field>
  )
}

function RateMeter({
  label,
  count,
}: {
  label: string
  count: IssuePreflight["limits"]["duplicates"]
}) {
  const pct = (count.used / count.limit) * 100
  return (
    <div className="grid gap-1">
      <div className="flex items-baseline justify-between gap-3 text-xs">
        <span className="min-w-0 truncate text-muted-foreground">{label}</span>
        <span className="numeric shrink-0">
          {count.used} of {count.limit}
        </span>
      </div>
      <Meter
        value={pct}
        tone={count.used >= count.limit ? "danger" : pct >= 80 ? "warning" : "default"}
        label={label}
      />
    </div>
  )
}

const ISSUE_METHODS = ["nginx", "webroot", "standalone", "dns"]
/** The certificate select's "a new one": no lineage name can be it. */
const NEW_CERT = " new"
/** A server_name a certificate can carry: not "_", a regex or a bare host. */
const SITE_NAME = /^(\*\.)?[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$/

function hostOf(url: string) {
  try {
    return new URL(url).host
  } catch {
    return url
  }
}

/** An argv word as a shell reads it back: quoted only where it has to be. */
function shellWord(word: string) {
  return /^[A-Za-z0-9_@%+=:,./-]+$/.test(word) ? word : `'${word.replaceAll("'", `'\\''`)}'`
}

/**
 * The certbot-is-missing state, shared by the page's two places that need it.
 * An admin can install it with the host's package manager from here; a host
 * without one says why in the refusal.
 */
export function CertbotMissing({ onInstall }: { onInstall?: (job: Job) => void }) {
  const [busy, setBusy] = useState(false)
  const install = async () => {
    if (!onInstall) return
    setBusy(true)
    try {
      onInstall(await post<Job>("/certificates/certbot/install", { plugin: "" }))
    } catch (err) {
      notify.error("Could not install certbot", err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <EmptyState
      icon={ShieldOff}
      title="certbot is not installed"
      description="Install it to issue and renew Let's Encrypt certificates from here. A certificate placed on disk by any other means still shows up in the list below."
      action={
        onInstall && (
          <Button size="sm" variant="outline" onClick={install} pending={busy}>
            <Download className="size-3.5" />
            Install certbot
          </Button>
        )
      }
      className="mt-2"
    />
  )
}
