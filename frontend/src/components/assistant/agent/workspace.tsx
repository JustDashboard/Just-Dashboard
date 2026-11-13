"use client"

import { useMemo, useState } from "react"
import { AnimatePresence, MotionConfig, motion } from "motion/react"
import { ChatBubble, Logs, Sparkles } from "@/components/icons"
import { cn } from "@/lib/utils"
import { useArrivals } from "@/hooks/use-arrivals"
import { PaneBar } from "@/components/phone-panes"
import { ProductGlyph } from "@/components/product-logo"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { AssistantMark } from "@/components/assistant/mark"
import { Shortcut } from "@/components/assistant/agent/step"
import { stepMark } from "@/components/assistant/agent/marks"
import {
  agentStatus,
  changesOf,
  inputTokens,
  shownSteps,
  totalUsage,
  type AgentStep,
} from "@/components/assistant/agent/model"
import { agentWorking, type AgentSession } from "@/components/assistant/agent/sessions"

export type AgentPane = "chat" | "chats" | "activity"

/**
 * A chat as a page: the conversation, and beside it a rail of the person's chats
 * and what this one has done, with a shortcut to each change. Nothing is boxed: the
 * conversation is the page's own ground down to the window's foot, and the rail is a
 * column after a hairline. On a phone they are one full-height pane at a time with a
 * bar to switch (`phone-panes.tsx`, the Git workspace's pattern), never stacked.
 */
export function AgentWorkspace({
  session,
  product,
  chat,
  chats,
  className,
}: {
  session: AgentSession
  product: string
  chat: React.ReactNode
  chats: React.ReactNode
  className?: string
}) {
  const [pane, setPane] = useState<AgentPane>("chat")
  const changes = changesOf(session.transcript).length
  return (
    <MotionConfig reducedMotion="user">
      <div
        data-slot="agent-workspace"
        className={cn("flex min-h-0 min-w-0 flex-1 flex-col lg:flex-row", className)}
      >
        <section
          aria-label="Chat"
          className={cn(
            "min-h-0 min-w-0 flex-1 flex-col",
            pane === "chat" ? "flex" : "hidden lg:flex",
          )}
        >
          {chat}
        </section>
        <aside
          data-slot="agent-rail"
          aria-label="Chats and what it did"
          className={cn(
            "min-h-0 w-full shrink-0 flex-col gap-6 overflow-y-auto overscroll-contain px-4 pt-5 pb-4 lg:flex lg:w-72 lg:border-l lg:border-hairline lg:pr-0",
            pane === "chat" ? "hidden" : "flex flex-1 lg:flex-none",
          )}
        >
          <RailHead session={session} product={product} />
          <div data-slot="agent-chats" className={cn(pane === "activity" && "max-lg:hidden")}>
            {chats}
          </div>
          <Activity
            session={session}
            className={cn("flex-1", pane === "chats" && "max-lg:hidden")}
          />
        </aside>
        <PaneBar
          label="Assistant panes"
          active={pane}
          onPick={setPane}
          stops={[
            { key: "chat", label: "Chat", icon: <Sparkles /> },
            { key: "chats", label: "Chats", icon: <ChatBubble /> },
            {
              key: "activity",
              label: "What it did",
              icon: <Logs />,
              count: changes,
              countClassName: "bg-success",
            },
          ]}
        />
      </div>
    </MotionConfig>
  )
}

/** Who answers, and what it is doing now, in a few words. */
function RailHead({ session, product }: { session: AgentSession; product: string }) {
  const { transcript } = session
  const working = agentWorking(session)
  const status = session.elsewhere
    ? { tone: "working" as const, text: "Answering in another tab" }
    : agentStatus(transcript)
  return (
    <div data-slot="agent-status" className="flex min-w-0 items-center gap-3">
      <AssistantMark product={product} working={working} size="sm" />
      <div className="min-w-0">
        <p className="truncate text-body font-medium">{transcript.modelLabel ?? "The assistant"}</p>
        <p
          role="status"
          className={cn(
            "truncate text-hint transition-colors duration-300",
            status.tone === "asking"
              ? "text-warning"
              : status.tone === "failed"
                ? "text-destructive"
                : status.tone === "done"
                  ? "text-success"
                  : "text-muted-foreground",
          )}
        >
          {status.tone === "working" ? (
            <TextShimmer className="max-w-full truncate align-bottom">{status.text}</TextShimmer>
          ) : (
            status.text
          )}
        </p>
      </div>
    </div>
  )
}

/** What this chat has changed, with a press to each, and what it has read. */
function Activity({ session, className }: { session: AgentSession; className?: string }) {
  const { transcript } = session
  const changes = changesOf(transcript)
  const reads = useMemo(() => {
    const seen = new Map<string, AgentStep>()
    for (const turn of transcript.turns)
      for (const step of shownSteps(turn))
        if (step.call.kind === "read" && step.state !== "waiting") seen.set(step.call.label, step)
    return [...seen.values()].reverse()
  }, [transcript])
  const arrived = useArrivals(changes.map((step) => step.call.id))
  const usage = totalUsage(transcript)
  return (
    <div data-slot="agent-activity" className={cn("flex min-h-0 flex-col gap-6", className)}>
      <section className="space-y-2">
        <h2 className="eyebrow">Changes</h2>
        {changes.length === 0 ? (
          <p className="text-hint text-muted-foreground">Nothing yet.</p>
        ) : (
          <ul className="space-y-2">
            <AnimatePresence initial={false}>
              {changes.map((step) => (
                <motion.li
                  key={step.call.id}
                  layout="position"
                  initial={{ opacity: 0, x: 8 }}
                  animate={{ opacity: 1, x: 0 }}
                  transition={{ duration: 0.22, ease: [0.16, 1, 0.3, 1] }}
                  data-slot="agent-rail-change"
                  className="space-y-1.5 rounded-lg border border-rule-success bg-wash-success p-2.5"
                >
                  <p className="text-body">{step.call.label}</p>
                  <Shortcut
                    label={step.change!.label}
                    href={step.change!.href}
                    lit={arrived.has(step.call.id)}
                  />
                </motion.li>
              ))}
            </AnimatePresence>
          </ul>
        )}
      </section>
      {reads.length > 0 && (
        <section className="space-y-2">
          <h2 className="eyebrow">What it read</h2>
          <ul className="space-y-1.5">
            {reads.map((step) => {
              const mark = stepMark(step.call)
              const Glyph = mark.icon
              return (
                <li key={step.call.id} className="flex min-w-0 items-center gap-2 text-hint">
                  {mark.product ? (
                    <ProductGlyph id={mark.product} className="size-3.5" />
                  ) : (
                    <Glyph aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
                  )}
                  <span className="min-w-0 truncate text-muted-foreground">{step.call.label}</span>
                </li>
              )
            })}
          </ul>
        </section>
      )}
      {inputTokens(usage) + usage.output > 0 && (
        <p className="numeric mt-auto text-micro text-muted-foreground">
          {inputTokens(usage).toLocaleString("en-US")} tokens in ·{" "}
          {usage.output.toLocaleString("en-US")} out
        </p>
      )}
    </div>
  )
}
