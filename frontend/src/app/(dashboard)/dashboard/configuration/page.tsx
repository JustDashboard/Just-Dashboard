"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { External, LockOpen, MoreHorizontal, ShieldCheck } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { calendarDate, plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { DashboardConfigReport, DashboardSettings, TailscaleIdentity } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { configPhaseLabel, useSelfConfig } from "@/hooks/use-self-config"
import { useSelfUninstall } from "@/hooks/use-self-uninstall"
import { stackGone } from "@/lib/uninstall"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { Allowlist } from "@/components/config/allowlist"
import { EndpointText, RequestPath } from "@/components/config/request-path"
import { RestartProgress } from "@/components/config/restart-progress"
import { UninstallDialog, UninstallProgress, UninstallRow } from "@/components/config/uninstall"
import { UsagePingSection } from "@/components/config/usage-ping"
import { PendingStrip } from "@/components/deploy/settings/pending-strip"
import { SaveBarProvider, useSaveBarEntry } from "@/components/deploy/settings/save-bar"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import { Disclosure, Field, FieldRow } from "@/components/form"
import { LogoGlyph } from "@/components/logo"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, PageState } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import {
  EditedTag,
  NoticeLine,
  NoticeLines,
  SettingsColumn,
  SwitchRow,
  SwitchRows,
} from "@/components/settings-column"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { MenuItemText } from "@/components/ui/menu-item-text"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

/**
 * The dashboard's own configuration: where it listens, how it is trusted, who
 * may reach it, and the two commands that restart it.
 *
 * Every setting here used to be an ssh session and a hand-edited .env, which is
 * fine until the thing you need to change is the port you would have to reach
 * the dashboard on to change it. The page exists because that circle has to be
 * broken somewhere.
 *
 * Its first draft explained itself to death: every field carried a paragraph,
 * every state carried a banner, and the page that resulted was unreadable. So
 * the rule is that **the page shows state and controls; the prose is one line
 * each, and the reasoning lives behind the ⓘ next to the label**.
 *
 * It opens on **the install's identity line** — the address it answers at as
 * its name, the checkout and the two files that configure it as its facts, and
 * whether it is running at the far end — the shape the Version page opens on.
 *
 * **The stack** is drawn as the path a request takes to reach it: this
 * browser, the network it arrives on, Caddy with the certificate's issuer,
 * and the two loopback services behind it (`config/request-path.tsx`). It
 * was a list of the services with their addresses, which said what the stack
 * is made of but not why the reader can or cannot reach it from where they
 * are. The two commands that recreate it are the identity line's `⋯`, each
 * with the Compose or Docker command it runs and what it costs: they were
 * two framed cards beside the picture, the loudest things on a page they
 * are rarely pressed on. The restart they start is watched under the path.
 *
 * **The settings** are one centred column of sections, as a project's
 * settings are (`deploy/settings/setting-card.tsx`), the certificate first
 * because picking one fills the address, the interface and the allowlist
 * under it. They were two columns across the width, which set two unrelated
 * heads on one line and a helper sentence under every field; the sentences
 * are behind the ⓘ beside each name now, and a switch is one line. Each head
 * carries what the section currently is, and an *Edited* mark in git's
 * modified colour while it holds a change that is not applied. The allowlist
 * is drawn as the networks it lets in, each as where its addresses are
 * (`config/allowlist.tsx`). What `.env` holds that the running stack does
 * not is the strip at the column's head. An edit raises the settings pages'
 * one floating bar, whose command here is *Apply and restart*; it asks the
 * same question, with the same list of changes, that it always asked.
 *
 * **Four readings used to sit under the title** — Answers at, Certificate,
 * Port, Two-factor — and §15 pass 2 is dropped here the way `/git` drops it,
 * by naming where each went. The address is the identity line's name and the
 * Address section's head, beside the field that sets it. The certificate's
 * state is the Certificate section's head, with its issuer's mark, and the
 * issuer again on the proxy in the path. The port is on the proxy in the
 * path, with the two loopback ports on the services that listen on them.
 * Two-factor is the Access section's head and the switch under it. Each
 * figure is beside the control that changes it, which is what the tiles could
 * not be.
 *
 * **Uninstalling** is last, in the danger rule, and asks in its dialog whether
 * the deployments and data stay for a reinstall (`config/uninstall.tsx`). An
 * uninstall in flight takes the page: its record sits under the identity line
 * and the stack and the settings step aside, since from the moment it starts
 * they describe what is being taken away. The page stays drawn when the API it
 * polls goes away with the stack.
 */
export default function DashboardConfigurationPage() {
  return (
    <SaveBarProvider>
      <Configuration />
    </SaveBarProvider>
  )
}

function Configuration() {
  const landedAccess = useRef(false)
  useEffect(() => {
    if (landedAccess.current || window.location.hash !== "#allowlist") return
    const target = document.getElementById("allowlist")
    if (!target) return
    landedAccess.current = true
    target.scrollIntoView({ block: "start" })
  })
  const { can } = useAuth()
  const { report, loading, error, restarting, running, apply, restart, issueCertificate, dismiss } =
    useSelfConfig()
  const { confirm, dialog } = useConfirm()
  const admin = can("system.admin")
  const removal = useSelfUninstall({ enabled: admin })
  const [uninstalling, setUninstalling] = useState(false)
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
  const uninstallRun = removal.report?.run
  // Once an uninstall is under way the settings are not the page's to change,
  // and a failing poll is the stack going rather than a page to replace.
  const removing = removal.running
  const stopped = Boolean(uninstallRun && stackGone(uninstallRun, removal.lost))
  const editable = Boolean(report?.supported) && admin && !running && !removing
  // Where this browser is talking to the dashboard, which is what decides
  // whether a change to loopback-only takes the page's own route away.
  const currentHost = typeof window === "undefined" ? "your-server" : window.location.hostname
  const isLocalhost = currentHost === "localhost" || currentHost === "127.0.0.1"

  const applyChanges = () => {
    if (!saved || !draft) return
    // Whether the dashboard comes back somewhere else, which is the one thing
    // about this change a browser cannot follow on its own.
    const moves =
      saved.site !== draft.site ||
      saved.port !== draft.port ||
      saved.tls !== draft.tls ||
      saved.bind !== draft.bind

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

  // The settings are one form: one PUT writes all twelve, so the page's bar
  // counts every edit as one entry and names the sections they are in. Its
  // command opens the question Apply always asked, and answers the bar "not
  // saved": the edits stay on it until the restart has been confirmed, and
  // leave with the draft once it has.
  useSaveBarEntry(
    editable,
    {
      name: sectionsOf(changes),
      dirty,
      changes: changes.length,
      saving: false,
      invalid: false,
      applies: "immediately",
      verb: "Apply and restart",
      note: "Restarts the dashboard",
    },
    {
      save: async () => {
        applyChanges()
        return false
      },
      discard: () => setLocal(null),
    },
  )

  if ((loading && !report) || (error && !(removing && report))) {
    return (
      <PageState
        eyebrow="Settings"
        title="Configuration"
        error={error ?? undefined}
        // The identity line, the path and the column, at about their heights.
        skeleton={
          <>
            <Skeleton className="h-14 rounded-xl" />
            <Skeleton className="h-64 rounded-xl" />
            <Skeleton className="mx-auto h-96 w-full max-w-3xl rounded-xl" />
          </>
        }
      />
    )
  }
  if (!report || !draft) return null

  const set = <K extends keyof DashboardSettings>(key: K, value: DashboardSettings[K]) =>
    setLocal({ ...draft, [key]: value })

  const tailnet = report.tailscale
  // Whether a Tailscale certificate can be had *today*. Not a gate on choosing
  // the mode — the address is the useful half and it works either way — only on
  // what the option is allowed to promise.
  const tailnetIssues = tailnet.running && Boolean(tailnet.hostname) && tailnet.httpsEnabled

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

  const commands = admin && report.supported && !removing
  const cidrs = draft.allowedCidrs
    .split(",")
    .map((entry) => entry.trim())
    .filter(Boolean)
  const features = [draft.terminalEnabled, draft.updateCheck].filter(Boolean).length
  const edited = new Set(changes.map((change) => change.key))
  const pending = (keys: (keyof DashboardSettings)[]) =>
    editable && keys.some((key) => edited.has(key)) ? <EditedTag /> : undefined
  const lifetime = durationWords(draft.sessionTtl)
  const idle = durationWords(draft.idleTtl)
  const run = report.run
  const drift = report.drift ?? []

  return (
    <Page className="animate-rise">
      {dialog}
      <PageContext eyebrow="Settings" title="Configuration" />

      {/* The install's identity line, laid out as a project's: the address
          it answers at is the name with its state beside it, the checkout it
          runs from and the files that configure it are the facts, and the
          two commands that recreate the stack are its `⋯`. */}
      <HostIdentity
        logo={
          <span
            aria-hidden
            className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-hairline bg-surface-sunken"
          >
            <LogoGlyph className="h-5 w-auto text-brand" />
          </span>
        }
        title={
          <a
            href={report.endpoint}
            title={report.endpoint}
            className="inline-flex max-w-full min-w-0 items-center gap-1.5 rounded-sm focus-ring transition-colors hover:[&_svg]:text-foreground"
          >
            <EndpointText url={report.endpoint} />
            <External aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
          </a>
        }
        state={
          <>
            {removing ? (
              <Status
                tone={stopped ? "stopped" : "warning"}
                label={stopped ? "Stopped" : <TextShimmer>Uninstalling</TextShimmer>}
              />
            ) : (
              <Status
                tone={running ? "warning" : "running"}
                label={
                  running ? (
                    <TextShimmer>
                      {restarting || !run ? "Restarting" : configPhaseLabel(run)}
                    </TextShimmer>
                  ) : (
                    "Running"
                  )
                }
              />
            )}
            <ReadOnlyNote supported={report.supported} admin={admin} running={running} />
          </>
        }
        facts={
          report.dir ? (
            <>
              <HostFact product="docker-compose">
                <span className="font-mono text-foreground">{report.dir}</span>
              </HostFact>
              {report.compose && (
                <>
                  <FactDot />
                  <span className="font-mono">{within(report.dir, report.compose)}</span>
                </>
              )}
              {report.envPath && (
                <>
                  <FactDot />
                  <span className="font-mono">{within(report.dir, report.envPath)}</span>
                </>
              )}
              {run && !running && (
                <>
                  <FactDot />
                  <span>
                    restarted {relativeTime(run.finishedAt ?? run.updatedAt)} by {run.actor}
                  </span>
                </>
              )}
            </>
          ) : (
            <span>
              No compose project · settings read from <span className="font-mono">.env</span>
            </span>
          )
        }
        menu={
          commands ? (
            <StackMenu
              running={running}
              onRestart={() => startRestart(false)}
              onRebuild={() => startRestart(true)}
            />
          ) : undefined
        }
      />

      {!report.supported && !removing && (
        <NoticeLines>
          <NoticeLine
            tone="warning"
            title="Configured by hand"
            detail={
              <>
                {report.reason ?? "No compose project was found for this install."} The settings are
                read from disk; change them in <code className="font-mono">.env</code> over ssh.
              </>
            }
          />
        </NoticeLines>
      )}

      {uninstallRun && (
        <Panel plain className="animate-rise">
          <PanelHeader title={removing ? "Uninstalling" : "Last uninstall"} />
          <PanelBody>
            <UninstallProgress
              run={uninstallRun}
              log={removal.report?.log}
              lost={removal.lost}
              onDismiss={() => {
                removal
                  .dismiss()
                  .catch((err) => notify.error("Could not dismiss", errorMessage(err)))
              }}
            />
          </PanelBody>
        </Panel>
      )}

      <Panel plain hidden={removing}>
        <PanelHeader title="Stack" />
        <PanelBody>
          <RequestPath report={report} restarting={running} />
        </PanelBody>
      </Panel>

      {run && (
        <Panel plain className="animate-rise">
          <PanelHeader title={running ? "Restarting" : "Last restart"} />
          <PanelBody>
            <RestartProgress
              run={run}
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

      {/* One centred column, a section to a row, as a project's settings
          are. The certificate is first because picking one fills the
          address, the interface and the allowlist under it. */}
      {!removing && (
        <SettingsColumn
          strip={
            // The file disagreeing with the running process — somebody edited
            // .env over ssh and never restarted. Saved and not in force, which
            // is what the strip at a settings column's head is for.
            drift.length > 0 && (
              <PendingStrip
                label="Settings not running yet"
                fold="What differs"
                headline={<Status tone="warning" label=".env differs from what is running" />}
                detail={
                  commands && !running ? (
                    <>
                      <button
                        type="button"
                        onClick={() => startRestart(false)}
                        className="rounded-sm text-foreground underline underline-offset-4 focus-ring"
                      >
                        Restart
                      </button>{" "}
                      adopts these.
                    </>
                  ) : (
                    "A restart adopts these."
                  )
                }
                items={drift.map((change) => ({
                  name: (
                    <>
                      {change.label}{" "}
                      <span className="font-mono text-hint text-muted-foreground line-through">
                        {change.from || "—"}
                      </span>{" "}
                      →{" "}
                      <span className="font-mono text-hint text-foreground">
                        {change.to || "—"}
                      </span>
                    </>
                  ),
                  state: "changed",
                }))}
              />
            )
          }
        >
          <SettingSection
            title="Certificate"
            state={<CertificateState report={report} />}
            status={pending(["tls"])}
          >
            <ChoiceGrid columns={3}>
              {/* Disabled only with no tailnet to speak of. A tailnet that
                  will not issue certificates yet is not a reason to refuse
                  the address: the dashboard answers at the MagicDNS name
                  with a self-signed certificate until the switch is flipped,
                  and upgrades itself when it is. */}
              <ChoiceCard
                verb="Use a Tailscale certificate"
                title="Tailscale"
                logo={<ProductLogo id="tailscale" />}
                description={
                  tailnetIssues
                    ? "A real Let's Encrypt certificate for your MagicDNS name."
                    : tailnet.running && tailnet.hostname
                      ? "Self-signed until your tailnet issues certificates."
                      : (tailnet.detail ?? "This machine is not on a tailnet.")
                }
                trailing={<TailnetReading tailnet={tailnet} />}
                selected={draft.tls === "tailscale"}
                disabled={!editable || !tailnet.running || !tailnet.hostname}
                onClick={() => selectMode("tailscale")}
              />
              <ChoiceCard
                verb="Use a self-signed certificate"
                title="Self-signed"
                logo={<ProductLogo id="caddy" />}
                description="Caddy's own CA — encrypted, and browsers warn once."
                trailing={<ModeReading>any address · HTTPS</ModeReading>}
                index={1}
                selected={draft.tls === "internal"}
                disabled={!editable}
                onClick={() => selectMode("internal")}
              />
              <ChoiceCard
                verb="Serve plain HTTP on localhost"
                title="Plain HTTP"
                logo={<ProductLogo fallback={LockOpen} />}
                description="Localhost only — an SSH tunnel is the encryption."
                trailing={<ModeReading>localhost · HTTP</ModeReading>}
                index={2}
                selected={draft.tls === "off"}
                disabled={!editable}
                onClick={() => selectMode("off")}
              />
            </ChoiceGrid>

            {editable && (
              <TailnetAction
                tailnet={tailnet}
                saved={saved!}
                mode={draft.tls}
                issued={report.certificate.issued}
                onIssue={issueCertificate}
              />
            )}
          </SettingSection>

          <SettingSection
            title="Address"
            status={pending(["site", "port", "bind", "frontendPort", "backendPort"])}
            state={
              // What it answers at once these settings are running: the
              // draft's address, a link only while it is the live one. One
              // height either way, so the first keystroke moves no field.
              <span className="flex h-5 min-w-0 items-center">
                {draft.site === saved!.site &&
                draft.port === saved!.port &&
                draft.tls === saved!.tls ? (
                  <a
                    href={report.endpoint}
                    title={report.endpoint}
                    className="inline-flex max-w-full min-w-0 items-center gap-1 rounded-sm focus-ring transition-colors hover:[&_svg]:text-foreground"
                  >
                    <EndpointText url={report.endpoint} />
                    <External aria-hidden className="size-3 shrink-0" />
                  </a>
                ) : (
                  <span className="flex min-w-0 items-center gap-1.5">
                    <span className="shrink-0">will answer at</span>
                    <EndpointText url={endpointOf(draft)} />
                  </span>
                )}
              </span>
            }
          >
            <FieldRow>
              <Field
                label="Address"
                htmlFor="cfg-site"
                info="What you type into the browser. It is also the name on the certificate, so the address and the certificate can never drift apart."
              >
                <Input
                  id="cfg-site"
                  value={draft.site}
                  disabled={!editable}
                  onChange={(e) => set("site", e.target.value)}
                  className="font-mono text-body"
                />
              </Field>

              <Field
                label="Port"
                htmlFor="cfg-port"
                info="The only port to remember. Everything else in the stack listens on loopback behind the proxy, so this is the single number that has to be reachable."
              >
                <Input
                  id="cfg-port"
                  type="number"
                  inputMode="numeric"
                  value={draft.port}
                  disabled={!editable}
                  onChange={(e) => set("port", Number(e.target.value))}
                  className="font-mono text-body"
                />
              </Field>
            </FieldRow>

            <Field
              label="Listening interface"
              htmlFor="cfg-bind"
              info="Blank uses the address. The proxy resolves names inside its own container rather than on the host, so a Tailscale install binds the tailnet IP and answers for the MagicDNS name behind it."
            >
              <Input
                id="cfg-bind"
                value={draft.bind}
                placeholder={draft.site}
                disabled={!editable}
                onChange={(e) => set("bind", e.target.value)}
                className="font-mono text-body"
              />
            </Field>

            <Disclosure
              quiet
              summary="Internal ports"
              facts={`frontend ${draft.frontendPort} · backend ${draft.backendPort}`}
            >
              <FieldRow>
                <Field label="Frontend port" htmlFor="cfg-frontend" info="Next.js, on 127.0.0.1.">
                  <Input
                    id="cfg-frontend"
                    type="number"
                    inputMode="numeric"
                    value={draft.frontendPort}
                    disabled={!editable}
                    onChange={(e) => set("frontendPort", Number(e.target.value))}
                    className="font-mono text-body"
                  />
                </Field>
                <Field label="Backend port" htmlFor="cfg-backend" info="The Go API, on 127.0.0.1.">
                  <Input
                    id="cfg-backend"
                    type="number"
                    inputMode="numeric"
                    value={draft.backendPort}
                    disabled={!editable}
                    onChange={(e) => set("backendPort", Number(e.target.value))}
                    className="font-mono text-body"
                  />
                </Field>
              </FieldRow>
            </Disclosure>
          </SettingSection>

          <SettingSection
            title="Access"
            id="allowlist"
            status={pending(["allowedCidrs", "require2fa"])}
            state={
              <>
                {cidrs.length} {cidrs.length === 1 ? "network" : "networks"} allowed · two-factor{" "}
                {draft.require2fa ? "required" : "optional"}
              </>
            }
          >
            <Allowlist
              value={draft.allowedCidrs}
              disabled={!editable}
              hint={false}
              onChange={(value) => set("allowedCidrs", value)}
            />

            <SwitchRows>
              <SwitchRow
                leading={
                  <ShieldCheck aria-hidden className="size-4 shrink-0 text-muted-foreground" />
                }
                title="Require two-factor"
                info="Every account must enrol. An enrolled account is asked for its code either way."
                checked={draft.require2fa}
                disabled={!editable}
                onCheckedChange={(value) => set("require2fa", value)}
              />
            </SwitchRows>
          </SettingSection>

          <SettingSection
            title="Sessions"
            status={pending(["sessionTtl", "idleTtl"])}
            state={
              lifetime && idle
                ? `a sign-in lasts ${lifetime}, idle ones end after ${idle}`
                : `a sign-in lasts ${draft.sessionTtl}, idle ones end after ${draft.idleTtl}`
            }
          >
            <FieldRow>
              <Field
                label="Session lifetime"
                htmlFor="cfg-session"
                info="How long a sign-in lasts, counted from signing in: 12h, 90m."
                error={
                  !lifetime && draft.sessionTtl ? "Not a duration — try 12h or 90m." : undefined
                }
              >
                <Input
                  id="cfg-session"
                  value={draft.sessionTtl}
                  disabled={!editable}
                  onChange={(e) => set("sessionTtl", e.target.value)}
                  className="font-mono text-body"
                />
              </Field>
              <Field
                label="Idle timeout"
                htmlFor="cfg-idle"
                info="A session that makes no request for this long ends: 60m, 2h."
                error={!idle && draft.idleTtl ? "Not a duration — try 60m or 2h." : undefined}
              >
                <Input
                  id="cfg-idle"
                  value={draft.idleTtl}
                  disabled={!editable}
                  onChange={(e) => set("idleTtl", e.target.value)}
                  className="font-mono text-body"
                />
              </Field>
            </FieldRow>
          </SettingSection>

          <SettingSection
            title="Features"
            state={`${features} of 2 on`}
            status={pending(["terminalEnabled", "updateCheck"])}
          >
            <SwitchRows>
              <SwitchRow
                leading={<ProductGlyph id="terminal" className="size-4" />}
                title="Web terminal"
                info="A root shell in the browser. Off removes the routes, not just the page."
                checked={draft.terminalEnabled}
                disabled={!editable}
                onCheckedChange={(value) => set("terminalEnabled", value)}
              />
              <SwitchRow
                leading={<ProductGlyph id="github" className="size-4" />}
                title="Check for new versions"
                info="One small file from GitHub, every two hours and at most every five minutes while pages are opening."
                checked={draft.updateCheck}
                disabled={!editable}
                onCheckedChange={(value) => set("updateCheck", value)}
              />
            </SwitchRows>
          </SettingSection>

          {admin && <UsagePingSection />}

          {admin && removal.report?.supported && !running && (
            <SettingSection title="Danger zone" tone="danger">
              <UninstallRow onUninstall={() => setUninstalling(true)} />
            </SettingSection>
          )}
        </SettingsColumn>
      )}
      {uninstalling && (
        <UninstallDialog
          onOpenChange={setUninstalling}
          loadPlan={removal.plan}
          onStart={removal.start}
        />
      )}
    </Page>
  )
}

/** The sections a set of changes is in, as the save bar names them. */
const SECTION_OF: Record<keyof DashboardSettings, string> = {
  tls: "Certificate",
  site: "Address",
  port: "Address",
  bind: "Address",
  frontendPort: "Address",
  backendPort: "Address",
  allowedCidrs: "Access",
  require2fa: "Access",
  sessionTtl: "Sessions",
  idleTtl: "Sessions",
  terminalEnabled: "Features",
  updateCheck: "Features",
}

function sectionsOf(changes: { key: keyof DashboardSettings }[]) {
  const names = [...new Set(changes.map((change) => SECTION_OF[change.key]))]
  if (names.length <= 1) return names[0] ?? "Configuration"
  return `${names.slice(0, -1).join(", ")} and ${names.at(-1)}`
}

/**
 * The two commands that recreate the stack, behind the identity line's `⋯`:
 * each is its verb, the Compose or Docker command it runs and what it costs,
 * with the mark of what does the work. Neither can be pressed while a restart
 * is in flight; the line's state says which phase that restart is at.
 */
function StackMenu({
  running,
  onRestart,
  onRebuild,
}: {
  running: boolean
  onRestart: () => void
  onRebuild: () => void
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label="Restart or rebuild"
          className="[&_svg:not([class*='size-'])]:size-3.5"
        >
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-80 max-w-[calc(100vw-2rem)]">
        <DropdownMenuItem disabled={running} onSelect={onRestart} className="items-start">
          <ProductGlyph id="docker-compose" className="mt-0.5 size-4" />
          <MenuItemText
            hint={
              <>
                <span className="font-mono">docker compose up -d --force-recreate</span>
                <span className="block">seconds · sessions survive</span>
              </>
            }
          >
            Restart the dashboard
          </MenuItemText>
        </DropdownMenuItem>
        <DropdownMenuItem disabled={running} onSelect={onRebuild} className="items-start">
          <ProductGlyph id="docker" className="mt-0.5 size-4" />
          <MenuItemText
            hint={
              <>
                <span className="font-mono">docker compose build</span>
                <span className="block">minutes · after editing the code</span>
              </>
            }
          >
            Rebuild and restart
          </MenuItemText>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** A certificate mode's address and scheme, under its sentence on the card. */
function ModeReading({ children }: { children: React.ReactNode }) {
  return <span className="pt-1 font-mono text-hint text-muted-foreground">{children}</span>
}

/** The certificate on disk, as the Certificate section's head reads it. */
function CertificateState({ report }: { report: DashboardConfigReport }) {
  const { tls } = report.settings
  const issued = report.certificate.issued
  const expires = report.certificate.expires
  // One line beside its peers' heads: the state, the issuer drawn as itself,
  // and when it next has to be renewed.
  if (tls === "tailscale" && issued) {
    return (
      <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
        <Status tone="running" label="Trusted" className="text-xs" />
        <span className="inline-flex items-center gap-1.5">
          <ProductGlyph id="lets-encrypt" />
          Let&rsquo;s Encrypt, through Tailscale
        </span>
        <span>renews automatically{expires ? ` · expires ${calendarDate(expires)}` : ""}</span>
      </span>
    )
  }
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
      <Status
        tone={tls === "off" ? "unknown" : "warning"}
        label={certLabel(tls, issued)}
        className="text-xs"
      />
      {tls !== "off" && (
        <span className="inline-flex items-center gap-1.5">
          <ProductGlyph id="caddy" />
          Caddy&rsquo;s own CA
        </span>
      )}
      <span>{certHint(tls, issued)}</span>
    </span>
  )
}

/** What the tailnet can do for this machine, on the Tailscale card. */
function TailnetReading({ tailnet }: { tailnet: TailscaleIdentity }) {
  if (!tailnet.running || !tailnet.hostname) return null
  return (
    <span className="flex min-w-0 flex-col gap-1 pt-1">
      <span className="truncate font-mono text-hint text-foreground">{tailnet.hostname}</span>
      {tailnet.httpsEnabled ? (
        <span className="inline-flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
          <ProductGlyph id="lets-encrypt" className="size-3" />
          issues Let&rsquo;s Encrypt certificates
        </span>
      ) : (
        <Status tone="warning" label="HTTPS off in your tailnet" className="text-hint" />
      )}
    </span>
  )
}

/**
 * The one thing about the tailnet the reader can act on from here, as a line
 * and a button rather than a tinted banner: fetch a certificate the tailnet
 * will now issue, or go and switch HTTPS on where only the tailnet's own admin
 * page can.
 */
function TailnetAction({
  tailnet,
  saved,
  mode,
  issued,
  onIssue,
}: {
  tailnet: TailscaleIdentity
  saved: DashboardSettings
  /** The mode the form is set to, which may not be saved yet. */
  mode: DashboardSettings["tls"]
  issued: boolean
  onIssue: () => Promise<void>
}) {
  const [busy, setBusy] = useState(false)
  if (mode !== "tailscale" || !tailnet.running || !tailnet.hostname) return null

  // The state this whole feature exists for, and the one that used to leave
  // somebody staring at a browser warning: the tailnet issues certificates now,
  // the dashboard is already configured for one, and it simply has not been
  // fetched yet. The keeper would get there within a minute or two — but the
  // person who just flipped the switch is here, so give them the button.
  // Only for the saved mode: the backend issues for the configuration it is
  // running, and refuses in any other.
  if (tailnet.httpsEnabled && !issued && saved.tls === "tailscale") {
    return (
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-3">
        <p className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
          <ProductGlyph id="tailscale" />
          Your tailnet issues certificates now; the dashboard fetches one every few minutes on its
          own.
        </p>
        <Button
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
      </div>
    )
  }

  if (!tailnet.httpsEnabled) {
    return (
      <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
        <ProductGlyph id="tailscale" />
        Turn HTTPS on at
        <a
          href="https://login.tailscale.com/admin/dns"
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-1 rounded-sm text-foreground underline underline-offset-2 focus-ring"
        >
          login.tailscale.com/admin/dns
          <External aria-hidden className="size-3" />
        </a>
        and the padlock arrives within ten minutes.
      </p>
    )
  }
  return null
}

/** A path inside the checkout, said relative to it; anywhere else, in full. */
function within(dir: string | undefined, path: string) {
  if (dir && path.startsWith(`${dir}/`)) return path.slice(dir.length + 1)
  return path
}

/** Says why the fields are read-only, once, in the header. */
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

/** The allowlist with the tailnet range added, unless it is already covered. */
function withTailnet(list: string) {
  const entries = list
    .split(",")
    .map((entry) => entry.trim())
    .filter(Boolean)
  if (entries.some((entry) => entry === "100.64.0.0/10")) return list
  return ["100.64.0.0/10", ...entries].join(",")
}

function certLabel(tls: DashboardSettings["tls"], trusted: boolean) {
  switch (tls) {
    case "tailscale":
      return trusted ? "Trusted" : "Self-signed for now"
    case "off":
      return "No certificate"
    default:
      return "Self-signed"
  }
}

function certHint(tls: DashboardSettings["tls"], trusted: boolean) {
  switch (tls) {
    case "tailscale":
      return trusted ? "Real certificate, renewed automatically." : "until your tailnet issues one"
    case "off":
      return "Localhost only — the SSH tunnel is the encryption."
    default:
      return "browsers warn once"
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

const UNIT_SECONDS: Record<string, number> = { h: 3600, m: 60, s: 1 }

/**
 * A Go duration as a person says it — `12h` is "12 hours", `90m` "1 hour 30
 * minutes" — or nothing for a value the backend would refuse, so the field
 * can say so before Apply does. Only the units a session is measured in.
 */
function durationWords(value: string) {
  const text = value.trim()
  if (!/^(?:\d+(?:\.\d+)?[hms])+$/.test(text)) return undefined
  const parts = text.match(/\d+(?:\.\d+)?[hms]/g) ?? []
  const total = parts.reduce(
    (sum, part) => sum + Number(part.slice(0, -1)) * UNIT_SECONDS[part.slice(-1)],
    0,
  )
  if (total <= 0) return undefined
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const seconds = Math.round(total % 60)
  return [
    hours && plural(hours, "hour"),
    minutes && plural(minutes, "minute"),
    seconds && plural(seconds, "second"),
  ]
    .filter(Boolean)
    .join(" ")
}
