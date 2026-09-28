"use client"

import { useState } from "react"
import {
  Clock,
  Key,
  Logs,
  RefreshClockwise,
  ShieldCheck,
  ShieldOff,
  Trash,
  Warning,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, post } from "@/lib/api"
import {
  authorityName,
  certbotRunning,
  lineageActivity,
  parseDomains,
  renewalMethod,
} from "@/lib/certificates"
import type { CertbotCert, CertbotState, DNSProvider, Job } from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { Field, OptionList, OptionRow } from "@/components/form"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ProductLogo, ProductLogos } from "@/components/product-logo"
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
export function CertbotLineages({
  state,
  admin,
  busy,
  job,
  onRenew,
  onReplace,
  onRevoke,
  onShowLog,
}: {
  state: CertbotState
  admin: boolean
  busy: string
  job: Job | null
  onRenew: (name: string, dryRun: boolean, force?: boolean) => void
  /** Opens the real issuance for a test certificate's names; absent where none can be issued. */
  onReplace?: (domains: string) => void
  onRevoke: (name: string) => void
  /** Opens the renewal schedule's log. */
  onShowLog: () => void
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
            revoke,
          ]
        : [revoke]
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
        run: () =>
          confirm({
            title: `Force renewal of ${name}`,
            confirmLabel: "Renew now",
            description: (
              <p>
                certbot normally refuses to renew a certificate that is not due. Forcing it spends
                one of the five duplicate certificates Let&rsquo;s Encrypt allows per week for this
                set of names.
              </p>
            ),
            action: async () => onRenew(name, false, true),
          }),
      },
      revoke,
    ]
  }
  return (
    <>
      {admin && running && (
        <p className="pt-3 pb-1 text-hint text-muted-foreground">
          certbot is running. Its other actions wait until it finishes.
        </p>
      )}
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
      {method.detail && (
        <Tag mono={method.mono} className="max-w-full break-all">
          {method.detail}
        </Tag>
      )}
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
 * The DNS plugins certbot can drive from here, and whether each can: the
 * plugin installed, a token saved. The token is never shown — that a
 * credential for a whole DNS zone exists on disk is the whole of what the
 * page says about it, and the reason it can be removed from here too.
 */
export function DnsProvidersPanel({
  providers,
  admin,
  onChanged,
}: {
  providers: DNSProvider[]
  admin: boolean
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const relevant = providers.filter((p) => p.installed || p.hasCredentials)
  return (
    <Panel plain>
      <PanelHeader title="DNS challenge providers" />
      <PanelBody flush>
        {relevant.length === 0 ? (
          <p className="py-2 text-body text-muted-foreground">
            No certbot DNS plugin is installed. A wildcard, or a domain behind a CDN, needs one:
            install <code className="font-mono">python3-certbot-dns-&lt;provider&gt;</code> and it
            appears here.
          </p>
        ) : (
          <ul className="divide-y divide-hairline">
            {relevant.map((p) => (
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
                {admin && p.hasCredentials && (
                  <VerbActions
                    dim
                    verbs={[
                      {
                        key: "remove",
                        label: "Remove credentials",
                        icon: Trash,
                        danger: true,
                        run: () =>
                          confirm({
                            title: `Remove ${p.name} credentials`,
                            confirmLabel: "Remove",
                            description: (
                              <p>
                                The saved token is deleted from disk. Any certificate issued through{" "}
                                {p.name} stops renewing until credentials are saved again.
                              </p>
                            ),
                            action: async () => {
                              await del(`/certificates/dns-credentials/${p.key}`)
                              onChanged()
                            },
                          }),
                      },
                    ]}
                  />
                )}
                <div className="flex w-full flex-wrap items-center gap-x-4 gap-y-2">
                  <Status
                    verdict={p.installed ? "ok" : "warning"}
                    label={p.installed ? "plugin installed" : "plugin missing"}
                  />
                  <Status
                    tone={p.hasCredentials ? "running" : "stopped"}
                    label={p.hasCredentials ? "credentials saved" : "no credentials"}
                  />
                </div>
              </li>
            ))}
          </ul>
        )}
      </PanelBody>
      {dialog}
    </Panel>
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
  hasNginx,
  providers,
  directory,
  testAuthority = false,
  certbotBusy,
  onStarted,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  initialDomains?: string
  initialStaging?: boolean
  hasNginx: boolean
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
      key={`${open}:${initialDomains ?? ""}:${initialStaging}`}
      open={open}
      onOpenChange={onOpenChange}
      initialDomains={initialDomains}
      initialStaging={initialStaging}
      hasNginx={hasNginx}
      providers={providers}
      directory={directory}
      testAuthority={testAuthority}
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
  hasNginx,
  providers,
  directory,
  testAuthority,
  certbotBusy,
  onStarted,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  initialDomains?: string
  initialStaging: boolean
  hasNginx: boolean
  providers: DNSProvider[]
  directory?: string
  testAuthority: boolean
  certbotBusy: boolean
  onStarted: (job: Job) => void
}) {
  // A configured authority is rehearsed with itself — certbot's --dry-run
  // goes to Let's Encrypt's staging endpoint only when no other is named —
  // and Let's Encrypt's limits say nothing about it. Let's Encrypt's own
  // directories come without one: they are Let's Encrypt.
  const authority = directory ? authorityName(directory) : undefined
  const [domains, setDomains] = useState(initialDomains ?? "")
  const [email, setEmail] = useState("")
  // Through nginx where there is one to answer the challenge; standalone
  // binds port 80 itself and fails wherever nginx is already holding it.
  const [chosenMethod, setMethod] = useState(hasNginx ? "nginx" : "webroot")
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

  const submit = async () => {
    setBusy(true)
    try {
      // The token travels with the request and is saved by the job once it
      // starts, so a refused issuance leaves nothing on disk.
      const job = await post<Job>("/certificates/issue", {
        domains: parseDomains(domains),
        email,
        method,
        webRoot,
        staging,
        dnsProvider: method === "dns" ? dnsProvider : "",
        ...(fileProvider && wait !== undefined && { dnsWait: wait }),
        ...(fileProvider && credentials.trim() && { credentials }),
      })
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
              !domains.trim() ||
              !email.trim() ||
              (method === "dns" && !dnsProvider) ||
              (needsCredentials && !credentials.trim()) ||
              (fileProvider && waitInvalid)
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
        <Field
          label="Contact email"
          htmlFor="issue-email"
          hint="Registered with the certificate authority account."
        >
          <Input
            id="issue-email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="you@example.com"
          />
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
              <ToggleGroupItem value="nginx" className="flex-1 text-hint">
                Through nginx
              </ToggleGroupItem>
            )}
            <ToggleGroupItem value="webroot" className="flex-1 text-hint">
              A folder
            </ToggleGroupItem>
            <ToggleGroupItem value="standalone" className="flex-1 text-hint">
              Standalone
            </ToggleGroupItem>
            <ToggleGroupItem value="dns" className="flex-1 text-hint">
              DNS
            </ToggleGroupItem>
          </ToggleGroup>
        </Field>
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
                    <SelectItem key={p.key} value={p.key}>
                      {p.name}
                      {!p.installed && " · plugin not installed"}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            {provider && !provider.installed && (
              <Notice tone="warning" icon={Warning} title="The plugin is missing">
                Install <code className="font-mono">python3-certbot-{provider.plugin}</code> (or{" "}
                <code className="font-mono">certbot plugin install certbot-{provider.plugin}</code>{" "}
                on a snap install) before issuing.
              </Notice>
            )}
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
          <Field label="Folder" htmlFor="issue-webroot">
            <Input
              id="issue-webroot"
              value={webRoot}
              onChange={(e) => setWebRoot(e.target.value)}
              className="font-mono text-xs"
            />
          </Field>
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
      </div>
    </Modal>
  )
}

/** The certbot-is-missing state, shared by the page's two places that need it. */
export function CertbotMissing() {
  return (
    <EmptyState
      icon={ShieldOff}
      title="certbot is not installed"
      description="Install it to issue and renew Let's Encrypt certificates from here. A certificate placed on disk by any other means still shows up in the list below."
      className="mt-2"
    />
  )
}
