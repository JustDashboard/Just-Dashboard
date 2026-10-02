"use client"

import { useCallback, useState } from "react"
import { useRouter } from "next/navigation"
import {
  Archive,
  LockClosed,
  Play,
  RotateClockwise,
  SettingsGear,
  StopCircle,
  Trash,
} from "@/components/icons"
import { ApiError, del, get, post, put } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { DbAccessChange, DbFleetEntry, Job } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { FormFact } from "@/components/form"
import { Notice } from "@/components/state"
import type { Verb } from "@/components/verbs"
import { sectionHref } from "@/components/database/engine"
import { powerOffers, whereWord } from "@/components/database/fleet/fleet"
import type { DbPowerAction, DbPowerResponse } from "@/components/database/fleet/types"
import type { FleetData } from "@/components/database/fleet/use-fleet"
import {
  createRefusal,
  useRefusal,
  type FleetConfirm,
  type Refusal,
} from "@/components/database/fleet/use-fleet-confirm"
import { EngineMark } from "@/components/database/kit"
import { useDatabases } from "@/components/database/shell/databases-context"

/** How long a started server is given to answer before its card stops saying it is starting. */
const SETTLE_MS = 60_000

const POWER: Record<
  DbPowerAction,
  { label: string; progressive: string; done: string; icon: Verb["icon"] }
> = {
  start: { label: "Start", progressive: "Starting", done: "started", icon: Play },
  stop: { label: "Stop", progressive: "Stopping", done: "stopped", icon: StopCircle },
  restart: {
    label: "Restart",
    progressive: "Restarting",
    done: "restarted",
    icon: RotateClockwise,
  },
}

/**
 * What can be done to a database from the control center, with each card's
 * own "this is happening" state attached: start, stop or restart the server
 * behind it, take a dump now, bring a published port back to this machine,
 * forget the connection.
 *
 * A stop is given ninety seconds by the server and a dump can take minutes,
 * while the fleet goes on reporting the old state for all of them. Without
 * the participle a card answers a press by doing nothing and then jumping,
 * which is indistinguishable from a button that did not work.
 *
 * A control the role cannot use is not offered (the capability each route
 * asks for is the one checked here), and the server decides again.
 */
export function useFleetControl({
  data,
  confirm,
  onForgotten,
}: {
  data: FleetData
  confirm: FleetConfirm
  /** The saved connections changed: discovery has something new to say. */
  onForgotten?: () => void
}) {
  const { can } = useAuth()
  const router = useRouter()
  const { engineFor, refresh: refreshConnections } = useDatabases()
  const [pending, setPending] = useState<Record<number, string>>({})
  const [settling, setSettling] = useState<Record<number, string>>({})
  const refreshFleet = data.fleet.refresh
  const refreshSummary = data.summary.refresh

  const mark = useCallback(
    (id: number, word?: string) =>
      setPending((held) => {
        const next = { ...held }
        if (word) next[id] = word
        else delete next[id]
        return next
      }),
    [],
  )

  /** The control a confirmation gives the keyboard back to: the database's own menu. */
  const menuOf = (entry: DbFleetEntry) => `Actions for ${entry.name}`

  const subjectOf = (entry: DbFleetEntry): ConfirmRequest["subject"] => {
    const engine = engineFor(entry)
    const where = whereWord(entry)
    return {
      mark: <EngineMark engine={engine} size="sm" />,
      name: entry.name,
      facts: (
        <>
          <FormFact label="Engine">
            {engine.label}
            {entry.versionNumber ? ` ${entry.versionNumber}` : ""}
          </FormFact>
          <FormFact label={where.label} mono={where.mono}>
            {where.text}
          </FormFact>
        </>
      ),
    }
  }

  const runPower = async (entry: DbFleetEntry, action: DbPowerAction) => {
    const words = POWER[action]
    mark(entry.id, words.progressive)
    try {
      const done = await post<DbPowerResponse>(`/databases/${entry.id}/power`, { action })
      notify.success(`${entry.name} ${words.done}`, { description: done.target })
      if (action !== "stop") {
        // The container is up before the engine inside it answers, and the
        // fleet reads it as unreachable for those seconds: the card goes on
        // saying it is starting until it is.
        setSettling((held) => ({ ...held, [entry.id]: words.progressive }))
        window.setTimeout(
          () =>
            setSettling((held) => {
              const next = { ...held }
              delete next[entry.id]
              return next
            }),
          SETTLE_MS,
        )
      }
    } catch (err) {
      notify.error(`Could not ${action} ${entry.name}`, err)
    } finally {
      mark(entry.id)
      refreshFleet()
    }
  }

  /** Start runs at once; a stop or a restart drops sessions, and asks first. */
  const power = (entry: DbFleetEntry, action: DbPowerAction) => {
    if (action === "start") {
      void runPower(entry, action)
      return
    }
    const where = whereWord(entry)
    confirm(
      {
        title: `${POWER[action].label} ${entry.name}`,
        subject: subjectOf(entry),
        description: (
          <>
            <p>
              {action === "stop"
                ? `Stops ${where.text}. Every open session is dropped and the database answers nothing until it is started again.`
                : `Restarts ${where.text}. Every open session is dropped and the database is away while it comes back.`}
            </p>
            {entry.consumers > 0 && (
              <p>
                {plural(entry.consumers, "deployment environment")}{" "}
                {entry.consumers === 1 ? "is" : "are"} bound to it and will lose{" "}
                {entry.consumers === 1 ? "its" : "their"} database for as long.
              </p>
            )}
          </>
        ),
        confirmLabel: POWER[action].label,
        // The request is held for as long as the server takes to shut down;
        // the card says so, and the dialog does not stay open over it.
        action: async () => {
          void runPower(entry, action)
          return "reported"
        },
      },
      menuOf(entry),
    )
  }

  /** Take a dump now and watch the job to its end, so a failure is said. */
  const backup = async (entry: DbFleetEntry) => {
    mark(entry.id, "Backing up")
    try {
      const started = await post<Job>(`/databases/${entry.id}/backup`, {})
      refreshSummary()
      let job = started
      while (job.status === "running") {
        await new Promise((resolve) => window.setTimeout(resolve, 1500))
        job = (await get<{ job: Job }>(`/jobs/${encodeURIComponent(started.id)}`)).job
      }
      if (job.status === "succeeded") notify.success(`${entry.name} backed up`)
      else notify.error(`The backup of ${entry.name} did not finish`, job.error || job.status)
    } catch (err) {
      notify.error(`Could not back up ${entry.name}`, err)
    } finally {
      mark(entry.id)
      refreshSummary()
      refreshFleet()
    }
  }

  /** Whether the port's binding can be changed from here: a container of its own, not compose's. */
  const restrictable = (entry: DbFleetEntry) =>
    entry.exposure === "public" && entry.source === "docker" && !entry.composeProject

  const restrict = (entry: DbFleetEntry) => {
    confirm(
      {
        title: `Restrict ${entry.name} to this server`,
        subject: subjectOf(entry),
        description: (
          <>
            <p>
              Recreates {whereWord(entry).text} with its port bound to this server only and closes
              the firewall rule opened for it. The data is kept; every open session is dropped while
              the container is replaced.
            </p>
            <p>Anything that reaches it from another machine stops reaching it.</p>
          </>
        ),
        confirmLabel: "Restrict",
        action: async () => {
          void (async () => {
            mark(entry.id, "Restricting")
            try {
              const change = await put<DbAccessChange>(`/databases/${entry.id}/access`, {
                exposure: "local",
              })
              if (change.firewallError) {
                notify.warning(`${entry.name} is bound to this server, the firewall rule is not`, {
                  description: change.firewallError,
                })
              } else notify.success(`${entry.name} is reachable from this server only`)
            } catch (err) {
              notify.error(`Could not restrict ${entry.name}`, err)
            } finally {
              mark(entry.id)
              refreshFleet()
            }
          })()
          return "reported"
        },
      },
      menuOf(entry),
    )
  }

  const forget = (entry: DbFleetEntry) => {
    // The server puts the found server a connection came from on the ignore
    // list only when this was the last connection to it, and never for a
    // file: the dialog promises it only where it will happen.
    const ignores =
      Boolean(entry.origin) &&
      !entry.origin.startsWith("file:") &&
      !data.entries.some((other) => other.id !== entry.id && other.origin === entry.origin)
    const refusal = createRefusal()
    confirm(
      {
        title: `Forget ${entry.name}`,
        subject: subjectOf(entry),
        description: <ForgetBody entry={entry} ignores={ignores} refusal={refusal} />,
        confirmLabel: "Forget",
        action: async () => {
          refusal.say(undefined)
          try {
            await del(`/databases/${entry.id}`)
          } catch (err) {
            if (err instanceof ApiError && err.code === "database_linked") {
              refusal.say(
                `Remove the link under that deployment's Settings › Databases, then forget ${entry.name} here.`,
              )
              // The dialog announces what it is thrown; this is the sentence
              // it prints, with the way out left to the dialog itself.
              throw new ApiError(
                err.status,
                err.code,
                `${entry.name} is linked to a deployment through a managed database network.`,
              )
            }
            throw err
          }
          notify.success(`Forgot ${entry.name}`)
          refreshConnections()
          refreshFleet()
          refreshSummary()
          data.topology.refresh()
          onForgotten?.()
          return "reported"
        },
      },
      menuOf(entry),
    )
  }

  const allowed = {
    start: can("service.control"),
    stop: can("service.control") && can("destructive"),
    restart: can("service.control") && can("destructive"),
    backup: can("service.control"),
    restrict: can("system.admin") && can("destructive"),
    forget: can("system.admin") && can("destructive"),
  }

  /** What a card says it is doing, when it is doing something. */
  const busyWord = (entry: DbFleetEntry): string | undefined =>
    pending[entry.id] ??
    (entry.state !== "running" ? settling[entry.id] : undefined) ??
    (data.backups.get(entry.id)?.running ? "Backing up" : undefined)

  /** Whether Start is the thing to offer on a card that is down. */
  const canStart = (entry: DbFleetEntry) => allowed.start && powerOffers(entry).start
  const canBackup = (entry: DbFleetEntry) =>
    allowed.backup && !entry.broken && entry.ok && data.dumps(entry)
  const canRestrict = (entry: DbFleetEntry) => allowed.restrict && restrictable(entry)

  /** The card's menu: the server's power, then what is done with the database as a thing kept. */
  const verbsFor = (entry: DbFleetEntry): Verb[] => {
    const busy = Boolean(busyWord(entry))
    const offers = powerOffers(entry)
    const verbs: Verb[] = []
    for (const action of ["start", "stop", "restart"] as const) {
      if (!offers[action] || !allowed[action]) continue
      verbs.push({
        key: action,
        label: POWER[action].label,
        icon: POWER[action].icon,
        progressive: POWER[action].progressive,
        group: "Server",
        disabled: busy,
        run: () => power(entry, action),
      })
    }
    if (canBackup(entry)) {
      verbs.push({
        key: "backup",
        label: "Back up now",
        icon: Archive,
        progressive: "Backing up",
        group: "Database",
        disabled: busy,
        run: () => void backup(entry),
      })
    }
    if (canRestrict(entry)) {
      verbs.push({
        key: "restrict",
        label: "Restrict to this server",
        icon: LockClosed,
        progressive: "Restricting",
        group: "Database",
        disabled: busy,
        run: () => restrict(entry),
      })
    }
    verbs.push({
      key: "settings",
      label: "Settings",
      icon: SettingsGear,
      group: "Database",
      run: () => router.push(sectionHref(entry.id, "settings")),
    })
    if (allowed.forget) {
      verbs.push({
        key: "forget",
        label: "Forget",
        icon: Trash,
        danger: true,
        group: "Database",
        disabled: busy,
        run: () => forget(entry),
      })
    }
    return verbs
  }

  return { power, backup, restrict, forget, busyWord, canStart, canBackup, canRestrict, verbsFor }
}

export type FleetControl = ReturnType<typeof useFleetControl>

/**
 * What forgetting a connection does, and — once the server has turned it down
 * — why it did not.
 */
function ForgetBody({
  entry,
  ignores,
  refusal,
}: {
  entry: DbFleetEntry
  /** The server it was found as is put on the ignore list with it. */
  ignores: boolean
  refusal: Refusal
}) {
  const refused = useRefusal(refusal)
  return (
    <>
      {refused && (
        <Notice tone="danger" title="It is linked to a deployment, so it was not forgotten">
          {refused}
        </Notice>
      )}
      <p>
        Removes the saved connection and its stored password from the dashboard. The database itself
        and its data are not touched
        {ignores
          ? ", and it is put on the ignore list so discovery does not connect it again."
          : "."}
      </p>
      {entry.consumers > 0 && !refused && (
        <p>
          {plural(entry.consumers, "deployment environment")} {entry.consumers === 1 ? "is" : "are"}{" "}
          bound to it. If one of them is linked through a managed database network the server
          refuses, and that link has to be removed in the deployment&apos;s settings first.
        </p>
      )}
    </>
  )
}
