"use client"

import { useState } from "react"
import { errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { FormFact, FormFacts, FormNote, Statement } from "@/components/form"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { changePower, POWER, type PowerResponse } from "@/components/database/home/power"
import { powerConfirmation } from "@/components/database/home/power-subject"
import { usePower } from "@/components/database/home/verbs"
import { refusal as refusalWords } from "@/components/database/schema/change-dialog"
import type { DdlRequest } from "@/components/database/schema/changes"
import type { DdlAnswer } from "@/components/database/schema/types"
import { usePreview, type Preview } from "@/components/database/schema/use-ddl"
import { ExtensionMark } from "@/components/database/ops/extension-dialogs"
import {
  listWords,
  preloadStage,
  type DbExtension,
  type PreloadStage,
  type PreloadAnswer,
  type PreloadState,
} from "@/components/database/ops/extensions-model"
import { TaskDialog } from "@/components/database/ops/settings-dialog"
import { useDatabase } from "@/components/database/shell/database-context"
import type { DbConnectionSummary } from "@/components/database/shell/types"

type StepState = "done" | "current" | "waiting" | "working"

/**
 * Enabling an extension that works only once the server has loaded its
 * library at start — pg_cron, pg_stat_statements, TimescaleDB, auto_explain —
 * as the three steps it takes, in one dialog: load it at start, restart, then
 * create the extension. A module, which is a library and nothing else, ends
 * at the restart.
 *
 * Each step is its own request, shown before it runs and audited by its own
 * route: the preload route writes shared_preload_libraries with every entry
 * already there kept, the power route restarts the server (the restart is
 * the destructive one, and its command is drawn that way), and the create
 * route makes the extension. Which step is next is read from the server, not
 * remembered here: the dialog opens on the step the server is at, so one
 * closed half-way — or a restart made from the server's own home — is picked
 * up where it was left. Where the dashboard cannot restart the server, the
 * step says why and waits to be told it has been restarted elsewhere.
 *
 * It is the general form of the guided action a single extension's page
 * might offer; one only has to open it with the extension.
 */
export function PreloadExtension({
  extension,
  preload,
  onClose,
  onChanged,
}: {
  extension: DbExtension
  /** What the server loads now and from its next start, as the page last read it. */
  preload: PreloadState
  onClose: () => void
  /** The server's configuration or state changed: read it again. */
  onChanged: () => void
}) {
  const { id, conn, engine, summary, status } = useDatabase()
  const power = usePower()
  const library = extension.library ?? extension.name
  const stage = preloadStage(extension, preload)
  const [busy, setBusy] = useState<"load" | "restart" | "create">()
  const [refusal, setRefusal] = useState<string>()
  // The step that has just been taken, until the server's answer moves the
  // dialog on: a second press in between would only be refused.
  const [taken, setTaken] = useState<PreloadStage>()
  const waitingForRead = taken === stage
  const loadRequest: DdlRequest | null =
    stage === "load" ? { method: "POST", path: "/server/preload", body: { library } } : null
  const createRequest: DdlRequest | null =
    stage === "create"
      ? { method: "POST", path: "/server/extensions", body: { name: extension.name } }
      : null
  const loadPlan = usePreview<PreloadAnswer>(id, loadRequest)
  const createPlan = usePreview(id, createRequest)
  const restarting = busy === "restart" || power.change?.action === "restart"
  const steps = extension.module ? 2 : 3

  const act = async (step: "load" | "restart" | "create", work: () => Promise<void>) => {
    setBusy(step)
    setRefusal(undefined)
    try {
      await work()
    } catch (err) {
      setRefusal(errorMessage(err))
    } finally {
      setBusy(undefined)
    }
  }

  const load = () =>
    act("load", async () => {
      const answer = await post<PreloadAnswer>(`/databases/${id}/server/preload`, { library })
      notify.success(`${library} is loaded from the next start`, {
        description: answer.libraries.join(", "),
      })
      setTaken("load")
      onChanged()
    })

  const restart = () =>
    act("restart", async () => {
      try {
        const { settled } = await changePower(id, "restart", {
          request: () => post<PowerResponse>(`/databases/${id}/power`, { action: "restart" }),
          read: async () => (await get<DbConnectionSummary>(`/databases/${id}`)).state,
        })
        if (!settled) {
          throw new Error(
            `${conn.name} was restarted and is not answering yet. Its log says why; this step can be taken again once it answers.`,
          )
        }
        notify.success(`${conn.name} ${POWER.restart.done}`, {
          description: `It loads ${library} now.`,
        })
        setTaken("restart")
      } finally {
        status.refresh()
        onChanged()
      }
    })

  const create = () =>
    act("create", async () => {
      const answer = await post<DdlAnswer>(`/databases/${id}/server/extensions`, {
        name: extension.name,
      })
      notify.success(`Enabled ${extension.name}`, { description: answer.statement })
      onChanged()
      onClose()
    })

  const stepState = (step: 1 | 2 | 3): StepState => {
    const at = { load: 1, restart: 2, create: 3, done: 4, blocked: 1 }[stage]
    if (step < at) return "done"
    if (step > at) return "waiting"
    if (
      (step === 1 && busy === "load") ||
      (step === 2 && restarting) ||
      (step === 3 && busy === "create")
    ) {
      return "working"
    }
    return "current"
  }

  const command =
    stage === "load"
      ? { label: "Load at start", run: load, ready: Boolean(loadPlan.answer) && !waitingForRead }
      : stage === "restart" && power.can.restart
        ? {
            label: `Restart ${conn.name}`,
            run: restart,
            ready: !restarting && !waitingForRead,
            destructive: true,
          }
        : stage === "create"
          ? { label: `Enable ${extension.name}`, run: create, ready: Boolean(createPlan.answer) }
          : undefined

  return (
    <TaskDialog
      title={extension.module ? `Load ${extension.name}` : `Enable ${extension.name}`}
      description={`${extension.name} needs ${library} loaded when the server starts: the steps that take`}
      subject={{
        mark: <ExtensionMark extension={extension} />,
        name: <span className="font-mono">{extension.name}</span>,
        facts: (
          <>
            {extension.category && <FormFact label="Category">{extension.category}</FormFact>}
            <FormFact label="Library" mono>
              {library}
            </FormFact>
          </>
        ),
      }}
      dirty={false}
      busy={busy !== undefined}
      refusal={refusal}
      cancelLabel={command ? "Cancel" : "Close"}
      secondary={
        stage === "restart" &&
        !restarting && (
          <Button
            type="button"
            variant="outline"
            onClick={() => {
              setTaken(undefined)
              onChanged()
            }}
          >
            Read again
          </Button>
        )
      }
      command={command?.label}
      destructive={command?.destructive}
      disabled={!command?.ready}
      onRun={() => void command?.run()}
      onClose={onClose}
    >
      <p className="text-body leading-relaxed text-muted-foreground">
        {extension.module
          ? `${extension.name} is a library the server loads when it starts, and nothing else: it is enabled by adding it to shared_preload_libraries and restarting.`
          : `${extension.name} works only once the server has loaded ${library}, which it does when it starts. Each step is shown before it runs.`}
      </p>
      {stage === "blocked" ? (
        <Notice tone="warning" title={`${library} cannot be added from here`}>
          {preload.reason}
        </Notice>
      ) : (
        <ol
          aria-label={`The ${steps} steps to enable ${extension.name}`}
          className="divide-y divide-hairline"
        >
          <PreloadStep index={1} title="Load it at start" state={stepState(1)}>
            {stage === "load" ? (
              <LoadPlan plan={loadPlan} library={library} />
            ) : (
              <p>
                shared_preload_libraries names {library}
                {preload.configured.length > 1
                  ? `, beside ${listWords(preload.configured.filter((one) => one !== library))}`
                  : ""}
                .
              </p>
            )}
          </PreloadStep>
          <PreloadStep index={2} title={`Restart ${conn.name}`} state={stepState(2)}>
            {stage === "load" ? (
              <p>The server reads the list only when it starts.</p>
            ) : stage === "restart" ? (
              power.can.restart && summary ? (
                powerConfirmation("restart", engine, summary).description
              ) : (
                <p>
                  {summary?.power.restart
                    ? "Restarting it interrupts whatever is using it, and your role may not do that."
                    : (summary?.power.reason ?? "Nothing here can restart it.")}{" "}
                  Once it has been restarted where it runs, read it again.
                </p>
              )
            ) : (
              <p>It loaded {library} when it started.</p>
            )}
          </PreloadStep>
          {!extension.module && (
            <PreloadStep index={3} title="Create the extension" state={stepState(3)}>
              {stage === "create" ? (
                <PlanStatement plan={createPlan} waiting="Reading the statement from the server…" />
              ) : (
                <p>Once the server has loaded it, in {conn.database || conn.name}.</p>
              )}
            </PreloadStep>
          )}
        </ol>
      )}
      {stage === "done" && (
        <FormNote tone="success" role="status">
          {extension.name} is loaded when the server starts.
        </FormNote>
      )}
    </TaskDialog>
  )
}

/** One step, numbered, with its state at the far end and what it does under it. */
function PreloadStep({
  index,
  title,
  state,
  children,
}: {
  index: number
  title: string
  state: StepState
  children: React.ReactNode
}) {
  return (
    <li className="flex min-w-0 gap-3 py-3 first:pt-0 last:pb-0" data-step={state}>
      {/* The list says the order to a screen reader; the figure says it to the eye. */}
      <span aria-hidden className="numeric w-3 shrink-0 text-body text-muted-foreground">
        {index}
      </span>
      <div className="min-w-0 flex-1 space-y-2">
        <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-3 gap-y-1">
          <span className="text-body font-medium">{title}</span>
          {state === "done" ? (
            <Status verdict="ok" label="Done" />
          ) : state === "working" ? (
            <Status
              tone="notice"
              live
              label={index === 2 ? POWER.restart.progressive : "Running…"}
            />
          ) : state === "current" ? (
            <Status verdict="notice" label="Next" />
          ) : (
            <Status tone="stopped" label="Waiting" />
          )}
        </div>
        <div className="space-y-2 text-xs leading-relaxed text-muted-foreground">{children}</div>
      </div>
    </li>
  )
}

/** The server's plan for a step, or why it has none. */
function PlanStatement({ plan, waiting }: { plan: Preview; waiting: string }) {
  return (
    <div className="space-y-2">
      <Statement
        sql={plan.answer?.statements.join(";\n") ?? ""}
        placeholder={plan.error ? "No statement: the server will not plan this step." : waiting}
      />
      {plan.error && (
        <FormNote tone="danger" role="alert" className="break-words">
          {refusalWords(plan.error)}
        </FormNote>
      )}
    </div>
  )
}

/** The first step's plan: the statements, the list the next start loads and what it did with each parameter. */
function LoadPlan({ plan, library }: { plan: Preview<PreloadAnswer>; library: string }) {
  const kept = plan.answer?.libraries.filter((one) => one !== library) ?? []
  return (
    <>
      <PlanStatement plan={plan} waiting="Reading what the server loads now…" />
      {plan.answer && (
        <FormFacts>
          <FormFact label="From the next start" mono>
            {plan.answer.libraries.join(", ")}
          </FormFact>
          {kept.length > 0 && (
            <FormFact label="Already loaded" mono>
              {kept.join(", ")}
            </FormFact>
          )}
        </FormFacts>
      )}
      {plan.answer?.notes.map((note) => (
        <FormNote key={note}>{note}</FormNote>
      ))}
    </>
  )
}
