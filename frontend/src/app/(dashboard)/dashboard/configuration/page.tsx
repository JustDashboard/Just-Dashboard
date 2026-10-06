"use client"

import { useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { External, LockOpen, ShieldCheck, Warning, type Icon } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { calendarDate, plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { DashboardConfigReport, DashboardSettings, TailscaleIdentity } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { configPhaseLabel, useSelfConfig } from "@/hooks/use-self-config"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { Allowlist } from "@/components/config/allowlist"
import { EndpointText, RequestPath } from "@/components/config/request-path"
import { RestartProgress } from "@/components/config/restart-progress"
import { ShellWords } from "@/components/deploy/run-evidence"
import { Disclosure, Field, FieldRow, FormSection, OptionList, OptionRow } from "@/components/form"
import { LogoGlyph } from "@/components/logo"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, PageState } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { BorderBeam } from "@/components/ui/border-beam"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
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
 * are. Beside it are the two commands that recreate it, the heaviest things
 * on the page, drawn as the two things you pick between with the Compose or
 * Docker command each runs. The restart they start is watched under them.
 *
 * **The settings** are five sections in two columns, the certificate across
 * both and first, because picking one fills the address, the interface and
 * the allowlist under it. Each head carries what the section currently is,
 * and an *Edited* mark in git's modified colour while it holds a change that
 * is not applied. The allowlist is drawn as the networks it lets in, each as
 * where its addresses are (`config/allowlist.tsx`).
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
 */
export default function DashboardConfigurationPage() {
  const { can } = useAuth()
  const { report, loading, error, restarting, running, apply, restart, issueCertificate, dismiss } =
    useSelfConfig()
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
    return <PageState eyebrow="Settings" title="Configuration" error={error ?? undefined} />
  }
  if (!report || !draft) return null

  const set = <K extends keyof DashboardSettings>(key: K, value: DashboardSettings[K]) =>
    setLocal({ ...draft, [key]: value })

  const tailnet = report.tailscale
  // Whether a Tailscale certificate can be had *today*. Not a gate on choosing
  // the mode — the address is the useful half and it works either way — only on
  // what the option is allowed to promise.
  const tailnetIssues = tailnet.running && Boolean(tailnet.hostname) && tailnet.httpsEnabled
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

  const commands = admin && report.supported
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

  return (
    <Page className="animate-rise">
      {dialog}
      <PageContext eyebrow="Settings" title="Configuration" />

      {/* The install's identity line, the shape the Version page opens on:
          the address it answers at is the name, the checkout it runs from
          and the files that configure it are the facts. */}
      <HostIdentity
        logo={
          <span
            aria-hidden
            className="flex size-12 shrink-0 items-center justify-center rounded-xl border border-hairline bg-surface-sunken"
          >
            <LogoGlyph className="h-6 w-auto text-brand" />
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
        aside={
          <span className="flex flex-wrap items-center gap-3">
            <ReadOnlyNote supported={report.supported} admin={admin} running={running} />
            <Status
              tone={running ? "warning" : "running"}
              label={
                running ? (
                  <TextShimmer>{restarting ? "Restarting" : "Working"}</TextShimmer>
                ) : (
                  "Running"
                )
              }
            />
          </span>
        }
      />

      <Panel plain>
        <PanelHeader title="Stack" />
        <PanelBody className="space-y-6">
          {/* The path takes the width when there is nothing to put beside it:
              a reader without the admin role sees no commands. */}
          <div
            className={cn(
              "grid min-w-0 items-center gap-x-12 gap-y-8",
              (commands || !report.supported) && "xl:grid-cols-[minmax(0,1fr)_minmax(0,22rem)]",
            )}
          >
            <RequestPath report={report} restarting={running} />

            {commands ? (
              <ChoiceGrid columns={2} className="xl:grid-cols-1">
                <Command
                  title="Restart"
                  verb="Restart the dashboard"
                  description="Recreates every container on the settings already on disk."
                  command="docker compose up -d --force-recreate"
                  cost="seconds · sessions survive"
                  product="docker-compose"
                  run={run}
                  action="restart"
                  running={running}
                  onClick={() => startRestart(false)}
                />
                <Command
                  title="Rebuild"
                  verb="Rebuild and restart"
                  description="Rebuilds every image from the checkout, then recreates the containers."
                  command="docker compose build"
                  cost="minutes · after editing the code"
                  product="docker"
                  index={1}
                  run={run}
                  action="rebuild"
                  running={running}
                  onClick={() => startRestart(true)}
                />
              </ChoiceGrid>
            ) : (
              !report.supported && (
                <Notice title="This install is configured by hand" icon={Warning}>
                  {report.reason ?? "No compose project was found for this install."} The settings
                  below are read from disk; change them in <code className="font-mono">.env</code>{" "}
                  over ssh.
                </Notice>
              )
            )}
          </div>

          {/* The file disagreeing with the running process — somebody edited
              .env over ssh and never restarted. Said beside the command that
              adopts it, as a reading, rather than in an amber box of its own. */}
          {report.drift && report.drift.length > 0 && (
            <div className="flex min-w-0 flex-wrap items-baseline gap-x-4 gap-y-1 text-xs">
              <Status tone="warning" label=".env differs from what is running" />
              {report.drift.map((change) => (
                <span key={change.key} className="text-muted-foreground">
                  {change.label}{" "}
                  <span className="font-mono text-hint line-through">{change.from || "—"}</span> →{" "}
                  <span className="font-mono text-hint text-foreground">{change.to || "—"}</span>
                </span>
              ))}
              <span className="text-muted-foreground">Restart adopts these.</span>
            </div>
          )}
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

      {/* The settings are two columns of sections from `xl`, with the
          certificate across both: one column no wider than its fields left
          two thirds of a wide screen empty and made the page three screens
          tall. The certificate is first because picking one fills the
          address, the interface and the allowlist under it. */}
      <Panel plain>
        <PanelHeader title="Settings" />
        <PanelBody className="divide-y divide-hairline pt-8">
          <SettingsRow>
            <FormSection
              aside
              title="Certificate"
              hint={<CertificateState report={report} />}
              actions={pending(["tls"])}
              className={SECTION}
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
            </FormSection>
          </SettingsRow>

          <SettingsRow columns>
            <FormSection
              aside
              title="Address"
              actions={pending(["site", "port", "bind", "frontendPort", "backendPort"])}
              className={SECTION}
              hint={
                // What it answers at once these settings are running: the
                // draft's address, a link only while it is the live one.
                draft.site === saved!.site &&
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
                )
              }
            >
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
                    className="font-mono text-body"
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
                    className="font-mono text-body"
                  />
                </Field>
              </FieldRow>

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
                  className="font-mono text-body"
                />
              </Field>

              <Disclosure
                quiet
                summary="Internal ports"
                facts={`frontend ${draft.frontendPort} · backend ${draft.backendPort}`}
              >
                <FieldRow>
                  <Field label="Frontend port" htmlFor="cfg-frontend" hint="Next.js, on 127.0.0.1.">
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
                  <Field
                    label="Backend port"
                    htmlFor="cfg-backend"
                    hint="The Go API, on 127.0.0.1."
                  >
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
            </FormSection>

            <FormSection
              aside
              title="Access"
              actions={pending(["allowedCidrs", "require2fa"])}
              className={SECTION}
              hint={
                <>
                  {cidrs.length} {cidrs.length === 1 ? "network" : "networks"} allowed · two-factor{" "}
                  {draft.require2fa ? "required" : "optional"}
                </>
              }
            >
              <Allowlist
                value={draft.allowedCidrs}
                disabled={!editable}
                onChange={(value) => set("allowedCidrs", value)}
              />

              <OptionList>
                <OptionRow
                  title={<Option glyph={ShieldCheck}>Require two-factor</Option>}
                  hint="Every account must enrol. An enrolled account is asked for its code either way."
                  checked={draft.require2fa}
                  disabled={!editable}
                  onCheckedChange={(value) => set("require2fa", value)}
                />
              </OptionList>
            </FormSection>
          </SettingsRow>

          <SettingsRow columns>
            <FormSection
              aside
              title="Sessions"
              actions={pending(["sessionTtl", "idleTtl"])}
              className={SECTION}
              hint={
                lifetime && idle
                  ? `a sign-in lasts ${lifetime}, idle ones end after ${idle}`
                  : `a sign-in lasts ${draft.sessionTtl}, idle ones end after ${draft.idleTtl}`
              }
            >
              <FieldRow>
                <Field
                  label="Session lifetime"
                  htmlFor="cfg-session"
                  hint={
                    lifetime ? `${lifetime} from signing in` : "How long a sign-in lasts, e.g. 12h."
                  }
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
                  hint={idle ? `${idle} without a request` : "Unused sessions expire, e.g. 60m."}
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
            </FormSection>

            <FormSection
              aside
              title="Features"
              hint={`${features} of 2 on`}
              actions={pending(["terminalEnabled", "updateCheck"])}
              className={SECTION}
            >
              <OptionList>
                <OptionRow
                  title={<Option product="terminal">Web terminal</Option>}
                  hint="A root shell in the browser. Off removes the routes, not just the page."
                  checked={draft.terminalEnabled}
                  disabled={!editable}
                  onCheckedChange={(value) => set("terminalEnabled", value)}
                />
                <OptionRow
                  title={<Option product="github">Check for new versions</Option>}
                  hint="One small file from GitHub, four times a day — the only outbound request it makes."
                  checked={draft.updateCheck}
                  disabled={!editable}
                  onCheckedChange={(value) => set("updateCheck", value)}
                />
              </OptionList>
            </FormSection>
          </SettingsRow>
        </PanelBody>
      </Panel>

      {/*
        The apply bar follows the reader instead of sitting at the bottom of one
        panel. The change being applied may have been typed at the top of the
        page, and a Save button that has to be hunted for is the reason a form
        gets abandoned half-edited.
      */}
      {editable && dirty && (
        <div className="sticky bottom-0 z-20 -mx-5 mt-auto animate-rise border-t border-hairline bg-background/85 px-5 py-3 backdrop-blur md:-mx-8 md:px-8">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="flex min-w-0 flex-wrap items-baseline gap-x-2 text-body">
              <span className="font-medium">
                {changes.length} unsaved change{changes.length === 1 ? "" : "s"}
              </span>
              <span className="hidden min-w-0 truncate font-mono text-hint text-[var(--git-modified)] sm:inline">
                {changes.map((change) => change.label).join(" · ")}
              </span>
              <span className="text-muted-foreground">— applying restarts the dashboard</span>
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

/** Every section in the grid keeps its own width and no padding of its own: the row has it. */
const SECTION = "max-w-none py-0 first:pt-0 last:pb-0"

/**
 * One band of the settings: a section across the width, or two side by side
 * from `xl`, with the band's hairline under both. Side by side, the gap
 * between them is the separation (§15 pass 1), and the two heads share a line.
 */
function SettingsRow({ columns, children }: { columns?: boolean; children: React.ReactNode }) {
  return (
    <div
      className={cn(
        "grid min-w-0 gap-x-12 gap-y-14 py-8 first:pt-0 last:pb-0",
        columns && "xl:grid-cols-2",
      )}
    >
      {children}
    </div>
  )
}

/** A section holding a change that is not applied yet, in git's colour for a modified file. */
function EditedTag() {
  return <Tag style={{ color: "var(--git-modified)" }}>Edited</Tag>
}

/** An option's name after the product it switches, or the glyph of the kind it is. */
function Option({
  product,
  glyph: Glyph,
  children,
}: {
  product?: string
  glyph?: Icon
  children: React.ReactNode
}) {
  return (
    <span className="inline-flex min-w-0 items-center gap-2">
      {product && <ProductGlyph id={product} className="size-4" />}
      {Glyph && <Glyph aria-hidden className="size-4 shrink-0 text-muted-foreground" />}
      {children}
    </span>
  )
}

/** A certificate mode's address and scheme, under its sentence on the card. */
function ModeReading({ children }: { children: React.ReactNode }) {
  return <span className="pt-1 font-mono text-hint text-muted-foreground">{children}</span>
}

/**
 * One of the two commands, drawn as a choice between two ways of restarting —
 * which is what the pair is — so each carries its consequence, the command it
 * runs and its cost where two outline buttons in the header carried only a
 * word each. The mark is what does the work: Compose recreates the stack,
 * Docker builds its images.
 *
 * While a restart is in flight both stop being pressable, and the one that is
 * running says so: its edge carries the beam §11 gives work in progress, and
 * its reading becomes the phase it is at.
 */
function Command({
  title,
  verb,
  description,
  command,
  cost,
  product,
  index,
  run,
  action,
  running,
  onClick,
}: {
  title: string
  verb: string
  description: string
  command: string
  cost: string
  product: string
  index?: number
  run?: DashboardConfigReport["run"]
  action: "restart" | "rebuild"
  running: boolean
  onClick: () => void
}) {
  // An apply is a restart too, and is drawn on the restart card.
  const mine =
    running && run && (run.action === action || (action === "restart" && run.action === "apply"))
  return (
    <ChoiceCard
      verb={verb}
      // The mark heads the card beside its name and its cost, rather than
      // through `logo`, which gives the tile a column of its own: beside four
      // lines of words that column was a 40px tile over 80px of nothing, and
      // the words it pushed over wrapped twice as often. Here the tile is as
      // tall as the two lines beside it, and what follows has the full width.
      title={
        <span className="flex min-w-0 items-center gap-3">
          <ProductLogo id={product} size="sm" />
          <span className="min-w-0">
            <span className="block truncate text-sm font-semibold tracking-tight">{title}</span>
            <span className="block truncate text-hint font-normal text-muted-foreground">
              {mine && run ? <TextShimmer>{configPhaseLabel(run)}</TextShimmer> : cost}
            </span>
          </span>
        </span>
      }
      description={<span className="mt-0.5 block">{description}</span>}
      index={index}
      trailing={
        <Well className="w-full truncate px-2.5 py-1.5">
          <span aria-hidden className="text-muted-foreground/60 select-none">
            ${" "}
          </span>
          <ShellWords command={command} />
        </Well>
      }
      disabled={running}
      className={cn("gap-2", mine && "opacity-100")}
      onClick={onClick}
    >
      {mine && (
        <span aria-hidden className="pointer-events-none absolute -inset-px rounded-xl">
          <BorderBeam size={80} duration={4} />
        </span>
      )}
    </ChoiceCard>
  )
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
