"use client"

import { useEffect, useLayoutEffect, useRef, useState } from "react"
import Link from "next/link"
import { ArrowRight, PaperAirplane, Plus, Sparkles, StopCircle } from "@/components/icons"
import { api, errorMessage, get } from "@/lib/api"
import { useAuth } from "@/hooks/use-auth"
import type { DeploymentEngineRun } from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { FormNote } from "@/components/form"
import { Markdown } from "@/components/git/markdown"
import { Well } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { EmptyState, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Textarea } from "@/components/ui/textarea"
import { AssistantSettingsSheet } from "@/components/database/query/assistant/settings-sheet"
import { useAssistantSettings } from "@/components/database/query/assistant/use-assistant"
import { diagnosisGlyph } from "@/components/deploy/run-diagnosis-model"
import { applySuggestedLimits } from "@/components/deploy/right-size"
import { applyLines, type EnvironmentSizing } from "@/components/deploy/sizing-model"
import {
  DIAGNOSE_QUESTION,
  pendingReads,
  proposalConsequence,
  proposalRequest,
  proposalVerb,
  ranLine,
  usageLine,
  type OperatorPart,
  type OperatorProposal,
  type OperatorRan,
  type OperatorRead,
  type OperatorTurn,
} from "@/components/deploy/operator/operator-model"
import { useOperator, type OperatorState } from "@/components/deploy/operator/use-operator"

/**
 * The deployment operator: the assistant asked about one environment and the
 * server under it, opened from the project overview's Diagnose.
 *
 * It begins knowing what the overview shows — the release, the last run,
 * the health — and reads anything more only by a card the person answers
 * one read at a time: the runs, a run's transcript, the containers' last
 * lines, the usage and sizing verdict, the container events, the host's
 * alerts, the firewall's rules for the deployment's ports, the pending
 * security updates. What it proposes — a restart, a rollback, maintenance —
 * is a card too, and nothing runs until the person presses it; the press
 * calls the route the project's own button calls, with that route's
 * capability, confirmation and audit.
 */
export function OperatorSheet({
  open,
  onOpenChange,
  projectId,
  environmentId,
  environment,
  environmentName,
  deploymentName,
  onChanged,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  projectId: number
  environmentId: number
  /** `?environment=` for staging; production's is the default. */
  environment?: number
  environmentName: string
  deploymentName: string
  /** Something a proposal ran changed the project. */
  onChanged?: () => void
}) {
  const { can } = useAuth()
  const operator = useOperator(projectId, environmentId, environment)
  const settings = useAssistantSettings(open)
  const { confirm, dialog } = useConfirm()
  const [settingUp, setSettingUp] = useState(false)
  const view = settings.data
  const configured = view?.configured === true
  const canSetUp = can("system.admin")

  // Diagnose asks its question once, when the sheet opens on an empty
  // conversation with a provider to ask.
  const asked = useRef(false)
  const { transcript, ask } = operator
  useEffect(() => {
    if (!open) {
      asked.current = false
      return
    }
    if (asked.current || !configured || transcript.turns.length > 0) return
    asked.current = true
    ask(DIAGNOSE_QUESTION)
  }, [open, configured, transcript.turns.length, ask])

  let body: React.ReactNode
  if (!view) {
    body = settings.error ? (
      <Notice tone="danger" title="The assistant's settings could not be read" className="m-4">
        {errorMessage(settings.error)}
      </Notice>
    ) : (
      <div aria-hidden className="space-y-3 p-4">
        <Skeleton className="h-3 w-40" />
        <Skeleton className="h-3 w-56" />
      </div>
    )
  } else if (!configured) {
    body = (
      <div className="space-y-3 p-4">
        {view.problem && (
          <Notice tone="warning" title="The saved provider cannot be read">
            {view.problem}
          </Notice>
        )}
        <EmptyState
          className="border-0 px-3 py-8"
          icon={Sparkles}
          title="The assistant is off"
          description={
            canSetUp
              ? "Choose who answers it — Claude with your Anthropic key, or a model on a server of your own — and Diagnose reads this deployment with your leave, a read at a time."
              : "An administrator turns it on by choosing who answers it."
          }
          action={
            canSetUp && (
              <Button size="sm" variant="outline" onClick={() => setSettingUp(true)}>
                Set up the assistant
              </Button>
            )
          }
        />
      </div>
    )
  } else {
    body = (
      <Conversation
        operator={operator}
        projectId={projectId}
        environmentId={environmentId}
        environmentName={environmentName}
        destination={
          view.provider === "anthropic"
            ? "Anthropic"
            : view.baseUrl
              ? new URL(view.baseUrl).host
              : "the server"
        }
        confirm={confirm}
        onChanged={onChanged}
      />
    )
  }

  return (
    <>
      <SidePanel
        open={open}
        onOpenChange={onOpenChange}
        width="md"
        initialFocus="body"
        bodyClassName="flex min-h-0 flex-col p-0"
        title={
          <>
            <Sparkles aria-hidden className="size-4 text-muted-foreground" />
            Diagnose {deploymentName}
          </>
        }
        description={`The assistant, asked about ${environmentName} of ${deploymentName} and the server under it. It reads more only with your leave, and runs nothing.`}
        actions={
          configured && (
            <>
              <span className="mr-auto flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
                <ProductGlyph id={diagnosisGlyph(view.provider)} />
                <span className="truncate">{view.modelLabel}</span>
                <span aria-hidden>·</span>
                <span className="truncate">{environmentName}</span>
              </span>
              {transcript.turns.length > 0 && (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={operator.busy}
                  onClick={operator.reset}
                >
                  <Plus />
                  New conversation
                </Button>
              )}
            </>
          )
        }
      >
        {body}
      </SidePanel>
      {canSetUp && (
        <AssistantSettingsSheet
          open={settingUp}
          settings={view}
          onClose={() => setSettingUp(false)}
          onSaved={() => settings.refresh()}
          confirm={confirm}
        />
      )}
      {dialog}
    </>
  )
}

type Confirm = ReturnType<typeof useConfirm>["confirm"]

function Conversation({
  operator,
  projectId,
  environmentId,
  environmentName,
  destination,
  confirm,
  onChanged,
}: {
  operator: OperatorState
  projectId: number
  environmentId: number
  environmentName: string
  destination: string
  confirm: Confirm
  onChanged?: () => void
}) {
  const { transcript, busy } = operator
  const log = useRef<HTMLOListElement>(null)
  const following = useRef(true)
  useLayoutEffect(() => {
    const list = log.current
    if (list && following.current) list.scrollTop = list.scrollHeight
  }, [transcript])
  const last = transcript.turns.at(-1)
  const waiting = pendingReads(transcript)

  const press = usePress(projectId, environmentId, environmentName, operator, confirm, onChanged)

  return (
    <>
      <ol
        ref={log}
        aria-label="Conversation with the assistant"
        aria-busy={busy}
        onScroll={(event) => {
          const list = event.currentTarget
          following.current = list.scrollHeight - list.scrollTop - list.clientHeight < 48
        }}
        className="min-h-0 flex-1 space-y-5 overflow-y-auto p-4"
      >
        {transcript.turns.length === 0 && (
          <li>
            <EmptyState
              className="border-0 px-2 py-8"
              icon={Sparkles}
              title={`Ask about ${environmentName}`}
              description="It is told what the overview shows. Each read it asks for waits for your Allow, and an action it proposes runs only when you press it."
              action={
                <Button size="sm" variant="outline" onClick={() => operator.ask(DIAGNOSE_QUESTION)}>
                  Diagnose
                </Button>
              }
            />
          </li>
        )}
        {transcript.turns.map((turn) => (
          <TurnView
            key={turn.id}
            turn={turn}
            current={turn === last}
            onDecide={operator.decide}
            onPress={press}
            projectId={projectId}
          />
        ))}
      </ol>
      <Composer
        busy={busy}
        waiting={waiting.length > 0}
        destination={destination}
        environmentName={environmentName}
        onAsk={operator.ask}
        onStop={operator.stop}
      />
    </>
  )
}

/**
 * A proposal's press: the confirmation the project's own button asks for a
 * destructive action, then that button's route. What it answered is written
 * into the card.
 */
function usePress(
  projectId: number,
  environmentId: number,
  environmentName: string,
  operator: OperatorState,
  confirm: Confirm,
  onChanged?: () => void,
) {
  const { settle } = operator
  const execute = async (proposal: OperatorProposal) => {
    settle(proposal.id, { state: "working" })
    const request = proposalRequest(proposal, projectId, environmentId)
    try {
      if (request.method === "sizing") {
        const sizing = await get<EnvironmentSizing>(
          `/deploy/${projectId}/environments/${environmentId}/sizing`,
        )
        if (!sizing.apply) throw new Error("The right-sizing verdict has no limits to suggest now.")
        await applySuggestedLimits(sizing)
        settle(proposal.id, {
          state: "done",
          note: `Saved ${applyLines(sizing).join(", ")}. They apply with the next release.`,
        })
      } else if (request.startsRun) {
        const run = await api<DeploymentEngineRun>(request.path, {
          method: request.method,
          body: request.body,
        })
        settle(proposal.id, {
          state: "done",
          note: `Run #${run.runNumber} started.`,
          href: `/deploy/${projectId}/runs/${run.id}`,
        })
      } else {
        await api(request.path, { method: request.method, body: request.body })
        settle(proposal.id, {
          state: "done",
          note:
            proposal.action === "maintenance_on"
              ? "Maintenance is on."
              : "Maintenance is off; the application answers again.",
        })
      }
      onChanged?.()
    } catch (error) {
      settle(proposal.id, { state: "failed", note: errorMessage(error) })
    }
  }
  return (proposal: OperatorProposal) => {
    if (!proposal.destructive) {
      void execute(proposal)
      return
    }
    confirm({
      title: proposalVerb(proposal),
      description: proposalConsequence(proposal, environmentName),
      confirmLabel: proposalVerb(proposal),
      action: async () => {
        await execute(proposal)
        return "reported"
      },
    })
  }
}

function TurnView({
  turn,
  current,
  onDecide,
  onPress,
  projectId,
}: {
  turn: OperatorTurn
  current: boolean
  onDecide: (id: string | "all", allow: boolean) => void
  onPress: (proposal: OperatorProposal) => void
  projectId: number
}) {
  const settled = !["waiting", "thinking", "writing"].includes(turn.phase)
  // What each read came to, for its card to say.
  const ran = new Map<string, OperatorRan>()
  for (const part of turn.parts)
    if (part.kind === "ran") for (const call of part.calls) ran.set(call.id, call)
  const usage = settled ? usageLine(turn) : undefined
  return (
    <li className="space-y-2.5" data-turn={turn.phase}>
      <div className="ml-6 rounded-lg bg-surface-header px-3 py-2">
        <p className="text-body break-words whitespace-pre-wrap">{turn.question}</p>
      </div>
      <div className="min-w-0 space-y-2.5">
        {turn.parts.map((part, at) => (
          <PartView
            key={at}
            part={part}
            ran={ran}
            active={current && turn.phase === "asking"}
            onDecide={onDecide}
            onPress={onPress}
            projectId={projectId}
          />
        ))}
        {(turn.phase === "waiting" || turn.phase === "thinking") && (
          <p role="status" className="text-body">
            <TextShimmer>{turn.phase === "thinking" ? "Thinking…" : "Asking…"}</TextShimmer>
          </p>
        )}
        {turn.phase === "stopped" && (
          <p className="text-hint text-muted-foreground">
            Stopped. Nothing of this answer is kept.
          </p>
        )}
        {turn.phase === "failed" && (
          <Notice tone="danger" title="No answer">
            {turn.error ?? "The assistant did not answer."}
          </Notice>
        )}
        {turn.phase === "refused" && (
          <Notice tone="warning" title="The model declined to answer this">
            Put the question another way. Nothing of this turn is kept.
          </Notice>
        )}
        {turn.phase === "cut" && (
          <p className="text-hint text-muted-foreground">
            The answer reached its length limit and was cut off.
          </p>
        )}
        {usage && <p className="numeric text-hint text-muted-foreground">{usage}</p>}
      </div>
    </li>
  )
}

function PartView({
  part,
  ran,
  active,
  onDecide,
  onPress,
  projectId,
}: {
  part: OperatorPart
  ran: Map<string, OperatorRan>
  active: boolean
  onDecide: (id: string | "all", allow: boolean) => void
  onPress: (proposal: OperatorProposal) => void
  projectId: number
}) {
  switch (part.kind) {
    case "text":
      return (
        <Markdown
          source={part.text}
          code={(block) => (
            <Well className="max-h-96 p-2.5 text-hint whitespace-pre" aria-label={block.lang}>
              {block.v}
            </Well>
          )}
        />
      )
    case "reads":
      return (
        <ReadsCard
          calls={part.calls}
          decisions={part.decisions}
          sent={Boolean(part.sent)}
          ran={ran}
          active={active && !part.sent}
          onDecide={onDecide}
        />
      )
    case "ran":
      // Said on the card that asked for it.
      return null
    case "proposal":
      return (
        <ProposalCard
          proposal={part.proposal}
          outcome={part.outcome}
          onPress={onPress}
          projectId={projectId}
        />
      )
  }
}

/**
 * The reads the assistant asks for, each with its own Allow: nothing it
 * names is read, nor sent anywhere, until the person allows that read.
 */
function ReadsCard({
  calls,
  decisions,
  sent,
  ran,
  active,
  onDecide,
}: {
  calls: OperatorRead[]
  decisions: Record<string, boolean>
  sent: boolean
  ran: Map<string, OperatorRan>
  active: boolean
  onDecide: (id: string | "all", allow: boolean) => void
}) {
  const open = calls.filter((call) => decisions[call.id] === undefined)
  return (
    <section
      data-slot="operator-reads"
      aria-label={
        calls.length === 1 ? "A read the assistant asks for" : "Reads the assistant asks for"
      }
      className="space-y-2.5 rounded-lg border border-hairline bg-card p-3"
    >
      <div className="space-y-0.5">
        <p className="text-body font-medium">
          {sent
            ? calls.length === 1
              ? "It asked to read"
              : `It asked for ${calls.length} reads`
            : calls.length === 1
              ? "It asks to read"
              : `It asks for ${calls.length} reads`}
        </p>
        <p className="text-hint leading-relaxed text-muted-foreground">
          Read on this server with your role, secret values masked, and sent to the model.
        </p>
      </div>
      <ul className="divide-y divide-hairline">
        {calls.map((call) => {
          const decided = decisions[call.id]
          const result = ran.get(call.id)
          return (
            <li
              key={call.id}
              className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 py-2 first:pt-0 last:pb-0"
            >
              <span className="min-w-0 flex-1 text-body">{call.label}</span>
              {result ? (
                <span className="text-hint text-muted-foreground">{ranLine(result)}</span>
              ) : decided !== undefined ? (
                <span className="text-hint font-medium text-muted-foreground">
                  {decided ? "Allowed" : "Declined"}
                </span>
              ) : (
                active && (
                  <span className="flex shrink-0 gap-1.5">
                    <Button
                      size="xs"
                      aria-label={`Allow: ${call.label}`}
                      onClick={() => onDecide(call.id, true)}
                    >
                      Allow
                    </Button>
                    <Button
                      size="xs"
                      variant="outline"
                      aria-label={`Decline: ${call.label}`}
                      onClick={() => onDecide(call.id, false)}
                    >
                      Decline
                    </Button>
                  </span>
                )
              )}
            </li>
          )
        })}
      </ul>
      {active && open.length > 1 && (
        <div className="flex flex-wrap gap-2 border-t border-hairline pt-2.5">
          <Button size="sm" onClick={() => onDecide("all", true)}>
            Allow all
          </Button>
          <Button size="sm" variant="outline" onClick={() => onDecide("all", false)}>
            Decline all
          </Button>
        </div>
      )}
      {sent && calls.every((call) => !ran.has(call.id)) && (
        <p role="status" className="text-hint">
          <TextShimmer>Reading…</TextShimmer>
        </p>
      )}
    </section>
  )
}

/**
 * An action the assistant proposed. Nothing has run: the press is the
 * project's own verb for it, refused to a role that may not use it.
 */
function ProposalCard({
  proposal,
  outcome,
  onPress,
  projectId,
}: {
  proposal: OperatorProposal
  outcome?: { state: "working" | "done" | "failed"; note?: string; href?: string }
  onPress: (proposal: OperatorProposal) => void
  projectId: number
}) {
  const { can } = useAuth()
  const allowed = can(proposal.capability)
  const verb = proposalVerb(proposal)
  return (
    <section
      data-slot="operator-proposal"
      aria-label={`Proposed: ${proposal.label}`}
      className="space-y-2 rounded-lg border border-hairline bg-card p-3"
    >
      <p className="eyebrow">Proposed</p>
      <p className="text-body font-medium">{proposal.label}</p>
      <p className="text-body leading-relaxed text-muted-foreground">{proposal.reason}</p>
      {outcome?.state === "done" ? (
        <p className="flex flex-wrap items-center gap-x-2 text-hint">
          <span>{outcome.note}</span>
          {outcome.href && (
            <Link
              href={outcome.href}
              className="inline-flex items-center gap-1 rounded-sm font-medium text-foreground focus-ring hover:underline"
            >
              Open the run <ArrowRight className="size-3" />
            </Link>
          )}
        </p>
      ) : (
        <>
          {outcome?.state === "failed" && (
            <Notice tone="danger" title={`Could not ${verb.toLowerCase()}`}>
              {outcome.note}
            </Notice>
          )}
          {allowed ? (
            <div className="flex flex-wrap items-center gap-2 pt-0.5">
              <Button
                size="sm"
                variant={proposal.destructive ? "destructive" : "default"}
                pending={outcome?.state === "working"}
                disabled={outcome?.state === "working"}
                onClick={() => onPress(proposal)}
              >
                {verb}
              </Button>
              <FormNote>
                Nothing has run. This is the project&rsquo;s own {verb.toLowerCase()}, audited as
                yours.
              </FormNote>
            </div>
          ) : (
            <FormNote>
              Your role cannot do this ({proposal.capability}). Someone who can does it from the{" "}
              <Link href={`/deploy/${projectId}`} className="underline">
                project&rsquo;s page
              </Link>
              .
            </FormNote>
          )}
        </>
      )}
    </section>
  )
}

/** The question being written. Asking something else declines the reads waiting. */
function Composer({
  busy,
  waiting,
  destination,
  environmentName,
  onAsk,
  onStop,
}: {
  busy: boolean
  waiting: boolean
  destination: string
  environmentName: string
  onAsk: (question: string) => void
  onStop: () => void
}) {
  const [question, setQuestion] = useState("")
  const ready = !busy && question.trim() !== ""
  const send = () => {
    if (!ready) return
    onAsk(question.trim())
    setQuestion("")
  }
  return (
    <form
      data-slot="operator-composer"
      className="shrink-0 space-y-2 border-t border-hairline bg-card p-3"
      onSubmit={(event) => {
        event.preventDefault()
        send()
      }}
    >
      {waiting && <FormNote>Asking something else declines the reads it is waiting on.</FormNote>}
      <Textarea
        aria-label="Ask the assistant"
        placeholder={`Why did the last deploy of ${environmentName} fail?`}
        value={question}
        maxLength={4000}
        rows={2}
        className="max-h-40 min-h-16 resize-none"
        onChange={(event) => setQuestion(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
            event.preventDefault()
            send()
          }
        }}
      />
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <FormNote className="mr-auto">
          {`Sends ${destination} your question and what the overview shows about ${environmentName}.`}
        </FormNote>
        {busy ? (
          <Button type="button" size="sm" variant="outline" onClick={onStop}>
            <StopCircle />
            Stop
          </Button>
        ) : (
          <Button type="submit" size="sm" disabled={!ready}>
            <PaperAirplane />
            Ask
          </Button>
        )}
      </div>
    </form>
  )
}
