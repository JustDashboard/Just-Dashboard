"use client"

import { createContext, useContext, useEffect, useMemo, useRef, useState } from "react"
import { errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { FormFact, FormNote, Statement } from "@/components/form"
import { RunPhases } from "@/components/run-phases"
import { Notice } from "@/components/state"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { changePower, type PowerResponse } from "@/components/database/home/power"
import { powerConfirmation } from "@/components/database/home/power-subject"
import { enableExtension, preloadStatements } from "@/components/database/ops/performance-api"
import type { DbStatementsEnable } from "@/components/database/ops/performance-types"
import { TaskDialog, databaseSubject } from "@/components/database/ops/settings-dialog"
import {
  restartAnswer,
  setupPhases,
  setupStart,
  type SetupProgress,
} from "@/components/database/ops/statements-setup-model"
import { useDatabase } from "@/components/database/shell/database-context"
import type { DbConnectionSummary } from "@/components/database/shell/types"

const EXTENSION = "pg_stat_statements"

/** What a view hands the setup when it opens it. */
export type SetupRequest = {
  enable: DbStatementsEnable
  /** The dialog has closed, done or not: the keyboard goes back where it was. */
  onClose?: () => void
}

type Setup = {
  open: (request: SetupRequest) => void
  /**
   * How many setups have finished on this page. A view that reads the
   * statistics reads them again when it moves: the one that opened the
   * dialog may have been replaced during the restart by one that read the
   * server before the extension existed.
   */
  finished: number
}

const SetupContext = createContext<Setup | null>(null)

/**
 * Where the setup's dialog lives: above the switch a page makes between a
 * server that answers and one that is down. The restart in the middle of the
 * setup is exactly when that switch fires — a restarting server reads as
 * unreachable for a second or two — and the view that opened the dialog is
 * replaced while it does; the dialog, and the step after the restart, have
 * to outlive it.
 */
export function StatementsSetupHost({ children }: { children: React.ReactNode }) {
  const [request, setRequest] = useState<SetupRequest>()
  const [finished, setFinished] = useState(0)
  const setup = useMemo(() => ({ open: setRequest, finished }), [finished])
  return (
    <SetupContext.Provider value={setup}>
      {children}
      {request && (
        <StatementsSetup
          enable={request.enable}
          onDone={() => setFinished((count) => count + 1)}
          onClose={() => {
            setRequest(undefined)
            request.onClose?.()
          }}
        />
      )}
    </SetupContext.Provider>
  )
}

/**
 * The setup, on a page that hosts it, with `refresh` — the view's read of the
 * statistics — run again each time one finishes. Nothing where no page
 * hosts it, and then nothing offers it.
 */
export function useStatementsSetup(refresh: () => void) {
  const setup = useContext(SetupContext)
  const finished = setup?.finished ?? 0
  // A view that mounts after a setup finished reads the server as it is.
  const seen = useRef(finished)
  useEffect(() => {
    if (finished === seen.current) return
    seen.current = finished
    refresh()
  }, [finished, refresh])
  return setup?.open
}

/**
 * Statement statistics turned on for a PostgreSQL server that never loaded
 * the module — the one thing the Statements view could only describe before:
 * "add pg_stat_statements to shared_preload_libraries and restart".
 *
 * One dialog carries the three steps (`statements-setup-model.ts`), each
 * drawn on the run path the provisioning flow draws, and each its own press:
 * the library is added with the statement shown before it runs; the restart
 * is asked for separately, after the reader has been told what it does to the
 * sessions on the server, and only where the dashboard can restart it; the
 * extension is created once the server answers again. A server the dashboard
 * cannot restart stops after the first step with the reason, and the setup
 * resumes from the page once it has been restarted where it runs.
 *
 * The dialog stays open through the restart: the step after it is part of
 * what was asked for, and a Postgres restart is seconds. The change of power
 * is held the way the menu's Restart holds it (`changePower`), so every other
 * surface of the database says "Restarting…" meanwhile. It is drawn by the
 * page's `StatementsSetupHost`, which a restart does not replace.
 */
function StatementsSetup({
  enable,
  onClose,
  onDone,
}: {
  enable: DbStatementsEnable
  onClose: () => void
  onDone: () => void
}) {
  const { id, conn, engine, summary, status } = useDatabase()
  const { can } = useAuth()
  const [progress, setProgress] = useState<SetupProgress>(() => ({
    step: setupStart(enable) ?? "preload",
    state: "waiting",
  }))
  const [refusal, setRefusal] = useState<string>()
  const [since, setSince] = useState<number>()
  const [elapsed, setElapsed] = useState(0)
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])
  useEffect(() => {
    if (since === undefined) return
    const timer = window.setInterval(
      () => setElapsed(Math.round((Date.now() - since) / 1000)),
      1000,
    )
    return () => window.clearInterval(timer)
  }, [since])

  const restart = restartAnswer(summary?.power, can("service.control") && can("destructive"))
  const busy = progress.state === "running"
  const name = conn.name
  const database = conn.database

  const at = (step: SetupProgress["step"], state: SetupProgress["state"], why?: string) => {
    if (!alive.current) return
    setProgress({ step, state })
    setRefusal(why)
  }

  const finish = async () => {
    at("extension", "running")
    try {
      await enableExtension(id, EXTENSION)
    } catch (err) {
      at("extension", "failed", errorMessage(err))
      return
    }
    notify.success("Statement statistics are on", {
      description: "They count from now; the list fills as statements run.",
    })
    onDone()
    onClose()
  }

  const store = async () => {
    at("preload", "running")
    try {
      await preloadStatements(id)
      at("restart", "waiting")
    } catch (err) {
      at("preload", "failed", errorMessage(err))
    }
  }

  const restartThenFinish = async () => {
    at("restart", "running")
    setElapsed(0)
    setSince(Date.now())
    try {
      const { settled } = await changePower(id, "restart", {
        request: () => post<PowerResponse>(`/databases/${id}/power`, { action: "restart" }),
        read: async () => (await get<DbConnectionSummary>(`/databases/${id}`)).state,
      })
      status.refresh()
      if (!settled) {
        at(
          "restart",
          "failed",
          `${name} was restarted and is not accepting connections yet. Once it is, Turn on statement statistics creates the extension.`,
        )
        return
      }
    } catch (err) {
      status.refresh()
      at("restart", "failed", errorMessage(err))
      return
    } finally {
      if (alive.current) setSince(undefined)
    }
    await finish()
  }

  const failed = progress.state === "failed"
  const command =
    progress.step === "preload"
      ? failed
        ? "Try again"
        : "Add pg_stat_statements"
      : progress.step === "restart"
        ? restart.can
          ? failed
            ? "Restart again"
            : "Restart and turn on"
          : undefined
        : "Try again"
  const run = () => {
    if (progress.step === "preload") void store()
    else if (progress.step === "restart") void restartThenFinish()
    else void finish()
  }
  const asked = summary && restart.can ? powerConfirmation("restart", engine, summary) : undefined

  return (
    <TaskDialog
      title="Turn on statement statistics"
      description="Load pg_stat_statements at start, restart the server and create the extension."
      size="lg"
      subject={databaseSubject(
        conn,
        engine,
        <>
          <FormFact label="Engine">
            {summary?.versionNumber ? `${engine.label} ${summary.versionNumber}` : engine.label}
          </FormFact>
          {summary?.container ? (
            <FormFact label="Container" mono>
              {summary.container.name}
            </FormFact>
          ) : (
            summary?.unit && (
              <FormFact label="Unit" mono>
                {summary.unit.name}
              </FormFact>
            )
          )}
        </>,
      )}
      dirty={false}
      busy={busy}
      refusal={refusal}
      command={command}
      // A restart disconnects every session, as the menu's Restart does.
      destructive={progress.step === "restart"}
      onRun={run}
      cancelLabel={busy ? null : command ? "Not now" : "Close"}
      onClose={onClose}
    >
      <RunPhases phases={setupPhases(progress)} />
      {busy ? (
        <p className="flex items-baseline justify-between gap-3 text-body" role="status">
          <TextShimmer className="font-medium">
            {progress.step === "preload"
              ? "Adding pg_stat_statements to shared_preload_libraries…"
              : progress.step === "restart"
                ? `Restarting ${name} and waiting for it to answer…`
                : `Creating the extension in ${database}…`}
          </TextShimmer>
          {progress.step === "restart" && (
            <span className="numeric shrink-0 text-hint text-muted-foreground">{elapsed}s</span>
          )}
        </p>
      ) : progress.step === "preload" ? (
        <>
          <p className="text-body leading-relaxed">
            PostgreSQL counts statements only when pg_stat_statements is loaded as the server
            starts. This adds pg_stat_statements to shared_preload_libraries on{" "}
            <span className="font-medium">{name}</span>; the libraries already listed stay. Nothing
            restarts yet.
          </p>
          <Statement label="Runs" sql={enable.preload ?? ""} placeholder="" />
          {!restart.can && (
            <Notice title="The restart is yours to do">
              {restart.reason} Once <span className="font-medium">{name}</span> has been restarted
              where it runs, Turn on statement statistics finishes the setup from this page.
            </Notice>
          )}
        </>
      ) : progress.step === "restart" ? (
        <>
          <p className="text-body leading-relaxed">
            pg_stat_statements is now in shared_preload_libraries on{" "}
            <span className="font-medium">{name}</span>. It loads the next time the server starts,
            and then the extension is created in <span className="font-medium">{database}</span>.
          </p>
          {asked ? (
            <Notice tone="warning" title={`Restart ${name} to load it`}>
              {asked.description}
            </Notice>
          ) : (
            <Notice title="Restart it where it runs">
              {restart.can ? "" : restart.reason} Once it is back, Turn on statement statistics
              creates the extension from this page.
            </Notice>
          )}
        </>
      ) : (
        <FormNote>
          <span className="font-medium">{name}</span> is back with pg_stat_statements loaded.
          Creating the extension in <span className="font-medium">{database}</span> is what is left.
        </FormNote>
      )}
    </TaskDialog>
  )
}
