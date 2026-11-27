"use client"

import { useState } from "react"
import Link from "next/link"
import { ArrowRight, Monorepo, RotateClockwise, Wrench } from "@/components/icons"
import { ApiError, post } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import type { SmartStackAddressesStarted, SmartStackState, SmartStackStep } from "@/lib/types"
import { INLINE_LINK } from "@/components/inline-link"
import { LoadingPanel } from "@/components/loading"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Stale } from "@/components/stale"
import { EmptyState, ErrorState, Notice } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { WireMark, WireNode } from "@/components/wire/wire"
import { useProject } from "@/components/deploy/project-context"
import { StepMark, type ReleaseNodeState } from "@/components/deploy/vocabulary"
import { AddressesStarted, AddressesStep } from "@/components/deploy/stack/stack-addresses"
import { StackApps, StackAppsSkeleton } from "@/components/deploy/stack/stack-apps"
import { StackLogin } from "@/components/deploy/stack/stack-login"
import {
  STACK_STATUS,
  STEP_STATUS,
  canConfigure,
  configureRefusal,
  configureVerb,
  failedSteps,
  isSourceAction,
  nextSteps,
  onlyUnpublished,
  readinessLines,
  skippedLine,
  stackPollInterval,
  stageGroups,
  stepDetail,
} from "@/components/deploy/stack/stack-model"
import { useStackState } from "@/components/deploy/stack/use-stack"

/**
 * A stack project's own page: what the stack is doing about itself.
 *
 * The deployment engine installs a stack as it installs any Compose template,
 * and every page beside this one reads that. What the engine does not do is
 * set the applications up and connect them, which the dashboard does once the
 * release is live, and this page is the reading of that: the one sentence the
 * server leads with, the state in the server's own word, and the five facts a
 * stack is asked about (installed, healthy, connected, able to fetch, able to
 * play) each said on its own, because a stack that is installed and healthy
 * with nothing connected is not ready and must not read as if it were.
 *
 * Then the one next step when something is left (a media source to add,
 * the apps' addresses to give) and what failed, with its whole error. Then
 * it is a launcher: the one login every app takes, and each app with what
 * it is for, its address and Open. Under those, the setup as a checklist,
 * each step's record behind its line. A list here keeps one height in every
 * row, and the steps a preset has no use for are one line.
 *
 * It follows a pass every two seconds and rests at fifteen (`usePoll`).
 */
export function StackPage() {
  const project = useProject()
  const { can, status: session } = useAuth()
  const { deployment } = project.detail
  const template = deployment.sourceTemplate
  // The definition says which stack a template is a preset of; until it has
  // landed nothing is known either way.
  const settled = !template || project.blueprint !== undefined
  const named = Boolean(project.blueprint?.stack)
  const [pace, setPace] = useState(stackPollInterval(undefined))
  const stack = useStackState(project.projectId, named, pace)
  // The release "Give every app its address" started, kept past the read
  // that no longer lists the apps as missing theirs.
  const [addressRun, setAddressRun] = useState<SmartStackAddressesStarted>()
  const absent =
    stack.error instanceof ApiError &&
    (stack.error.code === "stack_not_found" || stack.error.status === 404)
  // A project the server does not call a stack is not asked about again.
  const next = absent ? 0 : stackPollInterval(stack.data?.status)
  if (next !== pace) setPace(next)

  const base = `/deploy/${project.projectId}`

  if (settled && (!named || (absent && !stack.data)))
    return (
      <EmptyState
        icon={Monorepo}
        title="This project is not a stack"
        description="A stack is several applications the dashboard installs and connects. One is made from the Stacks tab of a new deployment."
        action={
          <Button size="sm" variant="outline" asChild>
            <Link href={base}>Open the Overview</Link>
          </Button>
        }
      />
    )
  if (stack.error && !stack.data)
    return <ErrorState error={stack.error} onRetry={stack.refresh} what="the stack" />
  if (!stack.data) return <StackSkeleton />

  const state = stack.data
  const admin = session?.user?.role === "admin"
  return (
    <>
      <Lead
        state={state}
        projectId={project.projectId}
        canControl={can("service.control") && !project.archived}
        stale={stack.error ? <Stale error={stack.error} onRetry={stack.refresh} /> : undefined}
        onStarted={stack.refresh}
      />
      <NextSteps
        state={state}
        projectId={project.projectId}
        projectName={project.detail.project.name}
        runHref={runHref(base, deployment.activeRun?.id)}
        activeRunId={deployment.activeRun?.id}
        canChangeDomains={can("system.admin") && !project.archived}
        addressRun={addressRun}
        onAddressesStarted={(answer) => {
          setAddressRun(answer)
          stack.refresh()
        }}
        onChanged={stack.refresh}
      />
      {/* The one login every app takes and the apps. Side by side only where
          the apps' rows keep room for what each is for (2xl); narrower, the
          login is one compact panel over them, read before an app is opened. */}
      <div className="grid min-w-0 items-start gap-x-6 gap-y-6 2xl:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="min-w-0 2xl:col-start-2 2xl:row-start-1">
          <StackLogin
            state={state}
            projectId={project.projectId}
            canReveal={admin && !project.archived}
            onChanged={stack.refresh}
          />
        </div>
        <div className="min-w-0 2xl:col-start-1 2xl:row-start-1">
          <StackApps state={state} runtime={project.detail.runtime?.services ?? []} />
        </div>
      </div>
      <Setup state={state} />
      <Limits limitations={state.limitations} />
    </>
  )
}

/** The run that is bringing the release up, or the list a finished one is in. */
function runHref(base: string, activeRun: number | undefined) {
  return activeRun ? `${base}/runs/${activeRun}` : `${base}/deployments`
}

/** The page's shape while its first read is in flight, so nothing moves when it lands. */
function StackSkeleton() {
  return (
    <div role="status" aria-label="Loading the stack" className="contents">
      <LoadingPanel plain rows={2} />
      <StackAppsSkeleton />
    </div>
  )
}

/**
 * The reading the page opens on: the stack's mark with its state in the
 * corner, the server's sentence, and the five facts under it. The one verb,
 * a pass asked for now, is on the title line (D6).
 */
function Lead({
  state,
  projectId,
  canControl,
  stale,
  onStarted,
}: {
  state: SmartStackState
  projectId: number
  canControl: boolean
  stale?: React.ReactNode
  onStarted: () => void
}) {
  const [busy, setBusy] = useState(false)
  // What the server answered a press with when it would not start a pass.
  // Words on the page: the press was a question and this is its answer.
  const [refusal, setRefusal] = useState<{ words: string; at: string }>()
  const tone = STACK_STATUS[state.status]
  const verb = configureVerb(state.status)
  const lines = readinessLines(state.readiness)

  const configure = async () => {
    setBusy(true)
    setRefusal(undefined)
    try {
      await post(`/deploy/${projectId}/stack/configure`, {})
      onStarted()
    } catch (caught) {
      const words = caught instanceof ApiError ? configureRefusal(caught.code) : undefined
      if (words) {
        setRefusal({ words, at: state.updatedAt })
        onStarted()
      } else notify.error("Could not start the stack's setup", caught)
    } finally {
      setBusy(false)
    }
  }
  // A refusal is about the moment it was given: once the state has moved on
  // it would be an old answer standing beside a new reading.
  const refused = refusal && refusal.at === state.updatedAt ? refusal.words : undefined

  return (
    <Panel plain data-slot="stack-lead">
      <PanelHeader
        title={state.title}
        count={`${state.presetTitle} · v${state.version}`}
        actions={
          <>
            {stale}
            {canControl && canConfigure(state) && (
              <Button size="sm" variant="outline" pending={busy} onClick={() => void configure()}>
                {state.status === "failed" ? <RotateClockwise /> : <Wrench />}
                {busy ? verb.progressive : verb.label}
              </Button>
            )}
          </>
        }
      />
      <PanelBody className="space-y-4 pt-4">
        <WireNode
          mark={
            <span className="relative flex">
              <WireMark tone="logo" size="md">
                <Monorepo aria-hidden />
              </WireMark>
              <StatusDot tone={tone.tone} corner="md" />
            </span>
          }
          title={<span data-slot="stack-summary">{state.summary}</span>}
          hint={
            <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
              {state.status === "configuring" ? (
                <span className="inline-flex items-center gap-1.5 text-xs font-medium">
                  <StatusDot tone={tone.tone} />
                  <TextShimmer>{tone.label}</TextShimmer>
                  <span className="sr-only" aria-live="polite" aria-atomic="true">
                    Stack state: {tone.label}
                  </span>
                </span>
              ) : (
                <Status tone={tone.tone} label={tone.label} announce="Stack state" />
              )}
              <PassAge state={state} />
            </span>
          }
        />
        <ul aria-label="What the stack can do" className="flex min-w-0 flex-wrap gap-x-5 gap-y-1.5">
          {lines.map((line) => (
            <li key={line.key} data-met={line.met} className="flex">
              <Status tone={line.met ? "running" : "unknown"} label={line.label} />
            </li>
          ))}
        </ul>
        {refused && (
          <p role="status" className="text-body leading-relaxed text-muted-foreground">
            {refused}
          </p>
        )}
      </PanelBody>
    </Panel>
  )
}

/** When the last pass ended, or that one is running: a fact beside the state. */
function PassAge({ state }: { state: SmartStackState }) {
  if (state.status === "waiting") return null
  if (state.status === "configuring")
    return state.startedAt ? (
      <span className="text-hint text-muted-foreground">
        pass started {relativeTime(state.startedAt)}
      </span>
    ) : null
  return state.finishedAt ? (
    <span className="text-hint text-muted-foreground">
      last pass {relativeTime(state.finishedAt)}
    </span>
  ) : null
}

/**
 * What is left for the person, when something is: that the release is still
 * coming up, then the next steps in the order they matter (what holds the
 * stack from ready, the apps with no address, what is optional), and what
 * failed, each failure with its whole error. The first step's command wears
 * the brand, because it is the page's one next step. Nothing is drawn when
 * all is done.
 */
function NextSteps({
  state,
  projectId,
  projectName,
  runHref,
  activeRunId,
  canChangeDomains,
  addressRun,
  onAddressesStarted,
  onChanged,
}: {
  state: SmartStackState
  projectId: number
  projectName: string
  runHref: string
  /** The deployment's run in flight, which publishes saved addresses when it goes live. */
  activeRunId?: number
  canChangeDomains: boolean
  addressRun?: SmartStackAddressesStarted
  onAddressesStarted: (answer: SmartStackAddressesStarted) => void
  onChanged: () => void
}) {
  // Once the addresses are on their way, their step is the run that brings them.
  const steps = nextSteps(state).filter((step) => !addressRun || step.kind !== "addresses")
  const failures = failedSteps(state.steps)
  if (state.status !== "waiting" && steps.length === 0 && failures.length === 0 && !addressRun)
    return null
  const base = `/deploy/${projectId}`
  return (
    <section aria-label="What is left to do" className="min-w-0 space-y-2">
      {state.status === "waiting" && (
        <Notice title="The deployment's run is still bringing the release up" live="status">
          <p>
            The apps are set up and connected once it is live; nothing can be configured before
            that.{" "}
            <Link href={runHref} className={INLINE_LINK}>
              {runHref.includes("/runs/") ? "Watch the run" : "Open Deployments"}
            </Link>
          </p>
        </Notice>
      )}
      {addressRun && <AddressesStarted started={addressRun} projectId={projectId} />}
      {steps.map((step, index) => {
        // Saved but not served yet, with a deployment under way: that run
        // publishes them when it goes live, so it is the step, not another.
        if (step.kind === "addresses" && activeRunId && onlyUnpublished(step.addresses))
          return (
            <AddressesStarted
              key="addresses"
              started={{ runId: activeRunId, domains: [] }}
              projectId={projectId}
            />
          )
        if (step.kind === "addresses")
          return (
            <AddressesStep
              key="addresses"
              addresses={step.addresses}
              projectId={projectId}
              projectName={projectName}
              stackId={state.stackId}
              canChange={canChangeDomains}
              first={index === 0}
              onStarted={onAddressesStarted}
              onSettled={onChanged}
            />
          )
        const { action } = step
        const source = isSourceAction(action)
        return (
          <Notice
            key={action.id}
            data-slot="stack-action"
            data-blocking={action.blocking}
            tone={action.blocking ? "warning" : "default"}
            title={action.title}
          >
            <p>{action.description}</p>
            <p className="text-hint text-muted-foreground">
              {serviceTitle(state, action.service)} ·{" "}
              {action.blocking ? "the stack is not ready until this is done" : "optional"}
            </p>
            {source && (
              <div className="pt-1">
                <Button size="sm" variant={index === 0 ? "default" : "outline"} asChild>
                  <Link href={`${base}/stack/sources`}>
                    Add a media source
                    <ArrowRight />
                  </Link>
                </Button>
              </div>
            )}
          </Notice>
        )
      })}
      {failures.map((step) => (
        <Notice key={step.id} data-slot="stack-failure" tone="danger" title={step.title}>
          <p className="font-mono text-hint leading-relaxed break-words">{stepDetail(step)}</p>
          <p className="text-hint text-muted-foreground">
            {serviceTitle(state, step.service)} · failed after{" "}
            {plural(Math.max(step.attempts, 1), "attempt")}. Retry runs the setup again without
            reinstalling anything.
          </p>
        </Notice>
      ))}
    </section>
  )
}

function serviceTitle(state: SmartStackState, name: string) {
  return state.services.find((service) => service.name === name)?.title ?? name
}

/**
 * The setup, stage by stage, as a checklist: each step one line of its mark
 * and what it is, in two columns from `md`, so a pass of twenty-seven steps
 * is a block the eye takes in rather than a page of text. What a step did,
 * its error and its report are behind the line, in a popover: on the line
 * they were a second sentence on every row, and a done step's "Done" said
 * twenty-six times what its tick says once. A step that is not done says
 * its state in a word; the failures' whole errors are over the apps already.
 * The steps a preset has no use for are one quiet line at the foot, with why.
 */
function Setup({ state }: { state: SmartStackState }) {
  const groups = stageGroups(state.steps)
  const skipped = skippedLine(state.steps)
  if (groups.length === 0 && !skipped) return null
  const shown = groups.reduce((total, group) => total + group.steps.length, 0)
  const done = groups.reduce((total, group) => total + group.done, 0)
  return (
    <Panel plain data-slot="stack-setup">
      <PanelHeader title="Setup" count={shown > 0 ? `${done} of ${shown} done` : undefined} />
      <PanelBody className="space-y-5 pt-3">
        {groups.map((group) => (
          <section key={group.stage} aria-label={group.title} className="min-w-0">
            <p className="eyebrow flex items-baseline justify-between gap-3">
              <span>{group.title}</span>
              <span className="numeric font-normal tracking-normal normal-case">
                {group.done} of {group.steps.length}
              </span>
            </p>
            {/* Down the first column and then the second, in the order the
                pass runs them. The lines bleed by their wash's inset, so the
                marks stand under the stage's name. */}
            <ul aria-label={group.title} className="-mx-2 mt-1.5 gap-x-6 md:columns-2">
              {group.steps.map((step) => (
                <StepLine key={step.id} step={step} />
              ))}
            </ul>
          </section>
        ))}
        {skipped && (
          <p data-slot="stack-skipped" className="text-hint leading-relaxed text-muted-foreground">
            {skipped}
          </p>
        )}
      </PanelBody>
    </Panel>
  )
}

/** A step's state as the run page's step marks draw it: a tick, a live dot, a ring. */
const STEP_MARK: Record<Exclude<SmartStackStep["status"], "skipped">, ReleaseNodeState> = {
  done: "passed",
  running: "running",
  failed: "failed",
  action_required: "warning",
  pending: "pending",
}

function StepLine({ step }: { step: SmartStackStep }) {
  if (step.status === "skipped") return null
  const reading = STEP_STATUS[step.status]
  // Done says nothing beside its tick, and waiting says it in a ring and a
  // fainter name; the rest say their state, a retry its attempt.
  const word =
    step.status === "running"
      ? step.attempts > 1
        ? `Attempt ${step.attempts}`
        : reading.label
      : step.status === "failed" || step.status === "action_required"
        ? reading.label
        : undefined
  return (
    <li data-status={step.status} className="break-inside-avoid">
      <Popover>
        <PopoverTrigger asChild>
          <button
            type="button"
            className="flex h-8 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left focus-ring transition-colors hover:bg-row-hover aria-expanded:bg-row-hover"
          >
            <StepMark state={STEP_MARK[step.status]} />
            <span
              className={cn(
                "min-w-0 flex-1 truncate text-body",
                step.status === "pending" && "text-muted-foreground",
              )}
            >
              {step.title}
            </span>
            {word && (
              <span
                className={cn(
                  "shrink-0 text-hint",
                  step.status === "failed"
                    ? "text-destructive"
                    : step.status === "action_required"
                      ? "text-warning"
                      : "text-muted-foreground",
                )}
              >
                {word}
              </span>
            )}
            <span className="sr-only">, {reading.label}</span>
          </button>
        </PopoverTrigger>
        <PopoverContent align="start" className="w-96 space-y-2">
          <StepDetail step={step} />
        </PopoverContent>
      </Popover>
    </li>
  )
}

/** What a step did, behind its line: its state and when, its words, and its last report. */
function StepDetail({ step }: { step: SmartStackStep }) {
  if (step.status === "skipped") return null
  const reading = STEP_STATUS[step.status]
  const detail = stepDetail(step)
  const at = step.finishedAt ?? step.startedAt
  return (
    <>
      <p className="text-body font-medium">{step.title}</p>
      <p className="flex flex-wrap items-center gap-x-2 text-hint text-muted-foreground">
        <Status tone={reading.tone} label={reading.label} />
        {step.attempts > 1 && <span className="numeric">{plural(step.attempts, "attempt")}</span>}
        {at && <span>{relativeTime(at)}</span>}
      </p>
      {detail && (
        <p
          className={cn(
            "text-hint leading-relaxed break-words",
            step.status === "failed" ? "text-destructive" : "text-muted-foreground",
          )}
        >
          {detail}
        </p>
      )}
      {step.log && step.log.length > 0 && (
        <pre className="max-h-40 overflow-auto rounded-md border border-hairline bg-surface-sunken px-2 py-1.5 font-mono text-micro leading-relaxed whitespace-pre-wrap text-muted-foreground">
          {step.log.join("\n")}
        </pre>
      )}
    </>
  )
}

/** What the stack does not do, said again where it is running. */
function Limits({ limitations }: { limitations: string[] }) {
  if (limitations.length === 0) return null
  return (
    <Panel plain>
      <PanelHeader title="What this stack does not do" />
      <PanelBody className="pt-3">
        <ul
          aria-label="What this stack does not do"
          className="max-w-3xl list-disc space-y-1 pl-4 text-body leading-relaxed text-muted-foreground"
        >
          {limitations.map((limitation) => (
            <li key={limitation}>{limitation}</li>
          ))}
        </ul>
      </PanelBody>
    </Panel>
  )
}
