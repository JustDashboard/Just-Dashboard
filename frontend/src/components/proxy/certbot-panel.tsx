"use client"

import { useState } from "react"
import {
  Clock,
  Key,
  RefreshClockwise,
  ShieldCheck,
  ShieldOff,
  Trash,
  Warning,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, post } from "@/lib/api"
import type { CertbotCert, CertbotState, DNSProvider, Job } from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { Field, FormNote, OptionList, OptionRow } from "@/components/form"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ProductLogo, ProductLogos } from "@/components/product-logo"
import { Row, RowList, ROW_BLEED } from "@/components/row-list"
import { CertLife } from "@/components/proxy/expiry-status"
import { dnsProviderProduct } from "@/components/proxy/marks"
import { EmptyState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { VerbActions, type Verb } from "@/components/verbs"
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
 */
export function CertbotLineages({
  state,
  admin,
  busy,
  onRenew,
  onRevoke,
}: {
  state: CertbotState
  admin: boolean
  busy: string
  onRenew: (name: string, dryRun: boolean, force?: boolean) => void
  onRevoke: (name: string) => void
}) {
  const { confirm, dialog } = useConfirm()
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
  const verbsFor = (name: string): Verb[] => [
    {
      key: "renew",
      label: "Renew",
      icon: RefreshClockwise,
      inline: true,
      disabled: busy === name,
      run: () => onRenew(name, false),
    },
    {
      key: "dry-run",
      label: "Dry run",
      icon: ShieldCheck,
      disabled: busy === name,
      run: () => onRenew(name, true),
    },
    {
      key: "force",
      label: "Force renewal",
      icon: Warning,
      disabled: busy === name,
      run: () =>
        confirm({
          title: `Force renewal of ${name}`,
          confirmLabel: "Renew now",
          description: (
            <p>
              certbot normally refuses to renew a certificate that is not due. Forcing it spends one
              of the five duplicate certificates Let&rsquo;s Encrypt allows per week for this set of
              names.
            </p>
          ),
          action: async () => onRenew(name, false, true),
        }),
    },
    {
      key: "revoke",
      label: "Revoke and delete",
      icon: Trash,
      danger: true,
      disabled: busy === name,
      run: () => onRevoke(name),
    },
  ]
  return (
    <>
      {/* Rows rather than a table: three columns naming themselves is a
          header spent on nothing, and each lineage is what it is — a
          Let's Encrypt certificate, drawn as one. */}
      <RowList className="animate-rise">
        {state.certs.map((cert) => (
          <Row
            key={cert.name}
            leading={<ProductLogo id="lets-encrypt" size="sm" />}
            title={cert.name}
            subtitle={cert.domains.join(", ")}
            trailing={
              <>
                <span className="flex flex-col items-end gap-1">
                  <Status
                    verdict={!cert.valid ? "critical" : cert.daysLeft <= 14 ? "warning" : "ok"}
                    label={
                      busy === cert.name
                        ? "Renewing…"
                        : cert.valid
                          ? `${cert.daysLeft}d left`
                          : "expired"
                    }
                  />
                  <LineageLife cert={cert} />
                </span>
                {admin && (
                  <VerbActions
                    dim
                    verbs={verbsFor(cert.name)}
                    menuLabel={`More actions for ${cert.name}`}
                  />
                )}
              </>
            }
            className="py-2.5"
          />
        ))}
      </RowList>
      {dialog}
    </>
  )
}

/**
 * certbot's lineages carry days left and an expiry but no issue date, and
 * every one of them is a ninety-day certificate: the meter is drawn against
 * that term.
 */
function LineageLife({ cert }: { cert: CertbotCert }) {
  return (
    <CertLife
      cert={{
        notBefore: new Date(new Date(cert.expiry).getTime() - 90 * 86_400_000).toISOString(),
        notAfter: cert.expiry,
        daysLeft: cert.daysLeft,
        expired: !cert.valid,
        expiring: cert.valid && cert.daysLeft <= 30,
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
          No certbot timer and no cron entry was found. Let&rsquo;s Encrypt certificates last ninety
          days, so without a schedule every one of these expires — which is what has happened to
          almost every expired certificate anybody has ever had.
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
                  "group flex min-w-0 items-center gap-3 py-2.5 transition-colors hover:bg-row-hover",
                  ROW_BLEED,
                )}
              >
                {/* The provider as itself where it has a mark — Cloudflare,
                    Route 53 as AWS — and a key for the ones that do not. */}
                <ProductLogo id={dnsProviderProduct(p.key)} size="sm" fallback={Key} />
                <div className="min-w-0 flex-1">
                  <div className="flex min-w-0 items-baseline gap-2">
                    <span className="text-body font-medium">{p.name}</span>
                    <Tag mono>{p.plugin}</Tag>
                  </div>
                  <p className="text-hint text-muted-foreground">
                    {p.key === "route53"
                      ? "Reads the saved profile, or the machine's IAM role."
                      : `Waits ${p.defaultWait}s for the record to propagate.`}
                  </p>
                </div>
                <Status
                  verdict={p.installed ? "ok" : "warning"}
                  label={p.installed ? "plugin installed" : "plugin missing"}
                />
                <Status
                  tone={p.hasCredentials ? "running" : "stopped"}
                  label={p.hasCredentials ? "credentials saved" : "no credentials"}
                />
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
 * Staging is on by default, and the page offers the real run once a staging
 * run has passed — the real limit is five failures an hour and it is easy to
 * reach.
 */
export function IssueDialog({
  open,
  onOpenChange,
  initialDomains,
  initialStaging = true,
  hasNginx,
  providers,
  onStarted,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  initialDomains?: string
  initialStaging?: boolean
  hasNginx: boolean
  providers: DNSProvider[]
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
  onStarted,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  initialDomains?: string
  initialStaging: boolean
  hasNginx: boolean
  providers: DNSProvider[]
  onStarted: (job: Job) => void
}) {
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

  const provider = providers.find((p) => p.key === dnsProvider)
  // A wildcard is only ever signed against a DNS challenge, so the form
  // switches to it the moment one is typed rather than after a failed
  // attempt — derived here, not synced, so the operator's own choice comes
  // back if the wildcard is removed again.
  const wantsWildcard = domains.split(/[\s,]+/).some((d) => d.startsWith("*."))
  const method = wantsWildcard ? "dns" : chosenMethod

  const submit = async () => {
    setBusy(true)
    try {
      if (method === "dns" && credentials.trim() && provider?.key !== "route53") {
        await post("/certificates/dns-credentials", { provider: dnsProvider, credentials })
      }
      const job = await post<Job>("/certificates/issue", {
        domains: domains.split(/[\s,]+/).filter(Boolean),
        email,
        method,
        webRoot,
        staging,
        dnsProvider: method === "dns" ? dnsProvider : "",
      })
      onStarted(job)
      onOpenChange(false)
    } catch (err) {
      notify.error("Could not start", err)
    } finally {
      setBusy(false)
    }
  }

  const needsCredentials =
    method === "dns" && provider && provider.key !== "route53" && !provider.hasCredentials

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Issue a certificate"
      description="Let's Encrypt proves you control the domain, then signs a certificate for ninety days. The renewal is automatic once the first one works."
      footer={
        <Button
          onClick={submit}
          disabled={
            busy ||
            !domains.trim() ||
            !email.trim() ||
            (method === "dns" && !dnsProvider) ||
            (needsCredentials && !credentials.trim())
          }
          pending={busy}
        >
          {staging ? "Run the test" : "Issue"}
        </Button>
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
          hint="Where expiry warnings go if renewal ever stops working."
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
            {provider && provider.key !== "route53" && (
              <Field
                label="Credentials"
                htmlFor="dns-credentials"
                hint={
                  provider.hasCredentials
                    ? `Saved to a file only root can read, and never shown again. Leave empty to reuse what is already stored for ${provider.name}.`
                    : "Saved to a file only root can read, and never shown again."
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
            {provider && (
              <FormNote>
                certbot waits {provider.defaultWait}s for the record to propagate before asking
                Let&rsquo;s Encrypt to look. A challenge that fails on the first try is almost
                always that wait being too short rather than a wrong token.
              </FormNote>
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
            hint="Issues from Let's Encrypt's staging authority: not trusted by browsers, and not rate-limited. The real limit is five failures an hour and it is easy to reach, so this is the right first attempt."
            checked={staging}
            onCheckedChange={setStaging}
          />
        </OptionList>
        {!staging && (
          <Notice tone="warning" icon={Warning} title="This counts against the rate limit">
            Five failed attempts an hour for the same set of names, and five duplicate certificates
            a week. Get a staging run to pass first.
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
