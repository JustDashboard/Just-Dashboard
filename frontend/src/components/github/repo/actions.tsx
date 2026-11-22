"use client"

import { useState } from "react"
import { ArrowUpRight, Play, RotateCounterClockwise, StopCircle } from "@/components/icons"
import { Field } from "@/components/form"
import { DimActions, IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList, RowsSkeleton } from "@/components/row-list"
import { EmptyNote, ErrorState } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { runState, runVerbs, type RunTone } from "@/components/github/repo/model"
import {
  Blocked,
  ListBody,
  ShowMore,
  useActReason,
  usePages,
  useRepo,
} from "@/components/github/repo/shared"
import { usePoll } from "@/hooks/use-poll"
import { errorMessage, get, post } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type {
  GitHubBranchPage,
  GitHubDispatchResult,
  GitHubRun,
  GitHubRunPage,
  GitHubWorkflow,
  GitHubWorkflowList,
} from "@/lib/github-types"

const DOT: Record<RunTone, DotTone> = {
  success: "running",
  danger: "danger",
  warning: "warning",
  notice: "notice",
  neutral: "stopped",
}
const ALL = "\u0000all"

/**
 * The repository's workflows and their recent runs. A workflow GitHub can
 * start from here (a `workflow_dispatch` trigger) has **Run**, with its
 * inputs; one without has no Run at all, its reason on hover. A finished run
 * can be run again, all of it or its failed jobs; a waiting or running one
 * can be cancelled.
 */
export function RepoActions() {
  const { repo, path } = useRepo()
  const workflows = usePoll(
    (signal) => get<GitHubWorkflowList>(path("/actions/workflows"), undefined, signal),
    0,
    [repo.fullName],
  )
  const [workflow, setWorkflow] = useState(ALL)
  const [running, setRunning] = useState<GitHubWorkflow>()
  const runs = usePages<GitHubRun, GitHubRunPage>(
    path("/actions/runs"),
    { workflow: workflow === ALL ? undefined : workflow },
    (p) => p.runs,
  )
  const list = workflows.data
  const names = new Map((list?.workflows ?? []).map((w) => [w.id, w.name]))
  return (
    <div className="flex min-w-0 flex-col gap-8 pb-6">
      <Panel plain aria-label="Workflows">
        <PanelHeader
          title="Workflows"
          count={list?.workflows.length ? list.workflows.length : undefined}
        />
        <PanelBody flush>
          {!list ? (
            workflows.error ? (
              <ErrorState error={workflows.error} onRetry={workflows.refresh} retryable />
            ) : (
              <RowsSkeleton rows={3} mark={false} />
            )
          ) : !list.readable ? (
            <EmptyNote>{list.message ?? "GitHub would not list the workflows."}</EmptyNote>
          ) : list.workflows.length === 0 ? (
            <EmptyNote>No workflows in .github/workflows on the default branch.</EmptyNote>
          ) : (
            <RowList data-slot="github-workflows">
              {list.workflows.map((w) => (
                <WorkflowRow key={w.id} workflow={w} onRun={() => setRunning(w)} />
              ))}
            </RowList>
          )}
        </PanelBody>
      </Panel>

      <Panel plain aria-label="Runs">
        <PanelHeader
          title="Runs"
          actions={
            (list?.workflows.length ?? 0) > 1 && (
              <Select value={workflow} onValueChange={setWorkflow}>
                <SelectTrigger aria-label="Workflow" size="sm" className="w-48 max-sm:w-36">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={ALL}>Every workflow</SelectItem>
                  {list?.workflows.map((w) => (
                    <SelectItem key={w.id} value={String(w.id)}>
                      {w.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )
          }
        />
        <PanelBody flush>
          <ListBody pages={runs} empty="No runs yet." rows={6}>
            <RowList data-slot="github-runs">
              {runs.items.map((run) => (
                <RunRow
                  key={run.id}
                  run={run}
                  workflow={names.get(run.workflowId) ?? run.name}
                  onChanged={runs.reload}
                />
              ))}
            </RowList>
          </ListBody>
          <ShowMore pages={runs} />
        </PanelBody>
      </Panel>

      {running && (
        <DispatchDialog
          workflow={running}
          onClose={() => setRunning(undefined)}
          onStarted={() => {
            setRunning(undefined)
            runs.reload()
          }}
        />
      )}
    </div>
  )
}

function WorkflowRow({ workflow, onRun }: { workflow: GitHubWorkflow; onRun: () => void }) {
  const reason = useActReason("actions")
  const active = workflow.state === "active"
  return (
    <Row
      data-workflow={workflow.path}
      title={workflow.name}
      subtitle={workflow.path}
      mono
      trailing={
        <>
          <span className="text-hint text-muted-foreground max-sm:hidden">
            {active ? "active" : workflow.state.replace(/_/g, " ")}
          </span>
          {workflow.dispatch.available ? (
            reason ? (
              <Blocked reason={reason}>
                <Button size="xs" variant="outline" disabled>
                  <Play />
                  Run
                </Button>
              </Blocked>
            ) : (
              <Button size="xs" variant="outline" onClick={onRun} aria-label={`Run ${workflow.name}`}>
                <Play />
                Run
              </Button>
            )
          ) : (
            <Tooltip>
              <TooltipTrigger asChild>
                {/* eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- takes the keyboard so that its reason can be read without a pointer */}
                <span
                  tabIndex={0}
                  data-slot="no-dispatch"
                  className="rounded-sm text-hint text-faint focus-ring"
                >
                  on its triggers
                </span>
              </TooltipTrigger>
              <TooltipContent className="max-w-xs">
                {workflow.dispatch.message ??
                  "It has no workflow_dispatch trigger, so it runs only on the events it names."}
              </TooltipContent>
            </Tooltip>
          )}
          <IconAction label={`Open ${workflow.name} on GitHub`} asChild>
            <a href={workflow.htmlUrl} target="_blank" rel="noreferrer">
              <ArrowUpRight />
            </a>
          </IconAction>
        </>
      }
    />
  )
}

function RunRow({
  run,
  workflow,
  onChanged,
}: {
  run: GitHubRun
  workflow: string
  onChanged: () => void
}) {
  const { path, confirm } = useRepo()
  const reason = useActReason("actions")
  const state = runState(run)
  const verbs = runVerbs(run)
  const rerun = async (failedOnly: boolean) => {
    try {
      await post(path(`/actions/runs/${run.id}/rerun`), { failedOnly })
      notify.success(failedOnly ? "Re-running the failed jobs" : "Re-running")
      onChanged()
    } catch (err) {
      notify.error("GitHub did not re-run it", err)
    }
  }
  const cancel = () =>
    confirm({
      title: `Cancel run ${run.runNumber}`,
      confirmLabel: "Cancel run",
      description: <p>Stops {workflow} on {run.branch}. Jobs that finished keep their results.</p>,
      action: async () => {
        await post(path(`/actions/runs/${run.id}/cancel`))
        onChanged()
      },
    })
  const controls = verbs.cancel ? (
    reason ? (
      <Blocked reason={reason}>
        <IconAction label="Cancel run" disabled>
          <StopCircle />
        </IconAction>
      </Blocked>
    ) : (
      <IconAction label={`Cancel run ${run.runNumber}`} onClick={cancel}>
        <StopCircle />
      </IconAction>
    )
  ) : verbs.rerun ? (
    reason ? (
      <Blocked reason={reason}>
        <IconAction label="Re-run" disabled>
          <RotateCounterClockwise />
        </IconAction>
      </Blocked>
    ) : verbs.rerunFailed ? (
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button size="icon-sm" variant="ghost" aria-label={`Re-run run ${run.runNumber}`}>
            <RotateCounterClockwise />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onSelect={() => void rerun(false)}>Re-run all jobs</DropdownMenuItem>
          <DropdownMenuItem onSelect={() => void rerun(true)}>Re-run failed jobs</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    ) : (
      <IconAction label={`Re-run run ${run.runNumber}`} onClick={() => void rerun(false)}>
        <RotateCounterClockwise />
      </IconAction>
    )
  ) : null
  return (
    <Row
      data-run={run.id}
      leading={<Status tone={DOT[state.tone]} live={state.live} label={state.word} className="w-28" />}
      title={run.title || run.name}
      subtitle={
        <>
          {workflow} · <span className="font-mono">{run.branch}</span> · {run.event}
          {run.actor && ` · ${run.actor}`} ·{" "}
          <span className="numeric" title={timestamp(run.createdAt)}>
            {relativeTime(run.createdAt)}
          </span>
        </>
      }
      trailing={
        <DimActions>
          {controls}
          <IconAction label={`Open run ${run.runNumber} on GitHub`} asChild>
            <a href={run.htmlUrl} target="_blank" rel="noreferrer">
              <ArrowUpRight />
            </a>
          </IconAction>
        </DimActions>
      }
    />
  )
}

/**
 * Starting a workflow: which branch it runs on, and its inputs as their
 * types ask — a switch for a boolean, a select for a choice, a box for the
 * rest — with the required ones marked and their defaults filled in.
 */
function DispatchDialog({
  workflow,
  onClose,
  onStarted,
}: {
  workflow: GitHubWorkflow
  onClose: () => void
  onStarted: () => void
}) {
  const { repo, path } = useRepo()
  const branches = usePoll(
    (signal) => get<GitHubBranchPage>(path("/branches"), { page: 1 }, signal),
    0,
    [repo.fullName],
  )
  const refs = [
    repo.defaultBranch,
    ...(branches.data?.branches ?? []).map((b) => b.name).filter((n) => n !== repo.defaultBranch),
  ]
  const [ref, setRef] = useState(repo.defaultBranch)
  const [inputs, setInputs] = useState<Record<string, string>>(() =>
    Object.fromEntries(
      workflow.dispatch.inputs.map((input) => [
        input.name,
        input.default ?? (input.type === "boolean" ? "false" : input.type === "choice" ? (input.options?.[0] ?? "") : ""),
      ]),
    ),
  )
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState<string>()
  const missing = workflow.dispatch.inputs.filter((i) => i.required && !inputs[i.name]?.trim())
  const run = async () => {
    if (missing.length > 0 || busy) return
    setBusy(true)
    setRefused(undefined)
    try {
      const result = await post<GitHubDispatchResult>(
        path(`/actions/workflows/${workflow.id}/dispatch`),
        { ref, inputs },
      )
      notify.success(`Started ${workflow.name}`, result.htmlUrl)
      onStarted()
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open
      onOpenChange={(open) => !open && !busy && onClose()}
      title={`Run ${workflow.name}`}
      size="md"
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={run} disabled={missing.length > 0} pending={busy}>
            <Play className="size-3.5" />
            Run
          </Button>
        </>
      }
    >
      <div className="space-y-4" data-slot="github-dispatch">
        <Field label="Branch" htmlFor="dispatch-ref">
          <Select value={ref} onValueChange={setRef}>
            <SelectTrigger id="dispatch-ref" className="w-full font-mono">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {refs.map((name) => (
                <SelectItem key={name} value={name} className="font-mono">
                  {name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        {workflow.dispatch.inputs.map((input) => {
          const id = `dispatch-${input.name}`
          const label = (
            <>
              <span className="font-mono">{input.name}</span>
              {input.required && <span className="text-destructive"> *</span>}
            </>
          )
          const set = (value: string) => setInputs((held) => ({ ...held, [input.name]: value }))
          if (input.type === "boolean") {
            return (
              <div key={input.name} className="flex items-center justify-between gap-3">
                <label htmlFor={id} className="text-body font-medium">
                  {label}
                  {input.description && (
                    <span className="block text-hint font-normal text-muted-foreground">
                      {input.description}
                    </span>
                  )}
                </label>
                <Switch
                  id={id}
                  checked={inputs[input.name] === "true"}
                  onCheckedChange={(on) => set(on ? "true" : "false")}
                />
              </div>
            )
          }
          return (
            <Field key={input.name} label={label} htmlFor={id} info={input.description}>
              {input.type === "choice" && input.options?.length ? (
                <Select value={inputs[input.name]} onValueChange={set}>
                  <SelectTrigger id={id} className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {input.options.map((option) => (
                      <SelectItem key={option} value={option}>
                        {option}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : (
                <Input
                  id={id}
                  inputMode={input.type === "number" ? "decimal" : undefined}
                  value={inputs[input.name] ?? ""}
                  onChange={(event) => set(event.target.value)}
                />
              )}
            </Field>
          )
        })}
        {refused && (
          <p role="alert" className="text-body text-destructive">
            {refused}
          </p>
        )}
      </div>
    </Modal>
  )
}
