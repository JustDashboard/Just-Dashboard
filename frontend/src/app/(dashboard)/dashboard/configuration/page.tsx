"use client"

import { useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { ChevronDown, Warning } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DashboardSettings, TailscaleIdentity } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { useSelfConfig } from "@/hooks/use-self-config"
import { useConfirm } from "@/components/confirm-dialog"
import { RestartProgress } from "@/components/config/restart-progress"
import { Field, FieldRow, OptionList, OptionRow } from "@/components/form"
import { Page, PageHeader, PageState } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

/**
 * The dashboard's own configuration: where it listens, how it is trusted, who
 * may reach it, and the buttons that restart it.
 *
 * Every setting here used to be an ssh session and a hand-edited .env, which is
 * fine until the thing you need to change is the port you would have to reach
 * the dashboard on to change it. The page exists because that circle has to be
 * broken somewhere.
 *
 * Its first draft explained itself to death: every field carried a paragraph,
 * every state carried a banner, and the page that resulted was unreadable — you
 * could not tell at a glance what was configurable, what was merely being
 * described, or which of six boxes of prose applied to you. So the rule now is
 * that **the page shows state and controls; the prose is one line each, and the
 * reasoning lives behind the ⓘ next to the label**. Nothing was removed, it was
 * moved to where somebody who wants it can ask for it.
 *
 * Its second draft then framed everything it had kept: two boxed panels, a
 * boxed run record, a boxed row of switches and two boxed drawers, on a page
 * whose neighbours had just stopped drawing boxes. The forms are plain now and
 * the two titles and the gap between them are the structure; the switches are
 * `OptionRow`s; the paths the install lives at are a row of facts under the
 * title, because they are what the page is rather than a setting on it.
 */
export default function DashboardConfigurationPage() {
  const { can } = useAuth()
  const {
    report,
    loading,
    error,
    restarting,
    running,
    apply,
    restart,
    issueCertificate,
    dismiss,
    refresh,
  } = useSelfConfig()
  const { confirm, dialog } = useConfirm()
  // The unsaved changes are kept for the tab: the page lists them as a diff
  // with Apply beside it, so what was typed before a walk to another page is
  // in plain sight rather than silently gone or silently pending.
  const [local, setLocal] = useSessionState<DashboardSettings | null>(
    "dashboard.configuration.draft",
    null,
  )

  const saved = report?.settings
  // The form follows the server until the operator types into it, and does so
  // by *deriving* rather than by copying: a draft mirrored into state on every
  // poll would wipe half-typed input every two seconds during a restart, and
  // one that never re-synced would show settings a restart has already
  // replaced. Null means "whatever the server says"; discarding returns to it.
  const draft = local ?? saved
  const changes = useMemo(() => (saved && draft ? diff(saved, draft) : []), [saved, draft])
  const dirty = changes.length > 0
  const admin = can("system.admin")
  const editable = Boolean(report?.supported) && admin && !running

  if ((loading && !report) || error) {
    return <PageState eyebrow="System" title="Configuration" error={error ?? undefined} />
  }
  if (!report || !draft) return null

  const set = <K extends keyof DashboardSettings>(key: K, value: DashboardSettings[K]) =>
    setLocal({ ...draft, [key]: value })

  const tailnet = report.tailscale
  // Whether a Tailscale certificate can be had *today*. Not a gate on choosing
  // the mode — the address is the useful half and it works either way — only on
  // what the option is allowed to promise.
  const tailnetIssues = tailnet.running && Boolean(tailnet.hostname) && tailnet.httpsEnabled
  const onTailnetAddress = draft.tls === "tailscale" && draft.site === tailnet.hostname
  // Where this browser is talking to the dashboard, which is what decides
  // whether a change to loopback-only takes the page's own route away.
  const currentHost = typeof window === "undefined" ? "your-server" : window.location.hostname
  const isLocalhost = currentHost === "localhost" || currentHost === "127.0.0.1"

  /**
   * Picking a certificate is picking a way of being reached, so it carries the
   * address, the interface and the allowlist with it.
   *
   * This is the fix for a form that used to reject its own suggestion: choosing
   * Tailscale left the address on `localhost`, and applying then failed with
   * "the address has to be a MagicDNS name" — a fact the machine knew perfectly
   * well and made the operator go and look up. Each mode sets the fields that
   * mode implies, and every one of them stays editable afterwards.
   */
  const selectMode = (tls: DashboardSettings["tls"]) => {
    if (tls === "tailscale" && tailnet.hostname) {
      setLocal({
        ...draft,
        tls,
        site: tailnet.hostname,
        // The proxy container resolves names through Docker's resolver rather
        // than the host's, so it binds the tailnet IP and answers for the name.
        bind: tailnet.ip4 ?? "",
        allowedCidrs: withTailnet(draft.allowedCidrs),
      })
      return
    }
    if (tls === "off") {
      // Plain HTTP is loopback-only by definition, so the address follows the
      // certificate back to localhost rather than being left somewhere the
      // validator would refuse.
      setLocal({ ...draft, tls, site: "localhost", bind: "" })
      return
    }
    setLocal({ ...draft, tls })
  }

  const applyChanges = () => {
    // Whether the dashboard comes back somewhere else, which is the one thing
    // about this change a browser cannot follow on its own.
    const moves =
      saved!.site !== draft.site ||
      saved!.port !== draft.port ||
      saved!.tls !== draft.tls ||
      saved!.bind !== draft.bind

    confirm({
      title: "Apply and restart",
      confirmLabel: "Apply and restart",
      description: (
        <>
          <Well plain>
            <ul className="space-y-1 text-xs">
              {changes.map((change) => (
                <li key={change.key} className="flex flex-wrap items-baseline gap-x-2">
                  <span className="font-medium">{change.label}</span>
                  <span className="font-mono text-muted-foreground line-through">
                    {change.from || "—"}
                  </span>
                  <span className="text-muted-foreground">→</span>
                  <span className="font-mono">{change.to || "—"}</span>
                </li>
              ))}
            </ul>
          </Well>
          {moves && (
            <p>
              Afterwards it answers at <b>{endpointOf(draft)}</b> — open that, not this tab.
            </p>
          )}
          {/* Loopback-only from a browser that is not on loopback is the one
              change that ends with no way back in through a browser at all. The
              tunnel command is the way back, so it is given here. */}
          {draft.site === "localhost" && !isLocalhost && (
            <div className="space-y-1">
              <p>This address stops answering. Reach it with a tunnel:</p>
              <Well className="text-hint break-all">
                ssh -N -L {draft.port}:localhost:{draft.port} you@{currentHost}
              </Well>
            </div>
          )}
          <p className="text-muted-foreground">
            If it does not come back up, the previous settings are restored automatically.
          </p>
        </>
      ),
      action: async () => {
        await apply(draft)
        // Back to following the server: the settings just sent are the ones it
        // is restarting into, and the form should show what it reports rather
        // than what this tab last typed.
        setLocal(null)
      },
    })
  }

  const startRestart = (rebuild: boolean) =>
    confirm({
      title: rebuild ? "Rebuild and restart" : "Restart the dashboard",
      confirmLabel: rebuild ? "Rebuild" : "Restart",
      description: rebuild ? (
        <>
          <p>
            Rebuilds every image from <code className="font-mono">{report.dir}</code> and recreates
            the containers. Takes a few minutes.
          </p>
          <p className="text-muted-foreground">
            Settings, data and accounts are untouched. This is what to do after editing the code on
            disk.
          </p>
        </>
      ) : (
        <>
          <p>Recreates every container on the settings already on disk. Takes a few seconds.</p>
          <p className="text-muted-foreground">
            Sessions survive — they live in the database, not in the process.
          </p>
        </>
      ),
      action: async () => {
        await restart(rebuild)
      },
    })

  const notices = !report.supported || (report.drift?.length ?? 0) > 0

  return (
    <Page className="animate-rise">
      {dialog}
      <PageHeader
        eyebrow="System"
        title="Configuration"
        actions={
          <>
            <Button variant="outline" size="sm" onClick={refresh}>
              Refresh
            </Button>
            {admin && report.supported && (
              <>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={running}
                  onClick={() => startRestart(false)}
                >
                  Restart
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={running}
                  onClick={() => startRestart(true)}
                >
                  Rebuild
                </Button>
              </>
            )}
          </>
        }
      />

      {/* Where this install lives. It was a drawer of four facts at the foot of
          the page; the checkout, its compose file and its settings file are
          what the page is about, so they are its first row — and the reason
          the fields below are read-only, when they are, sits beside them. */}
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-body text-muted-foreground">
        {report.supported ? (
          <>
            <span className="min-w-0 truncate font-mono text-foreground">{report.dir}</span>
            {report.compose && (
              <>
                <Dot />
                <span className="min-w-0 truncate">
                  compose{" "}
                  <span className="font-mono text-foreground">
                    {within(report.dir, report.compose)}
                  </span>
                </span>
              </>
            )}
            {report.envPath && (
              <>
                <Dot />
                <span className="min-w-0 truncate">
                  settings{" "}
                  <span className="font-mono text-foreground">
                    {within(report.dir, report.envPath)}
                  </span>
                </span>
              </>
            )}
          </>
        ) : (
          <span className="min-w-0 truncate">
            settings read from <span className="font-mono text-foreground">.env</span>
          </span>
        )}
        <ReadOnlyNote supported={report.supported} admin={admin} running={running} />
      </div>

      {/* What is true right now, in four figures. The certificate tile reads
          the certificate on disk rather than the mode in the file: JD_TLS
          =tailscale with nothing issued yet is served by Caddy's internal CA,
          and calling that "trusted" would be the one word the padlock
          disagrees with. */}
      <StatGrid columns={4}>
        <StatTile label="Answers at" value={report.settings.site} hint={report.endpoint} />
        <StatTile
          label="Certificate"
          value={certLabel(report.settings.tls, report.certificate.issued)}
          tone={certTone(report.settings.tls, report.certificate.issued)}
          hint={certHint(report.settings.tls, report.certificate.issued)}
        />
        <StatTile
          label="Port"
          value={String(report.settings.port)}
          hint={`frontend ${report.settings.frontendPort} · backend ${report.settings.backendPort}`}
        />
        <StatTile
          label="Two-factor"
          value={report.settings.require2fa ? "Required" : "Optional"}
          hint={`session ${report.settings.sessionTtl} · idle ${report.settings.idleTtl}`}
        />
      </StatGrid>

      {report.run && (
        <Panel plain className="animate-rise">
          <PanelHeader title={running ? "Restarting" : "Last restart"} />
          <PanelBody>
            <RestartProgress
              run={report.run}
              log={report.log}
              restarting={restarting}
              onDismiss={
                admin
                  ? () => {
                      dismiss().catch((err) => notify.error("Could not dismiss", errorMessage(err)))
                    }
                  : undefined
              }
            />
          </PanelBody>
        </Panel>
      )}

      {notices && (
        <div className="space-y-3">
          {!report.supported && (
            <Notice title="This install is configured by hand" icon={Warning}>
              {report.reason ?? "No compose project was found for this install."} The settings
              below are read from disk; change them in{" "}
              <code className="font-mono">.env</code> over ssh.
            </Notice>
          )}

          {report.drift && report.drift.length > 0 && (
            <Notice title=".env has been edited since this dashboard started" tone="warning">
              <p className="mb-1">Restarting adopts these. Nothing is lost by leaving them.</p>
              <ul className="space-y-0.5">
                {report.drift.map((change) => (
                  <li key={change.key} className="font-mono text-hint">
                    {change.label}: {change.from || "—"} → {change.to || "—"}
                  </li>
                ))}
              </ul>
            </Notice>
          )}
        </div>
      )}

      <div className="grid items-start gap-8 lg:grid-cols-2 [&>*]:min-w-0">
        <Panel plain>
          <PanelHeader title="How it is reached" />
          <PanelBody className="space-y-4">
            <FieldRow>
              <Field
                label="Address"
                htmlFor="cfg-site"
                hint="What you type into the browser."
                info="It is also the name on the certificate, so the address and the certificate can never drift apart."
              >
                <Input
                  id="cfg-site"
                  value={draft.site}
                  disabled={!editable}
                  onChange={(e) => set("site", e.target.value)}
                />
              </Field>

              <Field
                label="Port"
                htmlFor="cfg-port"
                hint="The only port to remember."
                info="Everything else in the stack listens on loopback behind the proxy, so this is the single number that has to be reachable."
              >
                <Input
                  id="cfg-port"
                  type="number"
                  inputMode="numeric"
                  value={draft.port}
                  disabled={!editable}
                  onChange={(e) => set("port", Number(e.target.value))}
                />
              </Field>

              <Field
                label="Certificate"
                hint={certHint(draft.tls, tailnetIssues)}
                info="Tailscale gets a real Let's Encrypt certificate for your MagicDNS name. Self-signed is Caddy's own CA — encrypted, but no browser knows the issuer. Plain HTTP is allowed only on localhost, where an SSH tunnel is the encryption."
              >
                <Select
                  value={draft.tls}
                  disabled={!editable}
                  onValueChange={(value) => selectMode(value as DashboardSettings["tls"])}
                >
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {/* Disabled only with no tailnet to speak of. A tailnet that
                        will not issue certificates yet is not a reason to refuse
                        the address: the dashboard answers at the MagicDNS name
                        with a self-signed certificate until the switch is
                        flipped, and upgrades itself when it is. */}
                    <SelectItem value="tailscale" disabled={!tailnet.running || !tailnet.hostname}>
                      {tailnetIssues ? "Tailscale — trusted" : "Tailscale — self-signed for now"}
                    </SelectItem>
                    <SelectItem value="internal">Self-signed</SelectItem>
                    <SelectItem value="off">Plain HTTP — localhost only</SelectItem>
                  </SelectContent>
                </Select>
              </Field>

              <Field
                label="Listening interface"
                htmlFor="cfg-bind"
                hint={`Blank uses ${draft.site}.`}
                info="The proxy resolves names inside its own container rather than on the host, so a Tailscale install binds the tailnet IP and answers for the MagicDNS name behind it."
              >
                <Input
                  id="cfg-bind"
                  value={draft.bind}
                  placeholder={draft.site}
                  disabled={!editable}
                  onChange={(e) => set("bind", e.target.value)}
                />
              </Field>
            </FieldRow>

            {editable && (
              <TailscaleNotice
                tailnet={tailnet}
                saved={saved!}
                issued={report.certificate.issued}
                onTailnetAddress={onTailnetAddress}
                onUseTailnet={() => selectMode("tailscale")}
                onIssue={issueCertificate}
              />
            )}

            <Disclosure label="Internal ports" hint="loopback only, reached by the proxy">
              <FieldRow>
                <Field label="Frontend port" htmlFor="cfg-frontend" hint="Next.js, on 127.0.0.1.">
                  <Input
                    id="cfg-frontend"
                    type="number"
                    inputMode="numeric"
                    value={draft.frontendPort}
                    disabled={!editable}
                    onChange={(e) => set("frontendPort", Number(e.target.value))}
                  />
                </Field>
                <Field label="Backend port" htmlFor="cfg-backend" hint="The Go API, on 127.0.0.1.">
                  <Input
                    id="cfg-backend"
                    type="number"
                    inputMode="numeric"
                    value={draft.backendPort}
                    disabled={!editable}
                    onChange={(e) => set("backendPort", Number(e.target.value))}
                  />
                </Field>
              </FieldRow>
            </Disclosure>
          </PanelBody>
        </Panel>

        <Panel plain>
          <PanelHeader title="Who may reach it" />
          <PanelBody className="space-y-4">
            <Field
              label="Network allowlist"
              htmlFor="cfg-cidrs"
              hint="Checked before the login page. Keep 127.0.0.1/32."
              info="The allowlist is enforced before authentication, so removing your own network does not give you an error page — it makes the dashboard stop existing for you. Loopback is the way back in over an SSH tunnel."
            >
              <Input
                id="cfg-cidrs"
                value={draft.allowedCidrs}
                disabled={!editable}
                onChange={(e) => set("allowedCidrs", e.target.value)}
                className="font-mono text-body"
              />
            </Field>

            <OptionList>
              <OptionRow
                title="Require two-factor"
                hint="Every account must enrol. An enrolled account is asked for its code either way."
                checked={draft.require2fa}
                disabled={!editable}
                onCheckedChange={(value) => set("require2fa", value)}
              />
              <OptionRow
                title="Web terminal"
                hint="A root shell in the browser. Off removes the routes, not just the page."
                checked={draft.terminalEnabled}
                disabled={!editable}
                onCheckedChange={(value) => set("terminalEnabled", value)}
              />
              <OptionRow
                title="Check for new versions"
                hint="The only outbound request it makes."
                checked={draft.updateCheck}
                disabled={!editable}
                onCheckedChange={(value) => set("updateCheck", value)}
              />
            </OptionList>

            <FieldRow>
              <Field
                label="Session lifetime"
                htmlFor="cfg-session"
                hint="How long a sign-in lasts, e.g. 12h."
              >
                <Input
                  id="cfg-session"
                  value={draft.sessionTtl}
                  disabled={!editable}
                  onChange={(e) => set("sessionTtl", e.target.value)}
                />
              </Field>
              <Field label="Idle timeout" htmlFor="cfg-idle" hint="Unused sessions expire, e.g. 60m.">
                <Input
                  id="cfg-idle"
                  value={draft.idleTtl}
                  disabled={!editable}
                  onChange={(e) => set("idleTtl", e.target.value)}
                />
              </Field>
            </FieldRow>
          </PanelBody>
        </Panel>
      </div>

      {/*
        The apply bar follows the reader instead of sitting at the bottom of one
        panel. The change being applied may have been typed at the top of the
        page, and a Save button that has to be hunted for is the reason a form
        gets abandoned half-edited.
      */}
      {editable && dirty && (
        <div className="sticky bottom-0 z-20 -mx-5 mt-auto border-t border-hairline bg-background/85 px-5 py-3 backdrop-blur md:-mx-8 md:px-8">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="text-body">
              <span className="font-medium">
                {changes.length} unsaved change{changes.length === 1 ? "" : "s"}
              </span>
              <span className="text-muted-foreground"> — applying restarts the dashboard</span>
            </p>
            <div className="flex gap-2">
              <Button variant="ghost" size="sm" onClick={() => setLocal(null)}>
                Discard
              </Button>
              <Button size="sm" onClick={applyChanges}>
                Apply and restart
              </Button>
            </div>
          </div>
        </div>
      )}
    </Page>
  )
}

function Dot() {
  return <span className="text-muted-foreground/40">·</span>
}

/** A path inside the checkout, said relative to it; anywhere else, in full. */
function within(dir: string | undefined, path: string) {
  if (dir && path.startsWith(`${dir}/`)) return path.slice(dir.length + 1)
  return path
}

/** Says why the fields are read-only, once, in the row of facts about the install. */
function ReadOnlyNote({
  supported,
  admin,
  running,
}: {
  supported: boolean
  admin: boolean
  running: boolean
}) {
  if (supported && admin && !running) return null
  const reason = !admin
    ? "You need the system admin role to change these."
    : running
      ? "A restart is in flight."
      : "This install has no compose project to restart."
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Tag>Read-only</Tag>
      </TooltipTrigger>
      <TooltipContent>{reason}</TooltipContent>
    </Tooltip>
  )
}

/**
 * What this machine's tailnet can do for it, in one sentence and one button.
 *
 * Three states are worth saying: no tailnet, a tailnet that will not issue
 * certificates yet (the common one, and the only fix is not on this machine),
 * and a tailnet that will while the dashboard is still on localhost.
 */
function TailscaleNotice({
  tailnet,
  saved,
  issued,
  onTailnetAddress,
  onUseTailnet,
  onIssue,
}: {
  tailnet: TailscaleIdentity
  saved: DashboardSettings
  issued: boolean
  onTailnetAddress: boolean
  onUseTailnet: () => void
  onIssue: () => Promise<void>
}) {
  // The state this whole feature exists for, and the one that used to leave
  // somebody staring at a browser warning: the tailnet issues certificates now,
  // the dashboard is already configured for one, and it simply has not been
  // fetched yet. The keeper would get there within a minute or two — but the
  // person who just flipped the switch is here, so give them the button.
  if (tailnet.httpsEnabled && saved.tls === "tailscale" && !issued) {
    return <PendingCertificate onIssue={onIssue} />
  }

  if (!tailnet.running || !tailnet.hostname) {
    // Only worth saying where somebody might have expected it to work.
    if (!tailnet.available) return null
    return (
      <Notice title="Tailscale is installed but not usable yet" icon={Warning}>
        {tailnet.detail}
      </Notice>
    )
  }

  if (!tailnet.httpsEnabled) {
    return (
      <Notice title="Your tailnet does not issue certificates yet" tone="warning">
        <p>
          Turn HTTPS on at{" "}
          <a
            href="https://login.tailscale.com/admin/dns"
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-2"
          >
            login.tailscale.com/admin/dns
          </a>{" "}
          and the padlock appears here within ten minutes. Until then this address works from every
          device on your tailnet, with one browser warning.
        </p>
        {!onTailnetAddress && (
          <Button className="mt-2" size="sm" variant="outline" onClick={onUseTailnet}>
            Move the dashboard to {tailnet.hostname}
          </Button>
        )}
      </Notice>
    )
  }

  if (!onTailnetAddress) {
    return (
      <Notice title="A trusted certificate is available" tone="success">
        <p>
          Your tailnet issues certificates, so this dashboard can answer at{" "}
          <code className="font-mono">{tailnet.hostname}</code> with no browser warning.
        </p>
        <Button className="mt-2" size="sm" variant="outline" onClick={onUseTailnet}>
          Use it
        </Button>
      </Notice>
    )
  }
  return null
}

function PendingCertificate({ onIssue }: { onIssue: () => Promise<void> }) {
  const [busy, setBusy] = useState(false)
  return (
    <Notice title="The trusted certificate has not been fetched yet" tone="warning">
      <p>
        Your tailnet issues certificates now, so this address can lose its browser warning. The
        dashboard checks on its own every minute or two — or get it now, which takes a few seconds
        and blips the proxy while it reloads.
      </p>
      <Button
        className="mt-2"
        size="sm"
        variant="outline"
        pending={busy}
        onClick={() => {
          setBusy(true)
          onIssue()
            .then(() =>
              notify.success("Certificate issued", {
                description: "Reload this page to see the padlock.",
              }),
            )
            .catch((err) => notify.error("Could not issue the certificate", err))
            .finally(() => setBusy(false))
        }}
      >
        Get the certificate now
      </Button>
    </Notice>
  )
}

/** The allowlist with the tailnet range added, unless it is already covered. */
function withTailnet(list: string) {
  const entries = list
    .split(",")
    .map((entry) => entry.trim())
    .filter(Boolean)
  if (entries.some((entry) => entry === "100.64.0.0/10")) return list
  return ["100.64.0.0/10", ...entries].join(",")
}

/**
 * A line that opens onto the settings most operators never touch. No frame and
 * no ground: the same chevron-and-word a finished restart uses for its
 * transcript, so "there is more here" is one mark across the page.
 */
function Disclosure({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: React.ReactNode
}) {
  return (
    <details className="group min-w-0">
      <summary className="inline-flex cursor-pointer list-none items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground">
        <ChevronDown className="size-3.5 transition-transform group-open:rotate-180" />
        <span className="font-medium">{label}</span>
        {hint && <span className="truncate text-hint">· {hint}</span>}
      </summary>
      <div className="pt-3">{children}</div>
    </details>
  )
}

function certLabel(tls: DashboardSettings["tls"], trusted: boolean) {
  switch (tls) {
    case "tailscale":
      return trusted ? "Trusted" : "Self-signed"
    case "off":
      return "None"
    default:
      return "Self-signed"
  }
}

/** `trusted` is whether the Tailscale certificate is real yet — issued and on disk. */
function certTone(tls: DashboardSettings["tls"], trusted: boolean) {
  if (tls === "internal" || (tls === "tailscale" && !trusted)) return "warning" as const
  return "success" as const
}

function certHint(tls: DashboardSettings["tls"], trusted: boolean) {
  switch (tls) {
    case "tailscale":
      return trusted
        ? "Real certificate, renewed automatically."
        : "Self-signed until your tailnet issues one."
    case "off":
      return "Localhost only — the SSH tunnel is the encryption."
    default:
      return "Caddy's own CA — browsers warn once."
  }
}

function endpointOf(s: DashboardSettings) {
  return `${s.tls === "off" ? "http" : "https"}://${s.site}:${s.port}`
}

/**
 * The same comparison the server makes, so the dialog lists exactly what the
 * server is about to write. Kept in the page rather than shared: it exists to
 * describe a form, and the authority is and remains the backend.
 */
function diff(saved: DashboardSettings, draft: DashboardSettings) {
  const fields: { key: keyof DashboardSettings; label: string }[] = [
    { key: "site", label: "Address" },
    { key: "bind", label: "Listening interface" },
    { key: "tls", label: "Certificate" },
    { key: "port", label: "Dashboard port" },
    { key: "frontendPort", label: "Frontend port" },
    { key: "backendPort", label: "Backend port" },
    { key: "allowedCidrs", label: "Network allowlist" },
    { key: "terminalEnabled", label: "Web terminal" },
    { key: "require2fa", label: "Two-factor required" },
    { key: "sessionTtl", label: "Session lifetime" },
    { key: "idleTtl", label: "Idle timeout" },
    { key: "updateCheck", label: "Version checks" },
  ]
  return fields
    .filter((field) => saved[field.key] !== draft[field.key])
    .map((field) => ({
      key: field.key,
      label: field.label,
      from: String(saved[field.key]),
      to: String(draft[field.key]),
    }))
}
