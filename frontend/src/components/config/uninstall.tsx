"use client"

import { useCallback, useState } from "react"
import {
  Archive,
  Check,
  CheckCircle,
  CrossCircle,
  Information,
  Trash,
  type Icon,
} from "@/components/icons"
import { ApiError, errorMessage } from "@/lib/api"
import { bytes, duration, plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type {
  DeploymentFootprint,
  DirectoryUsage,
  UninstallMode,
  UninstallPlan,
  UninstallRun,
} from "@/lib/types"
import {
  DURABLE_LOG,
  footprintLine,
  isLive,
  stackGone,
  uninstallHeadline,
  uninstallStages,
} from "@/lib/uninstall"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { Disclosure, Field, FormFact, FormFacts, OptionList, OptionRow } from "@/components/form"
import { LogoGlyph } from "@/components/logo"
import { Modal } from "@/components/modal"
import { Panel, Well } from "@/components/panel"
import { RunPhases, phaseStates } from "@/components/run-phases"
import { RunTranscript } from "@/components/run-transcript"
import { ErrorState, Spinner } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Skeleton } from "@/components/ui/skeleton"

/**
 * The one act on the Configuration page that does not come back: taking the
 * dashboard off the server.
 *
 * A row in the danger rule, the way the deployment and database settings draw
 * their acts that end something — one red edge saying "careful" once. The
 * question that matters is asked in the dialog it opens, not here: whether
 * what the dashboard made stays for a reinstall or goes with it.
 */
export function UninstallRow({ onUninstall }: { onUninstall: () => void }) {
  return (
    <Panel className="border-rule-danger">
      <div className="grid min-w-0 gap-x-8 gap-y-3 px-5 py-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
        <div className="min-w-0 space-y-1">
          <h4 className="text-sm font-medium">Uninstall Just Dashboard</h4>
          <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">
            Takes the dashboard off this server. You choose whether its deployments and data stay
            for a reinstall or go with it.
          </p>
        </div>
        <div className="flex max-sm:[&>*]:w-full">
          <Button variant="destructive" size="sm" onClick={onUninstall}>
            <Trash />
            Uninstall…
          </Button>
        </div>
      </div>
    </Panel>
  )
}

/**
 * The question, and everything the answer decides.
 *
 * Two cards because it is a choice between two kinds of ending (§4): keep
 * everything the dashboard made where a reinstall finds it, or wipe it. Under
 * the cards is what each one removes and what it leaves, read off the server
 * rather than written in prose — the number of containers, the size of the
 * data — because "your deployments" is abstract and "6 containers · 3
 * volumes" is not. A wipe asks for the server's name, as dropping a database
 * asks for the database's: it is the one way off this page that deletes what
 * nothing brings back. Whether the backups go too is asked on its own, and
 * off until asked, since the archives are copies of the operator's data
 * rather than the dashboard's.
 *
 * Mounted only while open, so a second opening starts from "keep" with an
 * empty phrase rather than from whatever was typed the first time.
 */
export function UninstallDialog({
  onOpenChange,
  loadPlan,
  onStart,
}: {
  onOpenChange: (open: boolean) => void
  loadPlan: (signal?: AbortSignal) => Promise<UninstallPlan>
  onStart: (mode: UninstallMode, deleteBackups: boolean, phrase: string) => Promise<unknown>
}) {
  const fetchPlan = useCallback((signal: AbortSignal) => loadPlan(signal), [loadPlan])
  // Read once on opening; the slow interval only keeps a dialog left open
  // from describing a server that has since changed.
  const { data: plan, error } = usePoll(fetchPlan, 60_000)
  const [mode, setMode] = useState<UninstallMode>("keep")
  const [deleteBackups, setDeleteBackups] = useState(false)
  const [typed, setTyped] = useState("")
  const [busy, setBusy] = useState(false)

  const wipe = mode === "wipe"
  const matches = !wipe || (plan !== undefined && typed === plan.phrase)
  const ready = plan !== undefined && matches && !busy

  const run = async () => {
    if (!ready) return
    setBusy(true)
    try {
      await onStart(mode, wipe && deleteBackups, typed)
      onOpenChange(false)
    } catch (err) {
      notify.error(
        "Uninstall did not start",
        err instanceof ApiError ? err.message : errorMessage(err),
      )
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      size="lg"
      onOpenChange={(open) => !busy && onOpenChange(open)}
      title="Uninstall Just Dashboard"
      description="Choose whether the deployments and data stay for a reinstall, then uninstall."
      footer={
        <>
          <p className="mr-auto min-w-0 text-hint text-muted-foreground">
            This page loses the dashboard partway through.
          </p>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={run} disabled={!ready} pending={busy}>
            {wipe ? "Wipe and uninstall" : "Uninstall"}
          </Button>
        </>
      }
    >
      {error && !plan ? (
        <ErrorState error={error} />
      ) : !plan ? (
        <div className="space-y-3">
          <Skeleton className="h-12 w-2/3" />
          <Skeleton className="h-28 w-full" />
          <Skeleton className="h-40 w-full" />
        </div>
      ) : (
        <div className="space-y-5">
          <div className="flex min-w-0 items-center gap-3">
            <span
              aria-hidden
              className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-hairline bg-surface-sunken"
            >
              <LogoGlyph className="h-4 w-auto text-brand" />
            </span>
            <div className="min-w-0 space-y-0.5">
              <p className="truncate text-body font-medium">Just Dashboard on {plan.phrase}</p>
              <FormFacts>
                <FormFact label="Checkout" mono>
                  {plan.dir}
                </FormFact>
                <FormFact label="Data" mono>
                  {plan.data.path}
                </FormFact>
              </FormFacts>
            </div>
          </div>

          <ChoiceGrid columns={2}>
            <ChoiceCard
              verb="Keep the deployments and data"
              title="Keep for a reinstall"
              mark={Archive}
              description="Removes the dashboard. Deployments keep running; data, backups and the key stay for the next install."
              selected={mode === "keep"}
              disabled={busy}
              onClick={() => setMode("keep")}
            />
            <ChoiceCard
              verb="Wipe the deployments and data"
              title="Wipe everything"
              mark={Trash}
              description="Also removes every deployment with its volumes, and deletes the dashboard's data and key."
              index={1}
              selected={mode === "wipe"}
              disabled={busy}
              onClick={() => setMode("wipe")}
            />
          </ChoiceGrid>

          <Consequences plan={plan} mode={mode} deleteBackups={deleteBackups} />

          {wipe && plan.backups && (
            <OptionList>
              <OptionRow
                tone="danger"
                title="Delete the backups too"
                hint={
                  <>
                    <span className="font-mono">{plan.backups.path}</span> ·{" "}
                    {usageLine(plan.backups)}. Off keeps them for whatever comes next.
                  </>
                }
                checked={deleteBackups}
                disabled={busy}
                onCheckedChange={setDeleteBackups}
              />
            </OptionList>
          )}

          {wipe && (
            <Field
              htmlFor="uninstall-phrase"
              label={
                <>
                  Type <code className="font-mono text-foreground">{plan.phrase}</code> to confirm
                </>
              }
            >
              <InputGroup>
                <InputGroupInput
                  id="uninstall-phrase"
                  autoComplete="off"
                  spellCheck={false}
                  value={typed}
                  disabled={busy}
                  onChange={(e) => setTyped(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && run()}
                  className="font-mono"
                  placeholder="Type the phrase above"
                />
                {matches && (
                  <InputGroupAddon align="inline-end">
                    <Check aria-hidden className="text-success" />
                  </InputGroupAddon>
                )}
              </InputGroup>
            </Field>
          )}
        </div>
      )}
    </Modal>
  )
}

/**
 * What the chosen ending removes and what it leaves, as two short lists of
 * the things themselves. Removed is marked in git's colour for a deletion
 * (§3): it is a change about to be made, not a state.
 */
function Consequences({
  plan,
  mode,
  deleteBackups,
}: {
  plan: UninstallPlan
  mode: UninstallMode
  deleteBackups: boolean
}) {
  const wipe = mode === "wipe"
  const deployments = plan.deployments
  const deploymentsLine = plan.deploymentsError
    ? `could not be counted: ${plan.deploymentsError}`
    : footprintLine(deployments)
  // Counted by project where the database knows them; a host whose
  // deployments are known only by their labels is still told what goes.
  const projects =
    deployments.projects > 0 ? plural(deployments.projects, "deployment") : "Deployments"
  const data = (
    <>
      <span className="font-mono">{plan.data.path}</span> · {usageLine(plan.data)} — accounts,
      settings, secrets, deployment history, audit trail
    </>
  )
  const backups = plan.backups && (
    <>
      <span className="font-mono">{plan.backups.path}</span> · {usageLine(plan.backups)}
    </>
  )

  const removed: Line[] = [
    {
      title: "The dashboard",
      detail: "its three containers, its two images and any open web-terminal sessions",
    },
    ...(wipe
      ? [
          {
            title: projects,
            detail: deploymentsLine,
            more: <FootprintList footprint={deployments} />,
          },
          { title: "Its data", detail: data },
          {
            title: "Its key",
            detail: (
              <>
                <span className="font-mono">{plan.envPath}</span> and its copies — what the data is
                sealed with
              </>
            ),
          },
        ]
      : []),
    ...(wipe && deleteBackups && backups ? [{ title: "Backups", detail: backups }] : []),
  ]
  const kept: Line[] = [
    ...(!wipe
      ? [
          { title: `${projects}, still running`, detail: deploymentsLine },
          { title: "Its data", detail: data },
          {
            title: "Its key",
            detail: (
              <>
                <span className="font-mono">{plan.envPath}</span>, with a copy beside the data
              </>
            ),
          },
        ]
      : []),
    ...((!wipe || !deleteBackups) && backups ? [{ title: "Backups", detail: backups }] : []),
    ...(wipe && plan.keptCache
      ? [
          {
            title: "The nginx cache",
            detail: (
              <>
                <span className="font-mono">{plan.keptCache}</span> — a site on this server still
                caches into it
              </>
            ),
          },
        ]
      : []),
    {
      title: "Everything else it set up",
      detail:
        "databases it created, proxy sites, firewall rules, WireGuard and other network settings",
    },
    {
      title: "The checkout",
      detail: (
        <>
          <span className="font-mono">{plan.dir}</span>
          {wipe ? (
            " — delete it yourself afterwards"
          ) : (
            <>
              {" "}
              — run <code className="font-mono">sudo ./install.sh</code> in it to reinstall
            </>
          )}
        </>
      ),
    },
  ]

  return (
    <div className="grid min-w-0 gap-4 sm:grid-cols-2">
      <LineList label="Removed" tone="removed" lines={removed} />
      <LineList label="Kept" tone="kept" lines={kept} />
    </div>
  )
}

type Line = { title: React.ReactNode; detail: React.ReactNode; more?: React.ReactNode }

function LineList({
  label,
  tone,
  lines,
}: {
  label: string
  tone: "removed" | "kept"
  lines: Line[]
}) {
  const Mark: Icon = tone === "removed" ? Trash : Check
  return (
    <section className="min-w-0 space-y-2" aria-label={label}>
      <Tag style={tone === "removed" ? { color: "var(--git-deleted)" } : undefined}>{label}</Tag>
      <ul className="space-y-2.5">
        {lines.map((line, index) => (
          <li key={index} className="flex min-w-0 items-start gap-2">
            <Mark
              aria-hidden
              className={cn(
                "mt-0.5 size-3.5 shrink-0",
                tone === "removed" ? "text-[var(--git-deleted)]" : "text-muted-foreground",
              )}
            />
            <div className="min-w-0 space-y-0.5">
              <p className="text-body leading-snug font-medium">{line.title}</p>
              <p className="text-hint leading-relaxed break-words text-muted-foreground">
                {line.detail}
              </p>
              {line.more}
            </div>
          </li>
        ))}
      </ul>
    </section>
  )
}

/** Every name a wipe removes, folded: the count is what is read, the list is what is checked. */
function FootprintList({ footprint }: { footprint: DeploymentFootprint }) {
  const groups: [string, string[]][] = (
    [
      ["Containers", footprint.containers.map((item) => item.name)],
      ["Volumes", footprint.volumes.map((item) => item.name)],
      ["Folders", footprint.paths.map((item) => item.name)],
      ["Images", footprint.images.map((item) => item.name)],
      ["Networks", footprint.networks.map((item) => item.name)],
      ["Routes", footprint.routes.map((item) => item.name)],
      ["Ingress", footprint.ingress ? [footprint.ingress.name] : []],
    ] as [string, string[]][]
  ).filter(([, names]) => names.length > 0)
  if (groups.length === 0) return null
  return (
    <Disclosure quiet summary="Every name">
      <Well className="max-h-48 space-y-2 text-hint">
        {groups.map(([label, names]) => (
          <div key={label}>
            <p className="font-sans text-muted-foreground">{label}</p>
            {names.map((name) => (
              <p key={name} className="break-all">
                {name}
              </p>
            ))}
          </div>
        ))}
      </Well>
    </Disclosure>
  )
}

function usageLine(usage: DirectoryUsage) {
  return `${usage.partial ? "at least " : ""}${bytes(usage.bytes)} in ${plural(usage.files, "file")}`
}

/**
 * An uninstall, watched for as long as there is anything to watch.
 *
 * Until the stack comes down this is the restart's progress: a mark, a
 * headline, a bar of the stages and the transcript live under them. Then the
 * page loses the API — which is the uninstall working, not failing — and the
 * card stops pretending to follow it. It says the dashboard has stopped, that
 * the rest finishes on the server, and gives the three commands an operator
 * now needs: where the transcript continues, and how to reinstall or clear up.
 */
export function UninstallProgress({
  run,
  log,
  lost,
  onDismiss,
  className,
}: {
  run: UninstallRun
  log?: string
  lost: boolean
  onDismiss?: () => void
  className?: string
}) {
  const live = isLive(run)
  const gone = stackGone(run, lost)
  const stages = uninstallStages(run, lost)
  const took =
    run.finishedAt &&
    duration((new Date(run.finishedAt).getTime() - new Date(run.startedAt).getTime()) / 1000)

  return (
    <div className={cn("min-w-0 space-y-4", className)}>
      <div className="flex min-w-0 items-start gap-3">
        <span className="pt-px">
          {live && !gone && <Spinner className="size-4 text-primary" />}
          {gone && <Information className="size-4 text-muted-foreground" />}
          {run.status === "success" && <CheckCircle className="size-4 text-success" />}
          {run.status === "failed" && <CrossCircle className="size-4 text-destructive" />}
        </span>
        <div className="min-w-0 flex-1 space-y-1">
          <p className="text-sm leading-tight font-semibold tracking-tight">
            {uninstallHeadline(run, lost)}
          </p>
          <p className="text-hint text-muted-foreground">
            {run.mode === "wipe" ? "wiping" : "keeping the deployments and data"} · started{" "}
            {relativeTime(run.startedAt)}
            {run.actor ? ` by ${run.actor}` : ""}
            {took ? ` · took ${took}` : ""}
          </p>
        </div>
        {!live && onDismiss && (
          <Button variant="ghost" size="sm" onClick={onDismiss}>
            Dismiss
          </Button>
        )}
      </div>

      <Outcome run={run} live={live} gone={gone} />

      <RunPhases phases={phaseStates(stages.labels, stages.at, stages.outcome)} />

      {log && (
        <RunTranscript
          text={log}
          // Still running once the page has lost it — on the server, where
          // this copy of its end can no longer follow.
          live={live}
          source="/dashboard/uninstall/log"
          label="Uninstall transcript"
        />
      )}
    </div>
  )
}

function Outcome({ run, live, gone }: { run: UninstallRun; live: boolean; gone: boolean }) {
  const warnings = run.warnings ?? []
  if (gone) {
    return (
      <div className="space-y-3">
        <Note>
          The page lost the dashboard, which is what stopping it does. The rest finishes on the
          server within a minute, where nothing here can watch it.
        </Note>
        <Well className="space-y-1 text-hint break-all">
          <p className="font-sans text-muted-foreground"># follow it to the end</p>
          <p>sudo tail -f {DURABLE_LOG}</p>
          {run.mode === "keep" ? (
            <>
              <p className="pt-2 font-sans text-muted-foreground">
                # reinstall, keeping everything
              </p>
              <p>cd {run.dir} && sudo ./install.sh</p>
            </>
          ) : (
            <>
              <p className="pt-2 font-sans text-muted-foreground">
                # the source checkout is left; delete it when you are done with it
              </p>
              <p>sudo rm -rf {run.dir}</p>
            </>
          )}
        </Well>
      </div>
    )
  }
  if (live) {
    return (
      <Note>
        Keep this tab open: it follows the uninstall until the dashboard stops, then says what is
        left to do.
      </Note>
    )
  }
  return (
    <div className="space-y-2">
      {run.status === "failed" && run.error && <Detail tone="danger">{run.error}</Detail>}
      {run.status === "failed" && (run.phase === "pausing" || run.phase === "deployments") && (
        <Note>
          Nothing past that step ran. The dashboard restarted to resume the deployments and
          schedules it had paused.
        </Note>
      )}
      {warnings.length > 0 && <Detail tone="warning">{warnings.join("\n")}</Detail>}
    </div>
  )
}

function Note({ children }: { children: React.ReactNode }) {
  return (
    <p className="flex min-w-0 items-start gap-2 text-xs leading-relaxed text-muted-foreground">
      <Information aria-hidden className="mt-0.5 size-3.5 shrink-0" />
      <span className="min-w-0">{children}</span>
    </p>
  )
}

/** The runner's own sentence about an outcome, coloured by it — text, not a box. */
function Detail({ tone, children }: { tone: "warning" | "danger"; children: React.ReactNode }) {
  return (
    <p
      className={cn(
        "border-l-2 pl-3 font-mono text-hint leading-relaxed break-words whitespace-pre-wrap",
        tone === "danger" ? "border-destructive text-destructive" : "border-warning text-warning",
      )}
    >
      {children}
    </p>
  )
}
