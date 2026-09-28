"use client"

import { useState } from "react"
import { Logs, Play, Warning } from "@/components/icons"
import { del, get, put } from "@/lib/api"
import {
  hookFailureText,
  renewalReload,
  runPhrase,
  runTime,
  sinceDay,
  standingFailures,
} from "@/lib/certificates"
import { timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { CertbotState, RenewalHealth, RenewalLog, RenewalRun } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { OptionList, OptionRow } from "@/components/form"
import { Well } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { VerbBar, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

/**
 * What the renewal schedule did, not only whether there is one.
 *
 * An active timer says certbot is asked to renew twice a day, and nothing
 * about whether it does: the host this was built on had certbot.timer active
 * and every run of certbot.service failing for months, while the page said
 * "Scheduled" in green. The last run's result, the certificate it failed on
 * in certbot's own words, and the next run are what an operator needs; "Run
 * now" starts the timer's own service, so systemd records the run and this
 * reading changes with it.
 */
export function RenewalRecord({
  state,
  admin,
  certbotBusy,
  running,
  onRun,
  onShowLog,
}: {
  state: CertbotState
  admin: boolean
  /** A certbot run is on screen: another would fail on its lock. */
  certbotBusy: boolean
  /** The run on screen is this one. */
  running: boolean
  onRun: () => void
  onShowLog: () => void
}) {
  const health = state.health
  if (!state.autoRenew || !health) return null
  const verbs: Verb[] = [
    ...(health.service
      ? [
          {
            key: "run",
            label: "Run now",
            icon: Play,
            inline: true,
            disabled: certbotBusy || health.state === "running",
            run: onRun,
          },
        ]
      : []),
    { key: "log", label: "Show log", icon: Logs, inline: true, run: onShowLog },
  ]
  const hooks = health.hookFailures ?? []
  const actions = admin && (
    <div className="flex flex-wrap items-center gap-2">
      {running && <Status state="activating" label="Running…" />}
      <VerbBar verbs={verbs} menuLabel="More renewal actions" />
    </div>
  )
  if (health.state === "ok" && hooks.length > 0 && !running) {
    // certbot only warns when a hook fails, so the run passed: the hook's
    // own words are what says nginx kept the old certificate.
    return (
      <Notice tone="warning" icon={Warning} title="A renewal hook failed" className="mt-2">
        <div className="space-y-2">
          <p>
            {health.service ?? "The renewal"} passed
            {health.lastRun ? ` ${runPhrase(health.lastRun)}` : ""}, but certbot only warns when a
            hook it runs fails.
          </p>
          <HookFailures health={health} />
          {health.error && <p className="break-words text-muted-foreground">{health.error}</p>}
          {actions}
        </div>
      </Notice>
    )
  }
  if (health.state === "failed") {
    const standing = standingFailures(health)
    return (
      <Notice tone="danger" icon={Warning} title="The last renewal failed" className="mt-2">
        <div className="space-y-2">
          <p>
            {health.service ?? "The renewal"}
            {health.exitStatus ? ` exited ${health.exitStatus}` : " failed"}
            {health.lastRun ? ` ${runPhrase(health.lastRun)}` : ""}.
            {health.failingSince && ` Every run since ${sinceDay(health.failingSince)} has failed.`}
          </p>
          {standing.length > 0 ? (
            <ul className="space-y-1">
              {standing.map((f) => (
                <li key={f.lineage} className="break-words">
                  <span className="font-medium">{f.lineage}</span>: {f.reason}
                </li>
              ))}
            </ul>
          ) : (
            health.reason && <p className="break-words">{health.reason}</p>
          )}
          <HookFailures health={health} />
          {health.error && <p className="break-words text-muted-foreground">{health.error}</p>}
          {actions}
        </div>
      </Notice>
    )
  }
  const reading = recordReading(health)
  return (
    <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2 pt-2 pb-3">
      <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
        {running || health.state === "running" ? (
          <Status state="activating" label="Running…" />
        ) : (
          <Status verdict={reading.verdict} label={reading.label} />
        )}
        {reading.hint && <span className="text-hint text-muted-foreground">{reading.hint}</span>}
      </div>
      {admin && <VerbBar verbs={verbs} menuLabel="More renewal actions" />}
      {hooks.length > 0 && (
        <div className="w-full text-hint text-warning">
          <HookFailures health={health} />
        </div>
      )}
      {health.error && (
        <p className="w-full text-hint break-words text-muted-foreground">{health.error}</p>
      )}
    </div>
  )
}

/** The hooks the last run ran that failed, each in a line. */
function HookFailures({ health }: { health: RenewalHealth }) {
  const hooks = health.hookFailures ?? []
  if (hooks.length === 0) return null
  return (
    <ul aria-label="Failed hooks" className="space-y-1">
      {hooks.map((hook, i) => (
        <li key={i} className="break-words">
          {hookFailureText(hook)}
        </li>
      ))}
    </ul>
  )
}

function recordReading(health: RenewalHealth): {
  verdict: "ok" | "warning" | "notice"
  label: string
  hint?: string
} {
  const last = health.lastRun ? runTime(health.lastRun) : undefined
  const next = health.nextRun ? `next run ${runTime(health.nextRun)}` : undefined
  switch (health.state) {
    case "ok":
      return {
        verdict: "ok",
        label: last ? `Last run ${last} passed` : "Last run passed",
        hint: next,
      }
    case "recovered":
      return {
        verdict: "warning",
        label: last ? `Last run ${last} failed` : "Last run failed",
        hint: "renewed since",
      }
    case "never":
      return { verdict: "notice", label: "Not run yet", hint: next }
    default:
      return { verdict: "notice", label: "No run on record", hint: next }
  }
}

/**
 * The renewal schedule's own record, newest run first: the timer's service
 * journal for the last fortnight, or certbot's log of its last renewal on a
 * host that renews from cron. systemd's lines about each run are quieter than
 * certbot's, and the ones that say why it failed are marked.
 */
export function RenewalLogPanel({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const log = usePoll<RenewalLog>(
    (signal) => get("/certificates/renewal/log", undefined, signal),
    0,
    [open],
    { enabled: open },
  )
  return (
    <SidePanel
      open={open}
      onOpenChange={onOpenChange}
      title="Renewal log"
      description="What certbot printed on each renewal run."
      width="lg"
      initialFocus="body"
    >
      {log.loading ? (
        <LoadingRows rows={4} />
      ) : log.error ? (
        <ErrorState error={log.error} onRetry={log.refresh} />
      ) : log.data && log.data.runs.length > 0 ? (
        <div className="space-y-6">
          <p className="text-hint text-muted-foreground">
            <code className="font-mono">{log.data.source}</code>
            {log.data.source.endsWith(".service") ? ", the last fourteen days." : "."}
          </p>
          {log.data.runs.map((run, i) => (
            <RunLines key={`${i}:${run.start}`} run={run} />
          ))}
        </div>
      ) : (
        <EmptyState
          icon={Logs}
          title="No renewal run on record"
          description="The journal keeps a fortnight of runs here, and certbot's log only the renewals it has made."
          className="mt-2"
        />
      )}
    </SidePanel>
  )
}

function RunLines({ run }: { run: RenewalRun }) {
  return (
    <section aria-label={`Run at ${timestamp(run.start)}`} className="space-y-2">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <span className="text-body font-medium">{timestamp(run.start)}</span>
        {run.result === "failed" ? (
          <Status verdict="critical" label="failed" />
        ) : run.result === "succeeded" ? (
          <Status verdict="ok" label="passed" />
        ) : (
          <Status verdict="notice" label="no result" />
        )}
      </div>
      <Well className="space-y-0.5 break-words whitespace-pre-wrap">
        {run.lines.map((line, i) => (
          <div
            key={i}
            className={cn(
              line.error && "text-destructive",
              line.systemd && !line.error && "text-muted-foreground",
            )}
          >
            {line.text}
          </div>
        ))}
      </Well>
    </section>
  )
}

/**
 * "Reload nginx after every renewal": the deploy hook certbot runs after it
 * renews a certificate. certbot reloads nginx itself only for a certificate
 * its nginx plugin installed; one issued through a folder, standalone or DNS
 * is renewed on disk while nginx keeps serving the old one until it expires.
 * The hook tests the configuration before it reloads. A copy changed by hand
 * no longer does what the switch says, and a file of somebody else's at the
 * same name is left alone.
 */
export function ReloadHookOption({
  state,
  onChanged,
}: {
  state: CertbotState
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState(false)
  const hook = state.reloadHook
  if (!hook) return null
  const file = hook.path.split("/").pop()

  const install = async (restore: boolean) => {
    setBusy(true)
    try {
      await put("/certificates/renewal-hook")
      notify.success(restore ? "The hook is restored" : "nginx reloads after every renewal", {
        description:
          "certbot runs the hook after it renews a certificate: nginx -t, then a reload.",
      })
      onChanged()
    } catch (err) {
      notify.error(restore ? "The hook was not restored" : "The hook was not installed", err)
    } finally {
      setBusy(false)
    }
  }
  const remove = () =>
    confirm({
      title: "Stop reloading nginx after renewals",
      confirmLabel: "Remove the hook",
      description: (
        <p>
          <code className="font-mono break-all">{hook.path}</code> is deleted. certbot keeps
          renewing, but a certificate it renews reaches browsers only once something reloads nginx.
        </p>
      ),
      action: async () => {
        await del("/certificates/renewal-hook")
        onChanged()
      },
    })

  // What certbot does about nginx on its own, without the hook.
  const readable = state.certs.filter((c) => !c.error)
  const withoutReload = readable.filter(
    (c) => renewalReload(c, { ...state, reloadHook: undefined }) === "none",
  ).length
  const others = hook.others.length
    ? ` certbot also runs ${hook.others.join(", ")} after each renewal.`
    : ""
  const hint =
    hook.state === "installed"
      ? `certbot runs ${file} after each renewal: nginx -t, then a reload.${others}`
      : hook.state === "modified"
        ? `${file} was changed by hand, or is no longer executable, so it may not do what this says.${others}`
        : hook.state === "foreign"
          ? `${hook.path} is not this dashboard's file, so it is left as it is.${others}`
          : readable.length > 0 && withoutReload === 0
            ? `certbot's nginx plugin already reloads nginx for these; the hook covers any issued another way.${others}`
            : withoutReload > 0
              ? `certbot reloads nginx itself only for certificates issued through nginx. ${withoutReload} of ${readable.length} here ${withoutReload === 1 ? "is" : "are"} not.${others}`
              : `certbot reloads nginx itself only for certificates issued through nginx.${others}`

  return (
    <div className="pb-3">
      <OptionList>
        <OptionRow
          title="Reload nginx after every renewal"
          hint={hint}
          tone={hook.state === "modified" ? "warning" : "default"}
          checked={hook.state === "installed" || hook.state === "modified"}
          disabled={busy || hook.state === "foreign"}
          onCheckedChange={(on) => (on ? install(false) : remove())}
        />
      </OptionList>
      {hook.state === "modified" && (
        <Button
          size="xs"
          variant="outline"
          className="mt-1"
          pending={busy}
          onClick={() => install(true)}
        >
          Restore the hook
        </Button>
      )}
      {dialog}
    </div>
  )
}
