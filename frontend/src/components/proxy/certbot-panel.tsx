"use client"

import { useEffect, useState } from "react"
import { Clock, RefreshClockwise, ShieldCheck, ShieldOff, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post, ApiError } from "@/lib/api"
import type { CertbotState, DNSProvider, Job } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { ImportDialog } from "@/components/proxy/import-dialog"
import { JobConsole, RecentJobs, useJobConsole } from "@/components/job-console"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel, Notice, Spinner } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Modal } from "@/components/modal"
import { RowActions } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

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
 * The renewal schedule gets a line of its own because it is the real story
 * behind almost every expired certificate: not a forgotten renewal, a renewal
 * timer that stopped months ago and told nobody.
 */
/** Busy sentinel for "renew everything", which has no certificate name. */
const ALL_CERTS = "*"

export function CertbotPanel({ onChanged }: { onChanged?: () => void }) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const { data, error, loading, refresh } = usePoll<CertbotState>(
    (signal) => get("/certificates/certbot", undefined, signal),
    300000,
  )
  const [busy, setBusy] = useState("")
  const admin = can("system.admin")
  const console_ = useJobConsole()

  // The list is only right once certbot has finished writing, so it is
  // refreshed when the job ends rather than when it starts.
  const lastStatus = console_.job?.status
  useEffect(() => {
    if (lastStatus === "succeeded") {
      refresh()
      onChanged?.()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [lastStatus])

  const unavailable = error instanceof ApiError && error.code === "certbot_unavailable"
  if (loading) return <LoadingPanel />
  if (unavailable) {
    return (
      <EmptyState
        icon={ShieldOff}
        title="certbot is not installed"
        description="Install it to issue and renew Let's Encrypt certificates from here. A certificate placed on disk by any other means still shows up in the list above."
      />
    )
  }
  if (error) return <ErrorState error={error} />
  if (!data) return null

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
      console_.attach(job)
    } catch (err) {
      notify.error(dryRun ? "Dry run refused" : "Renewal refused", err)
    } finally {
      setBusy("")
    }
  }

  return (
    <>
      <div className="flex min-w-0 flex-col gap-4">
        <JobConsole
          job={console_.job}
          lines={console_.lines}
          onDismiss={console_.dismiss}
          onCancel={console_.cancel}
        />

        {!data.autoRenew && data.certs.length > 0 && (
          <Notice tone="warning" icon={Clock} title="Nothing is scheduled to renew these">
            No certbot timer and no cron entry was found. Let&rsquo;s Encrypt certificates last
            ninety days, so without a schedule every one of these expires — which is what has
            happened to almost every expired certificate anybody has ever had.
          </Notice>
        )}

        <Panel>
          <PanelHeader
            title="certbot"
            actions={
              admin && (
                <>
                  <RecentJobs kinds={["certbot."]} onOpen={console_.open} />
                  <ImportDialog onDone={() => onChanged?.()} />
                  <IssueDialog onStarted={console_.attach} />
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={busy !== ""}
                    onClick={() => renew(ALL_CERTS, false)}
                  >
                    {busy === ALL_CERTS ? (
                      <Spinner className="size-4" />
                    ) : (
                      <RefreshClockwise className="size-3.5" />
                    )}
                    Renew all due
                  </Button>
                </>
              )
            }
          />
          <PanelBody flush>
            {data.certs.length === 0 ? (
              <EmptyState icon={ShieldCheck} title="certbot manages no certificates yet" />
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-full">Name</TableHead>
                    <TableHead>Domains</TableHead>
                    <TableHead>Expires</TableHead>
                    <TableHead className="w-px" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.certs.map((cert) => (
                    <TableRow key={cert.name} className="group">
                      <TableCell className="text-body font-medium">{cert.name}</TableCell>
                      <TableCell className="max-w-xs truncate text-muted-foreground">
                        {cert.domains.join(", ")}
                      </TableCell>
                      <TableCell>
                        <Status
                          verdict={
                            !cert.valid ? "critical" : cert.daysLeft <= 14 ? "warning" : "ok"
                          }
                          label={cert.valid ? `${cert.daysLeft}d left` : "expired"}
                        />
                      </TableCell>
                      <TableCell>
                        {admin && (
                          <RowActions className="gap-1">
                            <Button
                              size="xs"
                              variant="ghost"
                              disabled={busy === cert.name}
                              onClick={() => renew(cert.name, true)}
                              title="Run the whole exchange against the staging authority, changing nothing"
                            >
                              Dry run
                            </Button>
                            <Button
                              size="xs"
                              variant="ghost"
                              onClick={() => renew(cert.name, false)}
                              pending={busy === cert.name}
                            >
                              Renew
                            </Button>
                            <Button
                              size="xs"
                              variant="ghost"
                              disabled={busy === cert.name}
                              title="Renew even though it is not due. Spends one of the five duplicate certificates Let's Encrypt allows per week."
                              onClick={() =>
                                confirm({
                                  title: `Force renewal of ${cert.name}`,
                                  confirmLabel: "Renew now",
                                  description: (
                                    <p>
                                      certbot normally refuses to renew a certificate that is not
                                      due. Forcing it spends one of the five duplicate certificates
                                      Let&rsquo;s Encrypt allows per week for this set of names.
                                    </p>
                                  ),
                                  action: async () => {
                                    await renew(cert.name, false, true)
                                  },
                                })
                              }
                            >
                              Force
                            </Button>
                            <Button
                              size="xs"
                              variant="ghost"
                              className="text-destructive"
                              onClick={() =>
                                confirm({
                                  title: `Revoke ${cert.name}`,
                                  phrase: `revoke ${cert.name}`,
                                  confirmLabel: "Revoke and delete",
                                  description: (
                                    <p className="text-destructive">
                                      The authority publishes that this certificate is no longer to
                                      be trusted and the files are deleted. There is no undo, and
                                      every client holding it starts refusing the site.
                                    </p>
                                  ),
                                  action: async (c) => {
                                    const job = await post<Job>(
                                      "/certificates/revoke",
                                      { name: cert.name },
                                      { confirm: c },
                                    )
                                    console_.attach(job)
                                  },
                                })
                              }
                            >
                              Revoke
                            </Button>
                          </RowActions>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </PanelBody>
        </Panel>
      </div>
      {dialog}
    </>
  )
}

function IssueDialog({ onStarted }: { onStarted: (job: Job) => void }) {
  const [open, setOpen] = useState(false)
  const [domains, setDomains] = useState("")
  const [email, setEmail] = useState("")
  const [method, setMethod] = useState("nginx")
  const [webRoot, setWebRoot] = useState("/var/www/html")
  const [staging, setStaging] = useState(true)
  const [busy, setBusy] = useState(false)
  const [dnsProvider, setDnsProvider] = useState("")
  const [credentials, setCredentials] = useState("")

  const providers = usePoll<DNSProvider[]>(
    (signal) => get("/certificates/dns-providers", undefined, signal),
    0,
    [],
    { enabled: open },
  )
  const provider = providers.data?.find((p) => p.key === dnsProvider)
  // A wildcard is only ever signed against a DNS challenge, so the form says
  // so the moment one is typed rather than after a failed attempt.
  const wantsWildcard = domains.split(/[\s,]+/).some((d) => d.startsWith("*."))

  const submit = async () => {
    setBusy(true)
    try {
      if (method === "dns" && credentials.trim() && provider?.key !== "route53") {
        await post("/certificates/dns-credentials", {
          provider: dnsProvider,
          credentials,
        })
      }
      // The answer is a job, not a result: the ACME exchange is watched in
      // the console behind this dialog, which is why the dialog can close.
      const job = await post<Job>("/certificates/issue", {
        domains: domains.split(/[\s,]+/).filter(Boolean),
        email,
        method,
        webRoot,
        staging,
        dnsProvider: method === "dns" ? dnsProvider : "",
      })
      onStarted(job)
      setOpen(false)
    } catch (err) {
      notify.error("Could not start", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)}>
        Issue certificate
      </Button>
      <Modal
        open={open}
        onOpenChange={setOpen}
        title="Issue a certificate"
        description="Let&rsquo;s Encrypt proves you control the domain, then signs a certificate for ninety
              days. The renewal is automatic once the first one works."
        footer={
          <>
            <Button
              onClick={submit}
              disabled={
                busy || !domains.trim() || !email.trim() || (method === "dns" && !dnsProvider)
              }
              pending={busy}
            >
              {staging ? "Run the test" : "Issue"}
            </Button>
          </>
        }
      >
        <div className="grid gap-3">
          <div className="space-y-1.5">
            <Label htmlFor="issue-domains">Domains</Label>
            <Input
              id="issue-domains"
              value={domains}
              onChange={(e) => setDomains(e.target.value)}
              placeholder="app.example.com www.app.example.com"
              className="font-mono text-xs"
            />
            <p className="text-hint text-muted-foreground">
              Every name must already resolve to this server, or the challenge cannot reach it.
            </p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="issue-email">Contact email</Label>
            <Input
              id="issue-email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="you@example.com"
            />
            <p className="text-hint text-muted-foreground">
              Where expiry warnings go if renewal ever stops working.
            </p>
          </div>
          <div className="space-y-1.5">
            <Label>How to prove control</Label>
            <ToggleGroup
              type="single"
              value={method}
              onValueChange={(v) => v && setMethod(v)}
              variant="outline"
              size="sm"
              className="w-full"
            >
              <ToggleGroupItem value="nginx" className="flex-1 text-hint">
                Through nginx
              </ToggleGroupItem>
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
            <p className="text-hint leading-relaxed text-muted-foreground">
              {method === "nginx" &&
                "certbot asks the running nginx to serve the challenge. The right answer when nginx is already serving these domains."}
              {method === "webroot" &&
                "The challenge file is written into a folder your web server already serves."}
              {method === "standalone" &&
                "certbot runs its own server on port 80. It fails if nginx is holding that port, which on this host it probably is."}
              {method === "dns" &&
                "certbot writes a record into your DNS through the provider's API. The only way to get a wildcard, and the only one that works when the domain sits behind a CDN."}
            </p>
          </div>
          {wantsWildcard && method !== "dns" && (
            <Notice tone="warning" icon={Warning} title="A wildcard needs the DNS challenge">
              Let&rsquo;s Encrypt will not sign <code className="font-mono">*.example.com</code>{" "}
              against an HTTP challenge, whatever the web server is doing. Switch the method to DNS.
            </Notice>
          )}

          {method === "dns" && (
            <Well plain className="space-y-3">
              <div className="space-y-1.5">
                <Label>DNS provider</Label>
                <Select value={dnsProvider} onValueChange={setDnsProvider}>
                  <SelectTrigger className="w-full">
                    <SelectValue placeholder="Where this domain's DNS lives" />
                  </SelectTrigger>
                  <SelectContent>
                    {providers.data?.map((p) => (
                      <SelectItem key={p.key} value={p.key}>
                        {p.name}
                        {!p.installed && " · plugin not installed"}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              {provider && !provider.installed && (
                <Notice tone="warning" icon={Warning} title="The plugin is missing">
                  Install <code className="font-mono">python3-certbot-{provider.plugin}</code> (or{" "}
                  <code className="font-mono">
                    certbot plugin install certbot-{provider.plugin}
                  </code>{" "}
                  on a snap install) before issuing.
                </Notice>
              )}
              {provider && provider.key !== "route53" && (
                <div className="space-y-1.5">
                  <Label htmlFor="dns-credentials">Credentials</Label>
                  <Textarea
                    id="dns-credentials"
                    value={credentials}
                    onChange={(e) => setCredentials(e.target.value)}
                    rows={4}
                    className="font-mono text-hint"
                    placeholder={provider.credentials}
                  />
                  <p className="text-hint leading-relaxed text-muted-foreground">
                    Saved to a file only root can read, and never shown again. Leave empty to reuse
                    what is already stored for {provider.name}.
                  </p>
                </div>
              )}
              {provider && (
                <p className="text-hint leading-relaxed text-muted-foreground">
                  certbot waits {provider.defaultWait}s for the record to propagate before asking
                  Let&rsquo;s Encrypt to look. A challenge that fails on the first try is almost
                  always that wait being too short rather than a wrong token.
                </p>
              )}
            </Well>
          )}

          {method === "webroot" && (
            <div className="space-y-1.5">
              <Label htmlFor="issue-webroot">Folder</Label>
              <Input
                id="issue-webroot"
                value={webRoot}
                onChange={(e) => setWebRoot(e.target.value)}
                className="font-mono text-xs"
              />
            </div>
          )}
          <div className="flex items-start justify-between gap-3 rounded-lg border border-hairline bg-surface-sunken p-2.5">
            <div className="min-w-0 space-y-0.5">
              <Label className="font-normal">Test run first</Label>
              <p className="text-hint leading-relaxed text-muted-foreground">
                Issues from Let&rsquo;s Encrypt&rsquo;s staging authority: not trusted by browsers,
                and not rate-limited. The real limit is five failures an hour and it is easy to
                reach, so this is the right first attempt.
              </p>
            </div>
            <Switch checked={staging} onCheckedChange={setStaging} className="mt-0.5 shrink-0" />
          </div>
          {!staging && (
            <Notice tone="warning" icon={Warning} title="This counts against the rate limit">
              Five failed attempts an hour for the same set of names, and five duplicate
              certificates a week. Get a staging run to pass first.
            </Notice>
          )}
        </div>
      </Modal>
    </>
  )
}
