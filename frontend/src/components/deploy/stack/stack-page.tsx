"use client"

import { useState } from "react"
import Link from "next/link"
import { Copy, External, Eye, EyeOff, Monorepo, RotateClockwise, Wrench } from "@/components/icons"
import { ApiError, get, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import type {
  SmartStackCredential,
  SmartStackService,
  SmartStackState,
  SmartStackStep,
} from "@/lib/types"
import { ChoiceList, ChoiceListSkeleton, ChoiceRow } from "@/components/choice-row"
import { CopyValue } from "@/components/copy"
import { Explain } from "@/components/explain"
import { LoadingPanel } from "@/components/loading"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo, hasProductLogo, imageProduct } from "@/components/product-logo"
import { Stale } from "@/components/stale"
import { EmptyState, ErrorState, Notice } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbActions, type Verb } from "@/components/verbs"
import { WireMark, WireNode } from "@/components/wire/wire"
import { useProject } from "@/components/deploy/project-context"
import { RevealedValue } from "@/components/deploy/settings/variable-type"
import { hostOf } from "@/components/deploy/vocabulary"
import {
  ACCESS_WORD,
  STACK_STATUS,
  STEP_STATUS,
  canConfigure,
  configureRefusal,
  configureVerb,
  failedSteps,
  orderedActions,
  readinessLines,
  serviceState,
  skippedLine,
  splitCredentials,
  stackPollInterval,
  stageGroups,
  stepDetail,
  tunnelFor,
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
 * Then, in the order they are acted on: what only the person can do, what
 * failed, the services and how each is reached, the setup step by step, and
 * the accounts the stack created. A list here keeps one height in every row:
 * a private service's tunnel is behind its mark, a failed step's whole error
 * stands above the list, and the steps a preset has no use for are one line.
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
  return (
    <>
      <Lead
        state={state}
        projectId={project.projectId}
        canControl={can("service.control") && !project.archived}
        stale={stack.error ? <Stale error={stack.error} onRetry={stack.refresh} /> : undefined}
        onStarted={stack.refresh}
      />
      <NeedsALook state={state} runHref={runHref(base, deployment.activeRun?.id)} />
      <Services state={state} runtime={project.detail.runtime?.services ?? []} />
      <Setup state={state} />
      <Accounts
        state={state}
        projectId={project.projectId}
        canReveal={session?.user?.role === "admin"}
      />
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
      <Panel plain>
        <PanelHeader title="Services" />
        <PanelBody className="pt-3">
          <ChoiceListSkeleton rows={4} />
        </PanelBody>
      </Panel>
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
 * What needs a look, when something does: that the release is still coming
 * up, what only the person can do (the ones holding the stack from ready
 * first), and what failed, each failure with its whole error. Nothing is
 * drawn when all is well.
 */
function NeedsALook({ state, runHref }: { state: SmartStackState; runHref: string }) {
  const actions = orderedActions(state.actions)
  const failures = failedSteps(state.steps)
  if (state.status !== "waiting" && actions.length === 0 && failures.length === 0) return null
  return (
    <section aria-label="What needs a look" className="min-w-0 space-y-2">
      {state.status === "waiting" && (
        <Notice title="The deployment's run is still bringing the release up" live="status">
          <p>
            The applications are set up and connected once it is live; nothing can be configured
            before that.{" "}
            <Link href={runHref} className="rounded-sm underline underline-offset-4 focus-ring">
              {runHref.includes("/runs/") ? "Watch the run" : "Open Deployments"}
            </Link>
          </p>
        </Notice>
      )}
      {actions.map((action) => (
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
        </Notice>
      ))}
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

/** A service's mark: its own where the product has one, else what its image is. */
function serviceProduct(service: Pick<SmartStackService, "name" | "image">) {
  return hasProductLogo(service.name) ? service.name : imageProduct(service.image)
}

/**
 * The services as cards, one height each: what it is, what it does, its
 * state, and how it is reached. A card opens its container in Docker where
 * there is one. A public service's address is a link; a private one's is an
 * address on this server only, with the tunnel that reaches it behind the
 * mark beside it, and never a button that would open nothing.
 */
function Services({
  state,
  runtime,
}: {
  state: SmartStackState
  runtime: { containerId: string; name: string; service?: string }[]
}) {
  // The stack names a container; the project's runtime read holds its id,
  // which is what the Docker pages are addressed by.
  const containerId = (service: SmartStackService) =>
    runtime.find(
      (one) =>
        one.name === service.container?.name || (one.service && one.service === service.name),
    )?.containerId
  const host = typeof window === "undefined" ? "" : window.location.hostname
  return (
    <Panel plain>
      <PanelHeader title="Services" count={state.services.length} />
      <PanelBody className="pt-3">
        <ChoiceList aria-label="Services">
          {state.services.map((service) => {
            const id = service.container ? containerId(service) : undefined
            const reading = serviceState(service)
            const tunnel =
              service.access === "private" ? tunnelFor(service.address, host) : undefined
            const verbs: Verb[] =
              service.access === "public" && service.url
                ? [
                    {
                      key: "open",
                      label: `Open ${service.title} at ${hostOf(service.url)}`,
                      icon: External,
                      inline: true,
                      href: service.url,
                      run: () => undefined,
                    },
                  ]
                : []
            return (
              <ChoiceRow
                key={service.name}
                verb={`Open ${service.title}'s container`}
                href={id ? `/docker/containers/${id}` : undefined}
                disabled={!id}
                leading={<ProductLogo id={serviceProduct(service)} size="sm" />}
                title={service.title}
                description={service.purpose}
                trailing={
                  <>
                    <Reach service={service} />
                    <span className="flex justify-end sm:w-28 sm:justify-start">
                      <Status tone={reading.tone} label={reading.label} />
                    </span>
                  </>
                }
                actions={
                  tunnel ? (
                    <Explain
                      label={`How to reach ${service.title}`}
                      side="left"
                      entry={{
                        title: "Only on this server",
                        body: (
                          <span className="block space-y-2">
                            <span className="block">
                              {service.title} is published at{" "}
                              <span className="font-mono">{service.address}</span>, which only this
                              server can open. From another computer, open a tunnel over SSH with
                              your own account, then visit{" "}
                              <span className="font-mono">{tunnel.url}</span> there.
                            </span>
                            <CopyValue
                              framed
                              wrap="anywhere"
                              value={tunnel.command}
                              label={`Copy the tunnel command for ${service.title}`}
                            />
                          </span>
                        ),
                      }}
                    />
                  ) : verbs.length > 0 ? (
                    <VerbActions dim verbs={verbs} />
                  ) : undefined
                }
              />
            )
          })}
        </ChoiceList>
      </PanelBody>
    </Panel>
  )
}

/** Where a service answers, in the row's line: a phone keeps the state and drops this. */
function Reach({ service }: { service: SmartStackService }) {
  const word = (
    <span className="caps text-micro text-muted-foreground">{ACCESS_WORD[service.access]}</span>
  )
  if (service.access === "public" && service.url)
    return (
      <span className="flex min-w-0 items-center gap-2 max-md:hidden">
        {word}
        <span className="max-w-56 truncate font-mono text-hint text-foreground">
          {hostOf(service.url)}
        </span>
      </span>
    )
  if (service.access === "private" && service.address)
    return (
      <span className="flex min-w-0 items-center gap-2 max-md:hidden">
        {word}
        <span className="font-mono text-hint text-foreground">{service.address}</span>
      </span>
    )
  return <span className="max-md:hidden">{word}</span>
}

/**
 * The setup, stage by stage. Each step is one line: its state, what it is,
 * and what it did. The steps a preset has no use for are one quiet line at
 * the foot, with why.
 */
function Setup({ state }: { state: SmartStackState }) {
  const groups = stageGroups(state.steps)
  const skipped = skippedLine(state.steps)
  if (groups.length === 0 && !skipped) return null
  const shown = groups.reduce((total, group) => total + group.steps.length, 0)
  const done = groups.reduce((total, group) => total + group.done, 0)
  return (
    <Panel plain>
      <PanelHeader title="Setup" count={shown > 0 ? `${done} of ${shown} done` : undefined} />
      <PanelBody className="space-y-4 pt-3">
        {groups.map((group) => (
          <section key={group.stage} aria-label={group.title} className="min-w-0">
            <p className="eyebrow flex items-baseline justify-between gap-3">
              <span>{group.title}</span>
              <span className="numeric font-normal tracking-normal normal-case">
                {group.done} of {group.steps.length}
              </span>
            </p>
            <ul aria-label={group.title} className="mt-1 divide-y divide-hairline">
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

function StepLine({ step }: { step: SmartStackStep }) {
  if (step.status === "skipped") return null
  const reading = STEP_STATUS[step.status]
  const detail = stepDetail(step)
  return (
    <li data-status={step.status} className="flex h-9 min-w-0 items-center gap-3">
      <span className="flex w-24 shrink-0">
        <Status tone={reading.tone} label={reading.label} />
      </span>
      <span
        className="min-w-0 truncate text-body max-sm:flex-1 sm:max-w-[60%] sm:shrink-0"
        title={step.title}
      >
        {step.title}
      </span>
      {detail && (
        <span
          title={detail}
          className={cn(
            "min-w-0 flex-1 truncate text-hint max-sm:hidden",
            step.status === "failed" ? "text-destructive" : "text-muted-foreground",
          )}
        >
          {detail}
        </span>
      )}
    </li>
  )
}

/**
 * The accounts the stack created, each with a Reveal that reads its password
 * through the audited route and draws it where the mask was, as a variable's
 * is on the Variables page. Only an account that exists is listed.
 */
function Accounts({
  state,
  projectId,
  canReveal,
}: {
  state: SmartStackState
  projectId: number
  canReveal: boolean
}) {
  const { stored, pending } = splitCredentials(state.credentials)
  const [revealed, setRevealed] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState("")
  if (state.credentials.length === 0) return null

  const read = (credential: SmartStackCredential) =>
    get<{ name: string; username?: string; password: string }>(
      `/deploy/${projectId}/stack/credentials/${encodeURIComponent(credential.name)}/reveal`,
    )
  const reveal = async (credential: SmartStackCredential) => {
    setBusy(`reveal-${credential.name}`)
    try {
      const result = await read(credential)
      setRevealed((current) => ({ ...current, [credential.name]: result.password }))
    } catch (caught) {
      notify.error(`Could not reveal the password for ${credential.label}`, caught)
    } finally {
      setBusy("")
    }
  }
  const hide = (credential: SmartStackCredential) =>
    setRevealed((current) => {
      const next = { ...current }
      delete next[credential.name]
      return next
    })
  // Copies without drawing, for a screen somebody else can see.
  const copy = async (credential: SmartStackCredential) => {
    setBusy(`copy-${credential.name}`)
    try {
      const result = await read(credential)
      await copyText(result.password, `Password for ${credential.label} copied`)
    } catch (caught) {
      notify.error(`Could not copy the password for ${credential.label}`, caught)
    } finally {
      setBusy("")
    }
  }

  return (
    <Panel plain>
      <PanelHeader title="Accounts the stack created" count={stored.length || undefined} />
      <PanelBody className="space-y-3 pt-3">
        {stored.length > 0 && (
          <ChoiceList aria-label="Accounts the stack created">
            {stored.map((credential) => {
              const shown = revealed[credential.name]
              const verbs: Verb[] = canReveal
                ? [
                    {
                      key: "reveal",
                      label:
                        shown !== undefined
                          ? `Hide the password for ${credential.label}`
                          : `Reveal the password for ${credential.label}`,
                      icon: shown !== undefined ? EyeOff : Eye,
                      inline: true,
                      disabled: Boolean(busy),
                      run: () => (shown !== undefined ? hide(credential) : void reveal(credential)),
                    },
                    {
                      key: "copy",
                      label: `Copy the password for ${credential.label}`,
                      icon: Copy,
                      inline: true,
                      disabled: Boolean(busy),
                      run: () => void copy(credential),
                    },
                  ]
                : []
              const service = state.services.find((one) => one.name === credential.service)
              return (
                <ChoiceRow
                  key={credential.name}
                  // A reading with two verbs of its own and nowhere to go.
                  disabled
                  verb={credential.label}
                  leading={
                    <ProductLogo
                      id={service ? serviceProduct(service) : credential.service}
                      size="sm"
                    />
                  }
                  title={credential.label}
                  description={
                    <span className="flex min-w-0 items-center gap-2">
                      {credential.username && (
                        <span className="shrink-0 font-mono text-foreground">
                          {credential.username}
                        </span>
                      )}
                      <span className="min-w-0 flex-1">
                        {shown !== undefined ? (
                          <RevealedValue name={`${credential.label} password`} value={shown} />
                        ) : (
                          <span
                            className="block h-4 truncate leading-4"
                            aria-label="Password hidden"
                          >
                            ••••••••••••
                          </span>
                        )}
                      </span>
                    </span>
                  }
                  actions={
                    verbs.length > 0 ? (
                      <VerbActions
                        dim
                        verbs={verbs}
                        menuLabel={`Actions for ${credential.label}`}
                      />
                    ) : undefined
                  }
                />
              )
            })}
          </ChoiceList>
        )}
        <p className="text-hint leading-relaxed text-muted-foreground">
          {[
            pending > 0 &&
              `${plural(pending, "more account")} ${pending === 1 ? "appears" : "appear"} once ${pending === 1 ? "its application is" : "their applications are"} set up.`,
            stored.length > 0 &&
              (canReveal
                ? "Reading a password is recorded in the audit log."
                : "An administrator can reveal these passwords."),
          ]
            .filter(Boolean)
            .join(" ")}
        </p>
      </PanelBody>
    </Panel>
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
