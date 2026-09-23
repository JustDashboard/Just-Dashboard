"use client"

import { useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { External, LockOpen, RotateClockwise, Warning, Wrench, type Icon } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { calendarDate } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { DashboardConfigReport, DashboardSettings, TailscaleIdentity } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { configPhaseLabel, useSelfConfig } from "@/hooks/use-self-config"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { RestartProgress } from "@/components/config/restart-progress"
import {
  Disclosure,
  Field,
  FieldRow,
  FormSection,
  FormSections,
  OptionList,
  OptionRow,
} from "@/components/form"
import { Page, PageHeader, PageState } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
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
 * It is drawn in two parts. **The stack** is what the dashboard runs as — its
 * three services, each as the product it is, and the checkout they are built
 * from — beside the two commands that recreate it, which are the heaviest
 * things on the page and are drawn as the two things you pick between rather
 * than as two outline buttons in the header. The restart they start is watched
 * under them. **The settings** are a form in five sections, each head in a
 * rail with what the section currently is under it.
 *
 * **Four readings used to sit under the title** — Answers at, Certificate,
 * Port, Two-factor — and §15 pass 2 is dropped here the way `/git` drops it,
 * by naming where each went. The address and the URL it implies are the
 * Address section's head, beside the field that sets them. The certificate's
 * state is the Certificate section's head, with its issuer's mark, and the
 * card of the mode that produced it is selected. The port is on the proxy's
 * row in the stack, with the two loopback ports on the rows of the services
 * that listen on them. Two-factor is the Access section's head and the switch
 * under it. Each figure is now beside the control that changes it, which is
 * what the tiles could not be.
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

  return (
    <Page className="animate-rise">
      {dialog}
      <PageHeader
        eyebrow="Settings"
        title="Configuration"
        actions={<ReadOnlyNote supported={report.supported} admin={admin} running={running} />}
      />

      <Panel plain>
        <PanelHeader
          title="Stack"
          actions={
            running ? (
              <Status tone="running" label={restarting ? "Restarting" : "Working"} />
            ) : (
              <Status tone="running" label="Running" />
            )
          }
        />
        <PanelBody className="space-y-5">
          <div className="grid min-w-0 items-start gap-x-10 gap-y-6 xl:grid-cols-[minmax(0,1fr)_minmax(0,34rem)]">
            <Stack report={report} />

            {commands ? (
              <ChoiceGrid columns={2} className="xl:grid-cols-1">
                <Command
                  title="Restart"
                  verb="Restart the dashboard"
                  description="Recreates every container on the settings already on disk."
                  cost="seconds · sessions survive"
                  mark={RotateClockwise}
                  run={report.run}
                  action="restart"
                  running={running}
                  onClick={() => startRestart(false)}
                />
                <Command
                  title="Rebuild"
                  verb="Rebuild and restart"
                  description="Rebuilds every image from the checkout, then recreates the containers."
                  cost="minutes · after editing the code"
                  mark={Wrench}
                  run={report.run}
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

      <Panel plain>
        <PanelHeader
          title="Settings"
          actions={report.envPath && <Tag mono>{within(report.dir, report.envPath)}</Tag>}
        />
        <PanelBody className="pt-8">
          <FormSections>
            <FormSection
              aside
              title="Address"
              hint={
                // The host is the field beside it, so it may truncate here;
                // what the rail adds is that it answers, and on which port.
                <span className="block space-y-1">
                  <a
                    href={report.endpoint}
                    title={report.endpoint}
                    className="flex min-w-0 items-center gap-1 rounded-sm font-mono text-foreground focus-ring transition-colors hover:text-brand"
                  >
                    <span className="truncate">{report.settings.site}</span>
                    <External aria-hidden className="size-3 shrink-0" />
                  </a>
                  <span className="block">
                    port {report.settings.port} over{" "}
                    {report.settings.tls === "off" ? "plain HTTP" : "HTTPS"}
                  </span>
                </span>
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

            <FormSection aside title="Certificate" hint={<CertificateState report={report} />}>
              <ChoiceGrid columns={3}>
                {/* Disabled only with no tailnet to speak of. A tailnet that
                    will not issue certificates yet is not a reason to refuse
                    the address: the dashboard answers at the MagicDNS name
                    with a self-signed certificate until the switch is flipped,
                    and upgrades itself when it is. */}
                <ChoiceCard
                  verb="Use a Tailscale certificate"
                  title="Tailscale"
                  logo={<ProductLogo id="tailscale" size="sm" />}
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
                  logo={<ProductLogo id="caddy" size="sm" />}
                  description="Caddy's own CA — encrypted, and browsers warn once."
                  selected={draft.tls === "internal"}
                  disabled={!editable}
                  onClick={() => selectMode("internal")}
                />
                <ChoiceCard
                  verb="Serve plain HTTP on localhost"
                  title="Plain HTTP"
                  logo={<ProductLogo size="sm" fallback={LockOpen} />}
                  description="Localhost only — an SSH tunnel is the encryption."
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

            <FormSection
              aside
              title="Access"
              hint={
                <>
                  {cidrs.length} {cidrs.length === 1 ? "network" : "networks"} allowed · two-factor{" "}
                  {draft.require2fa ? "required" : "optional"}
                </>
              }
            >
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
              </OptionList>
            </FormSection>

            <FormSection
              aside
              title="Sessions"
              hint={`a sign-in lasts ${draft.sessionTtl}, idle ones end after ${draft.idleTtl}`}
            >
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
                    className="font-mono text-body"
                  />
                </Field>
                <Field
                  label="Idle timeout"
                  htmlFor="cfg-idle"
                  hint="Unused sessions expire, e.g. 60m."
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

            <FormSection aside title="Features" hint={`${features} of 2 on`}>
              <OptionList>
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
            </FormSection>
          </FormSections>
        </PanelBody>
      </Panel>

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

/**
 * What the dashboard runs as: the checkout and its compose file, then the
 * three services, each drawn as the product it is and with the address it
 * listens on. The proxy is the only one with a routable address; the other two
 * are loopback, reached through it, which is why their ports are the fold
 * under Address rather than fields beside the port that matters.
 */
function Stack({ report }: { report: DashboardConfigReport }) {
  const s = report.settings
  return (
    <RowList>
      <Row
        leading={<ProductLogo id="docker-compose" size="sm" />}
        title={
          report.dir ? (
            <span className="font-mono">{report.dir}</span>
          ) : (
            <span className="text-muted-foreground">No compose project</span>
          )
        }
        subtitle={
          report.supported ? (
            <span className="flex min-w-0 flex-wrap gap-x-2 font-mono">
              {report.compose && <span>{within(report.dir, report.compose)}</span>}
              {report.envPath && (
                <>
                  <span className="text-muted-foreground/40">·</span>
                  <span>{within(report.dir, report.envPath)}</span>
                </>
              )}
            </span>
          ) : (
            <>
              settings read from <span className="font-mono">.env</span>
            </>
          )
        }
      />
      <Row
        leading={<ProductLogo id="caddy" size="sm" />}
        title={
          <>
            Proxy <span className="font-normal text-muted-foreground">· Caddy</span>
          </>
        }
        subtitle={s.bind ? `listens on ${s.bind}` : `listens on ${s.site}`}
        trailing={<Port value={`:${s.port}`} />}
      />
      <Row
        leading={<ProductLogo id="nextjs" size="sm" />}
        title={
          <>
            Frontend <span className="font-normal text-muted-foreground">· Next.js</span>
          </>
        }
        subtitle="loopback, reached through the proxy"
        trailing={<Port value={`127.0.0.1:${s.frontendPort}`} />}
      />
      <Row
        leading={<ProductLogo id="go" size="sm" />}
        title={
          <>
            Backend <span className="font-normal text-muted-foreground">· Go</span>
          </>
        }
        subtitle="loopback, reached through the proxy"
        trailing={<Port value={`127.0.0.1:${s.backendPort}`} />}
      />
    </RowList>
  )
}

function Port({ value }: { value: string }) {
  return <span className="numeric font-mono text-xs text-foreground">{value}</span>
}

/**
 * One of the two commands, drawn as a choice between two ways of restarting —
 * which is what the pair is — so each carries its consequence and its cost
 * where two outline buttons in the header carried only a word each.
 *
 * While a restart is in flight both stop being pressable, and the one that is
 * running says so: its edge carries the beam §11 gives work in progress, and
 * its reading becomes the phase it is at.
 */
function Command({
  title,
  verb,
  description,
  cost,
  mark,
  run,
  action,
  running,
  onClick,
}: {
  title: string
  verb: string
  description: string
  cost: string
  mark: Icon
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
      title={<span className="text-sm font-semibold tracking-tight">{title}</span>}
      logo={
        <ProductLogo size="sm" fallback={mark} className="[&_svg]:size-4.5 [&_svg]:text-brand" />
      }
      description={description}
      trailing={
        <span className="pt-1 text-hint text-muted-foreground">
          {mine && run ? <TextShimmer>{configPhaseLabel(run)}</TextShimmer> : cost}
        </span>
      }
      disabled={running}
      className={mine ? "opacity-100" : undefined}
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
  if (tls === "tailscale" && issued) {
    return (
      <span className="block space-y-1">
        <Status tone="running" label="Trusted" className="text-body" />
        <span className="flex items-center gap-1.5">
          <ProductGlyph id="lets-encrypt" />
          Let&rsquo;s Encrypt, through Tailscale
        </span>
        <span className="block">
          renews automatically{expires ? ` · expires ${calendarDate(expires)}` : ""}
        </span>
      </span>
    )
  }
  return (
    <span className="block space-y-1">
      <Status
        tone={tls === "off" ? "unknown" : "warning"}
        label={certLabel(tls, issued)}
        className="text-body"
      />
      <span className="block">{certHint(tls, issued)}</span>
    </span>
  )
}

/** What the tailnet can do for this machine, on the Tailscale card. */
function TailnetReading({ tailnet }: { tailnet: TailscaleIdentity }) {
  if (!tailnet.running || !tailnet.hostname) return null
  return (
    <span className="flex min-w-0 flex-col gap-1 pt-1">
      <span className="truncate font-mono text-hint text-foreground">{tailnet.hostname}</span>
      <Status
        tone={tailnet.httpsEnabled ? "running" : "warning"}
        label={tailnet.httpsEnabled ? "issues certificates" : "HTTPS off in your tailnet"}
        className="text-hint"
      />
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
