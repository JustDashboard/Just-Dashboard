"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { StopCircle, Wrench } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { duration } from "@/lib/format"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { Segments } from "@/components/deploy/settings/segments"
import { FormFact, FormFacts, FormNote, Statement } from "@/components/form"
import { Modal } from "@/components/modal"
import { Well } from "@/components/panel"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import type { Verb } from "@/components/verbs"
import { EngineMark } from "@/components/database/kit"
import { readMaintenance, runMaintenance } from "@/components/database/ops/performance-api"
import { useAsk, useReturnFocus } from "@/components/database/ops/performance-parts"
import {
  byAction,
  confirmsFirst,
  locksThroughout,
  maintenanceVerbs,
  targetWords,
  type MaintenanceTarget,
  type MaintenanceVerb,
} from "@/components/database/ops/performance-storage"
import type { DbMaintenanceResult } from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"

type Run = {
  verb: MaintenanceVerb
  since: number
  state: "running" | "done" | "failed" | "stopped"
  result?: DbMaintenanceResult
  error?: string
}

/**
 * The engine's maintenance, for the views that offer it.
 *
 * The actions are the server's closed list (`GET /maintenance`): a vacuum on
 * PostgreSQL, an optimize on MySQL, a checkpoint on SQLite. A view asks for
 * the ones that apply to a table, to an index or to the whole database and
 * gets them as verbs, already narrowed to what the role may run and — on a
 * protected connection — to the checks that change nothing.
 *
 * A run is one request held open until the command finishes, so it is drawn
 * as what it is: a dialog that says what is running on what, counts how long
 * it has been, and can stop it — closing the request stops the command on the
 * server. When it ends the dialog holds the statements that ran and the
 * engine's own output. An action that locks what it works on, or can lose
 * rows, is asked about first with the table named.
 *
 * Whichever way a run ends — refused at the question, stopped, finished and
 * closed — the keyboard goes back to the menu or the button that started it.
 */
export function useMaintenance(onDone?: () => void) {
  const { id, engine, readOnly } = useDatabase()
  const { can } = useAuth()
  // One memory for the question and the run after it: both give the keyboard
  // back to the control that started them.
  const focus = useReturnFocus()
  const { remember, restore } = focus
  const { confirm, dialog } = useAsk(focus)
  const [run, setRun] = useState<Run | null>(null)
  const controller = useRef<AbortController | null>(null)
  const list = usePoll((signal) => readMaintenance(id, signal), 0, [id], {
    enabled: engine.can("maintenance"),
  })
  const actions = list.data?.actions

  // Leaving the page closes the request, which stops the command: a vacuum
  // nobody is watching is not left to run on.
  useEffect(() => () => controller.current?.abort(), [])

  const begin = useCallback(
    (verb: MaintenanceVerb) => {
      const abort = new AbortController()
      controller.current = abort
      setRun({ verb, since: Date.now(), state: "running" })
      runMaintenance(id, verb.request, abort.signal)
        .then((result) => {
          setRun((current) =>
            current?.verb === verb ? { ...current, state: "done", result } : current,
          )
          onDone?.()
        })
        .catch((error: unknown) => {
          setRun((current) =>
            current?.verb !== verb
              ? current
              : abort.signal.aborted
                ? { ...current, state: "stopped" }
                : { ...current, state: "failed", error: errorMessage(error) },
          )
          // A stopped vacuum has still done part of its work.
          if (abort.signal.aborted) onDone?.()
        })
    },
    [id, onDone],
  )

  const start = useCallback(
    (verb: MaintenanceVerb) => {
      if (!confirmsFirst(verb.action)) {
        remember()
        return begin(verb)
      }
      const on = targetWords(verb.request, engine.nouns.object)
      confirm({
        title: verb.label,
        subject: {
          mark: <EngineMark engine={engine} size="sm" />,
          name: <span className="font-mono">{on}</span>,
          facts: (
            <>
              <FormFact label="Runs">{verb.action.label}</FormFact>
              {verb.action.destructive && <FormFact label="Can lose">rows</FormFact>}
            </>
          ),
        },
        description: (
          <>
            <p>{verb.action.description}</p>
            <p className="text-muted-foreground">
              It runs until it finishes. Closing its window, or this page, stops it.
            </p>
          </>
        ),
        confirmLabel: verb.label,
        action: async () => {
          begin(verb)
          return "reported"
        },
      })
    },
    [begin, confirm, engine, remember],
  )

  /** The verbs for one target, as a row's menu or a header's draws them. */
  const verbsFor = useCallback(
    (target: MaintenanceTarget): Verb[] =>
      maintenanceVerbs(actions ?? [], target, { can, readOnly }).map((verb) => ({
        key: verb.key,
        label: verb.label,
        icon: Wrench,
        run: () => start(verb),
        danger: verb.action.destructive,
      })),
    [actions, can, readOnly, start],
  )

  const close = () => {
    controller.current?.abort()
    setRun(null)
    restore()
  }

  return {
    /** The read of the server's list, for a view that draws the actions and has to say when it could not. */
    list,
    actions,
    verbsFor,
    start,
    /**
     * Runs a verb at once, for a caller that has asked about it in its own
     * words. `from` is the control the keyboard goes back to when the run's
     * dialog closes.
     */
    run: (verb: MaintenanceVerb, from?: HTMLElement | null) => {
      remember(from ?? null)
      begin(verb)
    },
    /** The verbs a target has, with the action behind each. */
    forTarget: (target: MaintenanceTarget) =>
      maintenanceVerbs(actions ?? [], target, { can, readOnly }),
    dialogs: (
      <>
        {dialog}
        {run && (
          <MaintenanceRun run={run} onStop={() => controller.current?.abort()} onClose={close} />
        )}
      </>
    ),
  }
}

export type Maintenance = ReturnType<typeof useMaintenance>

/**
 * The engine's list of actions could not be read, so no row carries its
 * menu: said where the menus would have been, with the way to ask again. A
 * view whose verbs are simply absent reads as an engine that has none.
 */
export function MaintenanceUnread({ maintenance }: { maintenance: Maintenance }) {
  const list = maintenance.list
  if (list.data || !list.error) return null
  return (
    <div role="status" className="flex flex-wrap items-center gap-x-3 gap-y-2">
      <FormNote tone="warning" className="min-w-0">
        The maintenance this engine offers could not be read, so nothing here carries its menu:{" "}
        <span className="wrap-anywhere">{errorMessage(list.error)}</span>
      </FormNote>
      <Button size="xs" variant="outline" onClick={list.refresh}>
        Try again
      </Button>
    </div>
  )
}

/**
 * The actions for one target, each with the engine's own sentence about what
 * it does and what it locks, and the press that runs it. For a surface with
 * room to say it; a row of a table keeps them in its menu.
 */
export function MaintenanceList({
  verbs,
  maintenance,
  on,
}: {
  verbs: MaintenanceVerb[]
  maintenance: Maintenance
  /** What they run on, for each button's name: "public.orders", "the whole database". */
  on: string
}) {
  return (
    <ul className="divide-y divide-hairline border-y border-hairline">
      {byAction(verbs).map((group) => (
        <MaintenanceRow key={group[0].action.id} verbs={group} maintenance={maintenance} on={on} />
      ))}
    </ul>
  )
}

/**
 * One action of the list. Where the action has a choice in it — a checkpoint's
 * four modes, a rebuild online or not — the choice is one segmented control
 * on its row rather than a row per answer, each repeating the same sentence.
 */
function MaintenanceRow({
  verbs,
  maintenance,
  on,
}: {
  verbs: MaintenanceVerb[]
  maintenance: Maintenance
  on: string
}) {
  // Where an action can run without holding its lock, that is the answer
  // already chosen: the one that stops the table is a deliberate second press.
  const [chosen, setChosen] = useState(
    () => (verbs.find((entry) => entry.request.options?.concurrently) ?? verbs[0]).key,
  )
  const verb = verbs.find((entry) => entry.key === chosen) ?? verbs[0]
  const action = verb.action
  return (
    <li className="flex flex-wrap items-start gap-x-3 gap-y-2 py-2.5">
      <div className="min-w-0 flex-1 basis-64 space-y-0.5">
        <p className="flex flex-wrap items-center gap-x-2.5 gap-y-0.5 text-body font-medium">
          {action.label}
          {locksThroughout(verb) && <Tag tone="warning">locks while it runs</Tag>}
          {action.destructive && <Tag tone="danger">can lose rows</Tag>}
          {action.readOnly && <Tag>changes nothing</Tag>}
        </p>
        <p className="text-hint leading-relaxed text-pretty text-muted-foreground">
          {action.description}
        </p>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {verbs.length > 1 && (
          <Segments
            label={`How to run ${action.label.toLowerCase()}`}
            value={verb.key}
            options={verbs.map((entry) => ({
              value: entry.key,
              label: entry.variant ?? entry.label,
            }))}
            onChange={setChosen}
          />
        )}
        <Button
          size="sm"
          variant="outline"
          aria-label={`${verb.label}: ${on}`}
          onClick={() => maintenance.start(verb)}
        >
          Run
        </Button>
      </div>
    </li>
  )
}

function MaintenanceRun({
  run,
  onStop,
  onClose,
}: {
  run: Run
  onStop: () => void
  onClose: () => void
}) {
  const { engine } = useDatabase()
  const on = targetWords(run.verb.request, engine.nouns.object)
  const running = run.state === "running"
  const result = run.result
  return (
    <Modal
      open
      // While it runs the dialog is the command: it is closed with Stop, so a
      // press outside it does not end a vacuum by accident.
      onOpenChange={(open) => !open && !running && onClose()}
      size="lg"
      initialFocus="body"
      title={run.verb.label}
      description={`${run.verb.label} on ${on}`}
      footer={
        running ? (
          <>
            <FormNote className="mr-auto">
              One request, held until the command ends. Stop closes it.
            </FormNote>
            <Button variant="outline" onClick={onStop}>
              <StopCircle />
              Stop
            </Button>
          </>
        ) : (
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
        )
      }
    >
      <div className="space-y-4">
        <div className="flex min-w-0 items-center gap-3">
          <EngineMark engine={engine} size="sm" />
          <div className="min-w-0 space-y-0.5">
            <p className="truncate font-mono text-body font-medium">{on}</p>
            <FormFacts>
              <FormFact label="Action">{run.verb.action.label}</FormFact>
              {result && <FormFact label="Took">{result.duration}</FormFact>}
            </FormFacts>
          </div>
        </div>

        <div role="status" className="space-y-3">
          {running ? (
            <div className="space-y-2">
              <p className="flex items-center gap-2 text-body">
                <TextShimmer>Running…</TextShimmer>
                <Elapsed since={run.since} />
              </p>
              <div
                aria-hidden
                className="relative h-0.5 overflow-hidden rounded-full bg-meter-track"
              >
                <span className="absolute inset-y-0 left-0 w-1/3 animate-sweep rounded-full bg-brand" />
              </div>
            </div>
          ) : run.state === "stopped" ? (
            <Notice title="Stopped">
              <p>
                The request was closed, and the engine stops the command when it sees that. What it
                had done up to then is undone or kept as the engine does for any cancelled
                statement.
              </p>
            </Notice>
          ) : run.state === "failed" ? (
            <Notice tone="danger" title="The engine refused">
              <p className="wrap-anywhere">{run.error}</p>
            </Notice>
          ) : result ? (
            <Status
              verdict={result.ok ? "ok" : "warning"}
              label={
                result.ok ? "Finished" : "Finished, and reported a problem — see its output below"
              }
            />
          ) : null}
        </div>

        {result && (
          <>
            <Statement
              label={result.statements.length === 1 ? "Statement" : "Statements"}
              sql={result.statements.join("\n")}
              placeholder="The engine was sent no statement."
            />
            <div className="space-y-1.5">
              <p className="eyebrow">Output</p>
              {result.output.length > 0 ? (
                <Well className="max-h-72 overflow-auto text-hint leading-relaxed whitespace-pre-wrap">
                  {result.output.join("\n")}
                </Well>
              ) : (
                <FormNote>The engine printed nothing for this command.</FormNote>
              )}
              {result.outputTruncated && (
                <FormNote>The engine printed more; the first 500 lines are kept.</FormNote>
              )}
            </div>
          </>
        )}
      </div>
    </Modal>
  )
}

/** How long the run has been going, counted by the second. Not announced: it changes every second. */
function Elapsed({ since }: { since: number }) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [])
  return (
    <span aria-hidden className="numeric text-muted-foreground">
      {duration(Math.max(0, (now - since) / 1000))}
    </span>
  )
}
