"use client"

import { useEffect, useMemo, useState } from "react"
import { Plus, Sparkles } from "@/components/icons"
import {
  assistantDestination,
  assistantProduct,
  type AssistantMode,
  type AssistantSettings,
} from "@/components/assistant/settings"
import { AssistantMark } from "@/components/assistant/mark"
import { ModelPicker } from "@/components/assistant/model-picker"
import { Conversation } from "@/components/assistant/elements/conversation"
import { PromptInput } from "@/components/assistant/elements/prompt"
import { Suggestion } from "@/components/assistant/elements/suggestion"
import { ModeMenu } from "@/components/assistant/agent/mode-menu"
import { ApprovalCard, LinkCard, StepRow } from "@/components/assistant/agent/step"
import {
  answering,
  pendingSteps,
  usageLine,
  type AgentPart,
  type AgentStep,
  type AgentTurn,
} from "@/components/assistant/agent/model"
import {
  agentBusy,
  agentModelChanged,
  askAgent,
  decideAgent,
  resetAgent,
  setAgentMode,
  stopAgent,
  type AgentSession,
} from "@/components/assistant/agent/sessions"
import { FormNote } from "@/components/form"
import { Markdown } from "@/components/git/markdown"
import { Well } from "@/components/panel"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { cn } from "@/lib/utils"
import type { Icon } from "@/components/icons"

export interface AgentSuggestion {
  label: string
  question: string
  product?: string
  icon?: Icon
}

/**
 * The conversation: what was asked, what it did as it did it, what it is waiting on,
 * and the box to ask in. The same for a deployment's page and a new deployment's
 * setup; only what it is about differs, and every place that adds the assistant
 * later draws this.
 */
export function AgentChat({
  session,
  settings,
  title,
  description,
  placeholder,
  suggestions = [],
  compact,
  sends,
}: {
  session: AgentSession
  settings: AssistantSettings | undefined
  /** Said on an empty conversation. */
  title: string
  description: string
  placeholder: string
  suggestions?: AgentSuggestion[]
  /** A narrow column beside a form, rather than a page. */
  compact?: boolean
  /** What goes with a question, in the line under the box. */
  sends: string
}) {
  const { transcript } = session
  const busy = agentBusy(session)
  const product = assistantProduct(settings)
  const mode: AssistantMode = session.mode ?? settings?.defaultMode ?? "changes"
  const last = transcript.turns.at(-1)
  const waiting = pendingSteps(transcript)
  const ask = (question: string) => askAgent(session, question)
  return (
    <div data-slot="agent-chat" className="flex min-h-0 flex-1 flex-col">
      <Conversation
        label="Conversation with the assistant"
        busy={busy}
        contentClassName={cn(!compact && "mx-auto w-full max-w-3xl", compact && "gap-5 p-3")}
      >
        {transcript.turns.length === 0 && (
          <div
            data-slot="agent-empty"
            className={cn(
              "flex animate-rise flex-col items-center gap-3 text-center",
              compact ? "py-6" : "py-10",
            )}
          >
            <AssistantMark product={product} size="md" />
            <div className="space-y-1">
              <h2 className="text-lg font-semibold tracking-tight">{title}</h2>
              <p className="mx-auto max-w-md text-body leading-relaxed text-muted-foreground">
                {description}
              </p>
            </div>
            {suggestions.length > 0 && (
              <div className="flex flex-wrap justify-center gap-2 pt-2">
                {suggestions.map((suggestion) => (
                  <Suggestion key={suggestion.label} {...suggestion} onAsk={ask} />
                ))}
              </div>
            )}
          </div>
        )}
        {transcript.turns.map((turn) => (
          <TurnView
            key={turn.id}
            turn={turn}
            product={product}
            current={turn === last}
            onDecide={(id, allow) => decideAgent(session, id, allow)}
          />
        ))}
      </Conversation>
      {transcript.tools === false && (
        <div className={cn("px-3", !compact && "mx-auto w-full max-w-3xl")}>
          <FormNote tone="warning">
            This model is set up without tools, so it can only answer from what the page shows. Turn
            tools on in its settings, or switch to a provider that has them.
          </FormNote>
        </div>
      )}
      <div className={cn(!compact && "mx-auto w-full max-w-3xl")}>
        <PromptInput
          busy={busy}
          label="Ask the assistant"
          placeholder={placeholder}
          rows={compact ? 1 : 2}
          note={
            waiting.length > 0 && (
              <FormNote>Asking something else declines what it is waiting on.</FormNote>
            )
          }
          toolbar={
            <>
              <ModelPicker settings={settings} onChanged={agentModelChanged} />
              <ModeMenu mode={mode} onChange={(next) => setAgentMode(session, next)} />
              {transcript.turns.length > 0 && (
                <Button
                  type="button"
                  size="xs"
                  variant="ghost"
                  disabled={busy}
                  aria-label="New conversation"
                  onClick={() => resetAgent(session)}
                  className="text-muted-foreground"
                >
                  <Plus />
                  <span className="max-sm:hidden">New</span>
                </Button>
              )}
            </>
          }
          onAsk={ask}
          onStop={() => stopAgent(session)}
        />
        <p className="px-4 pb-2.5 text-micro text-muted-foreground">
          {`Sends ${assistantDestination(settings)} your question, ${sends}, with secret values masked.`}
        </p>
      </div>
    </div>
  )
}

/** The parts of an answer, with the steps next to each other drawn as one list. */
type Group =
  | { kind: "text"; text: string; last: boolean }
  | { kind: "steps"; steps: AgentStep[] }
  | { kind: "waiting"; steps: AgentStep[] }
  | { kind: "link"; part: Extract<AgentPart, { kind: "link" }> }

function groupsOf(parts: AgentPart[]): Group[] {
  const out: Group[] = []
  for (const [at, part] of parts.entries()) {
    if (part.kind === "text") {
      out.push({ kind: "text", text: part.text, last: at === parts.length - 1 })
    } else if (part.kind === "link") {
      out.push({ kind: "link", part })
    } else if (part.call.kind !== "free" || part.state === "failed") {
      // Looking up a route is not worth a row, unless it came to nothing.
      const kind = part.state === "waiting" ? "waiting" : "steps"
      const held = out.at(-1)
      if (held?.kind === kind) held.steps.push(part)
      else out.push({ kind, steps: [part] })
    }
  }
  return out
}

function TurnView({
  turn,
  product,
  current,
  onDecide,
}: {
  turn: AgentTurn
  product?: string
  current: boolean
  onDecide: (id: string | "all", allow: boolean) => void
}) {
  const working = answering(turn)
  const groups = useMemo(() => groupsOf(turn.parts), [turn.parts])
  const usage = working ? undefined : usageLine(turn)
  // A call under way says so on its own row, so the line below is for between them.
  const running = turn.parts.some((part) => part.kind === "step" && part.state === "running")
  return (
    <div className="space-y-3" data-turn={turn.phase}>
      {turn.fresh && (
        <p className="border-y border-hairline py-1.5 text-center text-hint text-muted-foreground">
          {turn.fresh}
        </p>
      )}
      <div className="ml-auto w-fit max-w-[85%] animate-rise rounded-xl bg-surface-header px-3.5 py-2.5">
        <p className="text-body break-words whitespace-pre-wrap">{turn.question}</p>
      </div>
      <div className="flex min-w-0 gap-3">
        <AssistantMark
          product={product}
          working={working}
          size="sm"
          className="mt-0.5 max-sm:hidden"
        />
        <div className="min-w-0 flex-1 space-y-3">
          {groups.map((group, at) => {
            switch (group.kind) {
              case "text":
                return (
                  <Markdown
                    key={at}
                    source={working && group.last ? group.text + " ▍" : group.text}
                    code={(block) => (
                      <Well
                        className="max-h-96 p-2.5 text-hint whitespace-pre"
                        aria-label={block.lang}
                      >
                        {block.v}
                      </Well>
                    )}
                  />
                )
              case "steps":
                return (
                  <ol
                    key={at}
                    data-slot="agent-steps"
                    className="space-y-0.5 rounded-xl border border-hairline bg-card p-1"
                  >
                    {group.steps.map((step) => (
                      <StepRow key={step.call.id} step={step} />
                    ))}
                  </ol>
                )
              case "waiting":
                return (
                  <ApprovalCard
                    key={at}
                    steps={group.steps}
                    active={current && turn.phase === "asking"}
                    paused={turn.paused}
                    onDecide={onDecide}
                  />
                )
              case "link":
                return <LinkCard key={at} link={group.part.link} />
            }
          })}
          {working && turn.phase !== "writing" && !running && <Thinking key={turn.parts.length} />}
          {turn.phase === "stopped" && (
            <p className="text-hint text-muted-foreground">
              Stopped. What it had already done stays done.
            </p>
          )}
          {turn.phase === "failed" && (
            <Notice tone="danger" title="No answer">
              {turn.error ?? "The assistant did not answer."}
            </Notice>
          )}
          {turn.phase === "asking" && turn.error && (
            <Notice tone="danger" title="That answer did not go through">
              {turn.error} Nothing above has run, so you can answer again.
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
          {usage && <p className="numeric text-micro text-muted-foreground">{usage}</p>}
        </div>
      </div>
    </div>
  )
}

/** AI Elements' `reasoning`, for a model that does not stream its thinking: how long. */
function Thinking() {
  const [seconds, setSeconds] = useState(0)
  useEffect(() => {
    const started = Date.now()
    const timer = window.setInterval(
      () => setSeconds(Math.floor((Date.now() - started) / 1000)),
      1000,
    )
    return () => window.clearInterval(timer)
  }, [])
  return (
    <p role="status" className="flex items-center gap-2 text-body">
      <Sparkles aria-hidden className="size-3.5 text-signal" />
      <TextShimmer>Thinking…</TextShimmer>
      {seconds >= 3 && <span className="numeric text-hint text-muted-foreground">{seconds}s</span>}
    </p>
  )
}
