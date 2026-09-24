"use client"

import { useId } from "react"
import Link from "next/link"
import { ArrowDown, ArrowUp, Key, Plus, Trash } from "@/components/icons"
import { get } from "@/lib/api"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentConfiguration,
  DeploymentRunSnapshot,
  DeploymentVariable,
} from "@/lib/types"
import { Disclosure, Field, FieldRow, FormNote } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Group } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import { Textarea } from "@/components/ui/textarea"
import { RELEASE_GROUPS } from "@/components/deploy/vocabulary"
import { StageStrip } from "@/components/deploy/run-pipeline"
import { useProject } from "@/components/deploy/project-context"

export type ReleaseTask = NonNullable<DeploymentConfiguration["build"]["releaseTasks"]>[number]

/** What a run recorded for each task it ran. */
type TaskEvidence = { name: string; durationMs: number; exitCode: number; variableNames?: string[] }

/**
 * Where a task runs, said as a place: the release's own image, with the
 * application's toolchain and variables, or the dashboard's shell over the
 * source as committed — which has none of the application's dependencies, so
 * `npx`, `python manage.py` or `bundle exec` can only fail there.
 */
const RUNNERS: { runner: ReleaseTask["runner"]; label: string; hint: string }[] = [
  {
    runner: "image",
    label: "Release image",
    hint: "Runs once in the release's own image, with its runtime variables and the ones below, before it starts.",
  },
  {
    runner: undefined,
    label: "Dashboard shell",
    hint: "Runs in the dashboard's shell over the unbuilt source, without the application's dependencies.",
  },
]

/**
 * Named, timed gates that run after the artifact is built and before the new
 * version starts — a migration, a cache warm, a smoke script.
 *
 * Above them, where in the release they run: the release path's seven stages
 * with Release lit, because "before activation" is only a sentence until it
 * is seen as a place on the same path the run page draws. Each task is a
 * fenced group of labelled fields — a name, a timeout in seconds, the command,
 * the directory it runs in folded away until it matters — because a placeholder
 * that vanishes once typed into is not a label, least of all on a phone with
 * the keyboard up. The order is the order they run in, so it can be changed,
 * and each task says how it went the last time it ran — in a release that
 * failed on it, if one has since the live one, since that release never went
 * live and the live one would still say every task passed.
 *
 * A task's `env` is drawn only from variables already scoped to Release task,
 * so a task can never reach a value nobody granted it.
 */
export function ReleaseTasks({
  tasks,
  variables,
  disabled,
  onChange,
  rowError,
}: {
  tasks: ReleaseTask[]
  variables: DeploymentVariable[]
  disabled?: boolean
  onChange: (tasks: ReleaseTask[]) => void
  /** The message a save refused for this task, when it named the task but no sub-field. */
  rowError?: (index: number) => string | undefined
}) {
  const project = useProject()
  const id = useId()
  const releaseVariables = variables.filter((variable) => variable.scopes.includes("release_task"))
  const liveRun = project.liveRun
  const failedOnTask = project.runs.find(
    (run) =>
      run.environmentId === project.environmentId &&
      (run.terminalCode === "release_task_failed" ||
        run.terminalCode === "release_task_tool_missing") &&
      run.id > (liveRun?.id ?? 0),
  )
  const lastRun = failedOnTask ?? liveRun
  const snapshot = usePoll(
    (signal) =>
      get<DeploymentRunSnapshot>(
        `/deploy/${project.projectId}/runs/${lastRun?.id}`,
        undefined,
        signal,
      ),
    0,
    [project.projectId, lastRun?.id],
    { enabled: lastRun !== undefined },
  )
  const evidence = (
    snapshot.data?.steps.find((step) => step.key === "release_task")?.evidence as
      { tasks?: TaskEvidence[] } | undefined
  )?.tasks

  const update = (index: number, patch: Partial<ReleaseTask>) =>
    onChange(tasks.map((task, i) => (i === index ? { ...task, ...patch } : task)))
  const move = (index: number, by: -1 | 1) => {
    const next = [...tasks]
    const [task] = next.splice(index, 1)
    next.splice(index + by, 0, task)
    onChange(next)
    // The tasks are drawn by position, so the button pressed now belongs to
    // the task that took this one's place: the keyboard follows the task,
    // to the same arrow while it can still go that way, else the other.
    const to = index + by
    const onward = by < 0 ? to > 0 : to < next.length - 1
    const arrow = by < 0 === onward ? "up" : "down"
    requestAnimationFrame(() => document.getElementById(`${id}-task-${to}-${arrow}`)?.focus())
  }

  return (
    <div className="space-y-4">
      {/* Where the tasks run: the run page's seven stages, with Release lit. */}
      <StageStrip
        steps={RELEASE_GROUPS.map((group) => group.label)}
        current={RELEASE_GROUPS.findIndex((group) => group.label === "Release")}
        label="Release tasks run in the Release stage, after Build and before Start"
      />
      <FormNote>A failing task stops the release; the live version keeps serving.</FormNote>
      {tasks.length === 0 ? (
        <p className="text-body text-muted-foreground">No release tasks configured.</p>
      ) : (
        <ol className="space-y-3">
          {tasks.map((task, index) => {
            const n = index + 1
            const error = rowError?.(index)
            const last = evidence?.find((one) => one.name === task.name)
            const titleId = `${id}-task-${index}-title`
            return (
              <li key={index}>
                <Group
                  role="group"
                  aria-labelledby={titleId}
                  tone={error ? "danger" : "default"}
                  className="space-y-3"
                >
                  <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
                    <p id={titleId} className="flex min-w-0 flex-1 items-baseline gap-2">
                      <span className="eyebrow shrink-0">Task {n}</span>
                      <span
                        className={cn(
                          "truncate text-sm font-medium",
                          !task.name.trim() && "text-muted-foreground",
                        )}
                      >
                        {task.name.trim() || "Unnamed"}
                      </span>
                    </p>
                    {last && lastRun && (
                      <Status
                        tone={last.exitCode === 0 ? "running" : "danger"}
                        label={`${
                          last.exitCode === 0
                            ? `passed in ${(last.durationMs / 1000).toFixed(1)} s`
                            : `exit ${last.exitCode}`
                        } · #${lastRun.runNumber}`}
                      />
                    )}
                    {!disabled && (
                      <span className="ml-auto flex shrink-0 items-center gap-0.5">
                        <IconAction
                          id={`${id}-task-${index}-up`}
                          label="Move up"
                          disabled={index === 0}
                          onClick={() => move(index, -1)}
                        >
                          <ArrowUp />
                        </IconAction>
                        <IconAction
                          id={`${id}-task-${index}-down`}
                          label="Move down"
                          disabled={index === tasks.length - 1}
                          onClick={() => move(index, 1)}
                        >
                          <ArrowDown />
                        </IconAction>
                        <IconAction
                          label={`Remove release task ${task.name || n}`}
                          className="text-destructive"
                          onClick={() => onChange(tasks.filter((_, i) => i !== index))}
                        >
                          <Trash />
                        </IconAction>
                      </span>
                    )}
                  </div>
                  <FieldRow className="sm:grid-cols-[minmax(0,1fr)_11rem]">
                    <Field label="Name" htmlFor={`${id}-task-${index}-name`}>
                      <Input
                        id={`${id}-task-${index}-name`}
                        aria-label={`Release task ${n} name`}
                        value={task.name}
                        onChange={(event) => update(index, { name: event.target.value })}
                        placeholder="Database migration"
                        readOnly={disabled}
                      />
                    </Field>
                    <Field
                      label="Timeout"
                      htmlFor={`${id}-task-${index}-timeout`}
                      hint={`${spoken(task.timeoutSeconds)} · at most 1 h`}
                    >
                      <InputGroup>
                        <InputGroupInput
                          id={`${id}-task-${index}-timeout`}
                          aria-label={`Release task ${n} timeout seconds`}
                          type="number"
                          inputMode="numeric"
                          min={1}
                          max={3600}
                          value={task.timeoutSeconds}
                          onChange={(event) =>
                            update(index, { timeoutSeconds: Number(event.target.value) })
                          }
                          readOnly={disabled}
                          className="numeric"
                        />
                        <InputGroupAddon align="inline-end">
                          <InputGroupText>seconds</InputGroupText>
                        </InputGroupAddon>
                      </InputGroup>
                    </Field>
                  </FieldRow>
                  <Field label="Runs in">
                    <div className="flex flex-wrap gap-1.5">
                      {RUNNERS.map((option) => (
                        <FilterChip
                          key={option.label}
                          selected={task.runner === option.runner}
                          disabled={disabled}
                          onClick={() => update(index, { runner: option.runner })}
                          className={cn(
                            "h-8 font-normal disabled:opacity-60 sm:h-7",
                            task.runner !== option.runner && "border-border",
                          )}
                        >
                          {option.label}
                        </FilterChip>
                      ))}
                    </div>
                  </Field>
                  <Field
                    label="Command"
                    htmlFor={`${id}-task-${index}-command`}
                    hint={RUNNERS.find((option) => option.runner === task.runner)?.hint}
                  >
                    <Textarea
                      id={`${id}-task-${index}-command`}
                      aria-label={`Release task ${n} command`}
                      value={task.command}
                      onChange={(event) => update(index, { command: event.target.value })}
                      placeholder="./bin/migrate"
                      readOnly={disabled}
                      rows={3}
                      className="font-mono sm:text-xs"
                      spellCheck={false}
                    />
                  </Field>
                  <Disclosure
                    quiet
                    summary="Working directory"
                    facts={
                      task.workingDirectory?.trim() ||
                      (task.runner === "image" ? "the image's own" : "source root")
                    }
                  >
                    <Input
                      aria-label={`Release task ${n} working directory`}
                      value={task.workingDirectory ?? ""}
                      onChange={(event) => update(index, { workingDirectory: event.target.value })}
                      placeholder="Source root"
                      readOnly={disabled}
                      className="font-mono sm:text-xs"
                      spellCheck={false}
                    />
                  </Disclosure>
                  <Field label="Environment">
                    {releaseVariables.length === 0 ? (
                      <FormNote>
                        No variable reaches release tasks yet.{" "}
                        <Link
                          href={`/deploy/${project.projectId}/settings/variables`}
                          className="text-foreground underline-offset-2 hover:underline"
                        >
                          Give one the Release task scope
                        </Link>
                      </FormNote>
                    ) : (
                      <div className="flex flex-wrap gap-1.5">
                        {releaseVariables.map((variable) => {
                          const on = task.env.includes(variable.name)
                          return (
                            <FilterChip
                              key={variable.name}
                              selected={on}
                              disabled={disabled}
                              onClick={() =>
                                update(index, {
                                  env: on
                                    ? task.env.filter((name) => name !== variable.name)
                                    : [...new Set([...task.env, variable.name])],
                                })
                              }
                              // In a form an unpressed chip keeps an edge, or a
                              // lone one reads as a caption rather than a control.
                              className={cn(
                                "h-8 font-mono font-normal disabled:opacity-60 sm:h-7",
                                !on && "border-border",
                              )}
                            >
                              <Key
                                aria-hidden
                                className={cn("size-3 shrink-0", on && "text-brand")}
                              />
                              {variable.name}
                            </FilterChip>
                          )
                        })}
                      </div>
                    )}
                  </Field>
                  {error && (
                    <p role="alert" className="text-hint text-destructive">
                      {error}
                    </p>
                  )}
                </Group>
              </li>
            )
          })}
        </ol>
      )}
      {!disabled && (
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => {
            onChange([
              ...tasks,
              {
                name: "",
                command: "",
                workingDirectory: "",
                timeoutSeconds: 300,
                env: [],
                runner: "image",
              },
            ])
            // A task just added takes the keyboard at its name, the field it
            // cannot do without — once it has been drawn.
            const added = `${id}-task-${tasks.length}-name`
            requestAnimationFrame(() => document.getElementById(added)?.focus())
          }}
        >
          <Plus className="size-3.5" />
          Add release task
        </Button>
      )}
    </div>
  )
}

/** A timeout the way it is said: "5 min", "90 s", "1 h". */
export function spoken(seconds: number) {
  if (!seconds || seconds < 1) return "no limit set"
  if (seconds < 120) return `${seconds} s`
  if (seconds % 3600 === 0) return `${seconds / 3600} h`
  if (seconds % 60 === 0) return `${seconds / 60} min`
  return `${Math.floor(seconds / 60)} min ${seconds % 60} s`
}
