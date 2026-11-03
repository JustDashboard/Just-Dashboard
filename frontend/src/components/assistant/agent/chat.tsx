"use client"

import { useEffect, useMemo, useState } from "react"
import { AnimatePresence, MotionConfig, motion, useReducedMotion } from "motion/react"
import { ChevronRight, Plus, Sparkles } from "@/components/icons"
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
  agentChatId,
  agentModelChanged,
  agentWorking,
  askAgent,
  decideAgent,
  newAgentChat,
  openAgentChat,
  setAgentMode,
  stopAgent,
  takeOverAgent,
  type AgentSession,
} from "@/components/assistant/agent/sessions"
import { FormNote } from "@/components/form"
import type { PastedImage } from "@/components/assistant/agent/images"
import { Markdown } from "@/components/git/markdown"
import { Well } from "@/components/panel"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { cn } from "@/lib/utils"
import type { Icon } from "@/components/icons"

export interface AgentSuggestion {
  label: string
  question: string
  product?: string
  icon?: Icon
}

const EASE = [0.16, 1, 0.3, 1] as const

/**
 * The conversation: what was asked, what it did as it did it, what it is waiting on,
 * and the box to ask in. The same for a deployment's page, the server's and a new
 * deployment's setup; only what it is about differs, and every place that adds the
 * assistant later draws this.
 *
 * Only the column of messages scrolls; the box sits under it. Everything that moves
 * here moves for a reason a person can name — an answer arriving, a step ending, a
 * call waiting on them — and fast, and not at all for a reader who asked for less
 * motion (`MotionConfig` hands that to every motion below it; CSS has its own rule).
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
  onNew,
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
  /** A new chat, where the page has more to do for one than the store. */
  onNew?: () => void
}) {
  const { transcript } = session
  const busy = agentBusy(session)
  const product = assistantProduct(settings)
  const mode: AssistantMode = session.mode ?? settings?.defaultMode ?? "changes"
  const last = transcript.turns.at(-1)
  const waiting = pendingSteps(transcript)
  const ask = (question: string, images: PastedImage[] = []) => askAgent(session, question, images)
  const column = cn(!compact && "mx-auto w-full max-w-3xl")
  return (
    <MotionConfig reducedMotion="user">
      <div data-slot="agent-chat" className="flex min-h-0 flex-1 flex-col">
        {session.loading ? (
          <ChatLoading className={column} />
        ) : session.loadError ? (
          <div className={cn("flex-1 p-4", column)}>
            <Notice tone="danger" title="This chat could not be read">
              <p>{session.loadError}</p>
              <Button
                size="xs"
                variant="outline"
                className="mt-2"
                onClick={() => void openAgentChat(session.scope, agentChatId(session))}
              >
                Try again
              </Button>
            </Notice>
          </div>
        ) : (
          <Conversation
            label="Conversation with the assistant"
            busy={agentWorking(session)}
            contentClassName={cn(compact ? "gap-5 p-3" : "px-4 pt-6 pb-6", column)}
          >
            {transcript.turns.length === 0 && (
              <Empty
                product={product}
                title={title}
                description={description}
                suggestions={suggestions}
                compact={compact}
                onAsk={ask}
              />
            )}
            {transcript.turns.map((turn) => (
              <TurnView
                key={turn.id}
                turn={turn}
                product={product}
                current={turn === last}
                elsewhere={session.elsewhere && turn === last}
                onDecide={(id, allow) => decideAgent(session, id, allow)}
              />
            ))}
          </Conversation>
        )}
        <div className={cn("shrink-0", column)}>
          <AnimatePresence initial={false}>
            {transcript.tools === false && (
              <Reveal key="tools">
                <div className="px-3 pb-2">
                  <FormNote tone="warning">
                    This model is set up without tools, so it can only answer from what the page
                    shows. Turn tools on in its settings, or switch to a provider that has them.
                  </FormNote>
                </div>
              </Reveal>
            )}
            {!session.flight && (session.elsewhere || session.held) && (
              <Reveal key="elsewhere">
                <ChatBar
                  slot="agent-elsewhere"
                  live
                  text="This chat is running in another tab."
                  action="Take over"
                  onAction={() => takeOverAgent(session)}
                />
              </Reveal>
            )}
            {!session.flight && !session.elsewhere && session.moved && (
              <Reveal key="moved">
                <ChatBar
                  slot="agent-moved"
                  text="Continued somewhere else."
                  action="Open it here"
                  onAction={() => void openAgentChat(session.scope, agentChatId(session))}
                />
              </Reveal>
            )}
          </AnimatePresence>
          <PromptInput
            busy={busy}
            // A chat still being read back has no conversation to carry on yet.
            disabled={session.loading}
            label="Ask the assistant"
            placeholder={placeholder}
            acceptImages
            imagesNote={
              <FormNote>
                Images are sent as they are: masking reads text, so a secret in a screenshot reaches
                the model.
              </FormNote>
            }
            rows={compact ? 1 : 2}
            note={
              waiting.length > 0 &&
              !session.held && (
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
                    aria-label="New chat"
                    onClick={onNew ?? (() => newAgentChat(session.scope))}
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
            className="pb-2"
          />
          <p className="px-4 pb-2 text-micro text-pretty text-muted-foreground">
            {`Sends ${assistantDestination(settings)} your question, ${sends}, with secret values masked.`}
          </p>
        </div>
      </div>
    </MotionConfig>
  )
}

/** Height and opacity together, for a line that joins the foot of the column. */
function Reveal({ children }: { children: React.ReactNode }) {
  // `MotionConfig` stills transforms, not heights; a reader who asked for less motion
  // gets the line at once.
  const reduced = useReducedMotion()
  return (
    <motion.div
      initial={{ opacity: 0, height: 0 }}
      animate={{ opacity: 1, height: "auto" }}
      exit={{ opacity: 0, height: 0 }}
      transition={{ duration: reduced ? 0 : 0.2, ease: EASE }}
      className="overflow-hidden"
    >
      {children}
    </motion.div>
  )
}

/**
 * A calm line over the box about the chat itself — answering in another tab, or
 * carried on in one — with the one thing to do about it.
 */
function ChatBar({
  slot,
  text,
  action,
  live,
  onAction,
}: {
  slot: string
  text: string
  action: string
  live?: boolean
  onAction: () => void
}) {
  return (
    <div className="px-3 pb-2">
      <div
        data-slot={slot}
        role="status"
        className="flex min-w-0 items-center gap-2.5 rounded-lg border border-hairline bg-surface-header py-1.5 pr-1.5 pl-3"
      >
        {live ? (
          <span aria-hidden className="relative flex size-2 shrink-0">
            <span className="absolute inset-0 animate-breathe rounded-full bg-signal" />
            <span className="relative size-2 rounded-full bg-signal" />
          </span>
        ) : (
          <span aria-hidden className="size-2 shrink-0 rounded-full bg-muted-foreground" />
        )}
        <p className="min-w-0 flex-1 truncate text-body">{text}</p>
        <Button size="xs" variant="outline" onClick={onAction}>
          {action}
        </Button>
      </div>
    </div>
  )
}

/** The empty chat: who answers, what it can do, and questions one press from asked. */
function Empty({
  product,
  title,
  description,
  suggestions,
  compact,
  onAsk,
}: {
  product: string
  title: string
  description: string
  suggestions: AgentSuggestion[]
  compact?: boolean
  onAsk: (question: string) => void
}) {
  return (
    <motion.div
      data-slot="agent-empty"
      initial={{ opacity: 0, y: 6 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.22, ease: EASE }}
      className={cn(
        "my-auto flex flex-col items-center gap-3 text-center",
        compact ? "py-4" : "py-6",
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
        <div className="flex max-w-2xl flex-wrap justify-center gap-2 pt-2">
          {suggestions.map((suggestion, index) => (
            <Suggestion key={suggestion.label} {...suggestion} index={index} onAsk={onAsk} />
          ))}
        </div>
      )}
    </motion.div>
  )
}

/** A chat being read back: the shape of a question and an answer, where they will be. */
function ChatLoading({ className }: { className?: string }) {
  return (
    <div
      aria-busy
      aria-label="Reading the chat"
      data-slot="agent-chat-loading"
      className={cn("min-h-0 flex-1 space-y-6 overflow-hidden px-4 pt-6", className)}
    >
      {[0, 1].map((at) => (
        <div key={at} className="space-y-3">
          <Skeleton className="ml-auto h-9 w-2/5 rounded-xl" />
          <div className="flex gap-3">
            <Skeleton className="size-7 shrink-0 rounded-lg" />
            <div className="flex-1 space-y-2 pt-1">
              <Skeleton className="h-3 w-4/5" />
              <Skeleton className="h-3 w-3/5" />
              <Skeleton className="h-3 w-2/3" />
            </div>
          </div>
        </div>
      ))}
    </div>
  )
}

/** The parts of an answer, with the steps next to each other drawn as one list. */
type Group =
  | { kind: "text"; text: string; last: boolean }
  | { kind: "steps"; steps: AgentStep[] }
  | { kind: "waiting"; steps: AgentStep[] }
  | { kind: "link"; part: Extract<AgentPart, { kind: "link" }> }

/** Looking a route up is said in a word; describing one is not worth a row at all. */
const drawn = (step: AgentStep) =>
  step.call.kind !== "free" || step.call.tool === "find_routes" || step.state === "failed"

function groupsOf(parts: AgentPart[]): Group[] {
  const out: Group[] = []
  for (const [at, part] of parts.entries()) {
    if (part.kind === "text") {
      out.push({ kind: "text", text: part.text, last: at === parts.length - 1 })
    } else if (part.kind === "link") {
      out.push({ kind: "link", part })
    } else if (drawn(part)) {
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
  elsewhere,
  onDecide,
}: {
  turn: AgentTurn
  product?: string
  current: boolean
  /** Being answered in another tab, and followed here. */
  elsewhere: boolean
  onDecide: (id: string | "all", allow: boolean) => void
}) {
  const working = answering(turn)
  const groups = useMemo(() => groupsOf(turn.parts), [turn.parts])
  const usage = working ? undefined : usageLine(turn)
  // A call under way says so on its own row, so the line below is for between them.
  const running = turn.parts.some((part) => part.kind === "step" && part.state === "running")
  return (
    <motion.div
      className="space-y-3"
      data-turn={turn.phase}
      initial={turn.replayed ? false : { opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.22, ease: EASE }}
    >
      {turn.fresh && (
        <p className="border-y border-hairline py-1.5 text-center text-hint text-muted-foreground">
          {turn.fresh}
        </p>
      )}
      <div
        data-slot="agent-question"
        className="ml-auto w-fit max-w-[85%] space-y-2 rounded-xl bg-surface-header px-3.5 py-2.5"
      >
        {turn.images && turn.images.length > 0 && (
          <ul data-slot="agent-question-images" className="flex flex-wrap justify-end gap-2">
            {turn.images.map((src, at) => (
              <li key={at}>
                {/* A thumbnail as a data address: nothing for Next to optimise. */}
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img
                  src={src}
                  alt={`Image ${at + 1} of the question`}
                  className="max-h-40 max-w-56 rounded-md border border-hairline object-contain"
                />
              </li>
            ))}
          </ul>
        )}
        {turn.question && (
          <p className="text-body break-words whitespace-pre-wrap">{turn.question}</p>
        )}
      </div>
      <div className="flex min-w-0 gap-3">
        <AssistantMark
          product={product}
          working={working}
          size="sm"
          className="mt-0.5 self-start max-sm:hidden"
        />
        <div className="min-w-0 flex-1 space-y-3">
          {groups.map((group, at) => {
            switch (group.kind) {
              case "text":
                return (
                  <Markdown
                    key={at}
                    className={cn(working && group.last && "agent-streaming")}
                    source={group.text}
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
                  <StepGroup
                    key={at}
                    steps={group.steps}
                    working={working}
                    replayed={turn.replayed}
                  />
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
          <AnimatePresence initial={false}>
            {working && turn.phase !== "writing" && !running && (
              <Thinking
                key="thinking"
                label={elsewhere ? "Answering in another tab…" : undefined}
              />
            )}
          </AnimatePresence>
          {turn.phase === "stopped" && (
            <p className="text-hint text-muted-foreground">
              Stopped. What it had already done stays done.
            </p>
          )}
          {turn.phase === "held" && (
            <p className="text-hint text-muted-foreground">
              Not sent: this chat is answering in another tab.
            </p>
          )}
          {turn.phase === "moved" && (
            <p className="text-hint text-muted-foreground">
              Continued somewhere else. What it had already done stays done.
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
          {usage && (
            <motion.p
              initial={turn.replayed ? false : { opacity: 0 }}
              animate={{ opacity: 1 }}
              transition={{ duration: 0.3 }}
              className="numeric text-micro text-muted-foreground"
            >
              {usage}
            </motion.p>
          )}
        </div>
      </div>
    </motion.div>
  )
}

/** A run of steps longer than this folds to one line once the answer is over. */
const FOLD_AT = 4

/**
 * Steps next to each other, as one list. A long run read back from a finished
 * answer folds to a line that says how many, opened to its height on a press; a
 * run arriving stays open, so nothing it is doing is hidden while it does it.
 */
function StepGroup({
  steps,
  working,
  replayed,
}: {
  steps: AgentStep[]
  working: boolean
  replayed?: boolean
}) {
  const foldable = steps.length > FOLD_AT
  const [open, setOpen] = useState(working || !foldable)
  const list = (
    <ol data-slot="agent-steps" className="space-y-0.5 p-1">
      {steps.map((step) => (
        <StepRow key={step.call.id} step={step} replayed={replayed} />
      ))}
    </ol>
  )
  if (!foldable) {
    return <div className="rounded-xl border border-hairline bg-card">{list}</div>
  }
  const changed = steps.filter((step) => step.change).length
  return (
    <Collapsible
      open={open}
      onOpenChange={setOpen}
      className="rounded-xl border border-hairline bg-card"
    >
      <CollapsibleTrigger
        data-slot="agent-steps-fold"
        className="group flex h-9 w-full min-w-0 items-center gap-2 rounded-xl px-3 text-left text-hint text-muted-foreground focus-ring transition-colors hover:bg-row-hover"
      >
        <ChevronRight
          aria-hidden
          className="size-3.5 shrink-0 transition-transform duration-200 group-data-[state=open]:rotate-90"
        />
        <span className="numeric">
          {steps.length} steps{changed > 0 ? ` · ${changed} changed` : ""}
        </span>
      </CollapsibleTrigger>
      <CollapsibleContent className="overflow-hidden data-[state=closed]:animate-collapsible-up data-[state=open]:animate-collapsible-down">
        <div className="border-t border-hairline">{list}</div>
      </CollapsibleContent>
    </Collapsible>
  )
}

/**
 * AI Elements' `reasoning`, for a model that does not stream its thinking: the word
 * lit while it thinks, and how long it has been at it, counted from the first second.
 */
function Thinking({ label = "Thinking…" }: { label?: string }) {
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
    <motion.p
      role="status"
      data-slot="agent-thinking"
      className="flex items-center gap-2 text-body"
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      transition={{ duration: 0.18 }}
    >
      <Sparkles aria-hidden className="size-3.5 text-signal" />
      <TextShimmer>{label}</TextShimmer>
      {seconds >= 1 && (
        <span className="numeric text-hint text-muted-foreground tabular-nums">{seconds}s</span>
      )}
    </motion.p>
  )
}
