"use client"

import { useEffect, useMemo, useRef } from "react"
import { useRouter } from "next/navigation"
import { ArrowRight, Cross, Sparkles } from "@/components/icons"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { AssistantMark } from "@/components/assistant/mark"
import { AssistantOff } from "@/components/assistant/off"
import { AgentChat } from "@/components/assistant/agent/chat"
import { SETUP_QUESTION, agentStatus } from "@/components/assistant/agent/model"
import {
  agentBusy,
  askAgent,
  useAgentSession,
  type AgentScope,
} from "@/components/assistant/agent/sessions"
import { assistantProduct, useAssistantSettings } from "@/components/assistant/settings"
import {
  loadDraft,
  resumeFlow,
  type ConfigureFlow,
  type FlowUpdate,
} from "@/components/deploy/new-project/draft"

/** The conversation about a draft, which the page holds Deploy back for while it works. */
export function draftScope(flow: ConfigureFlow): AgentScope {
  return { kind: "draft", draftId: flow.draft.id, name: flow.name || flow.sourceLabel }
}

/**
 * Handing a new deployment's settings to the assistant: "I do not want to bother
 * with the config." It works on the draft, so nothing it does runs anything, and each
 * thing it saves redraws the plan and the fields beside it. It cannot press Deploy;
 * that stays the person's, and is where this ends.
 *
 * A column beside the plan on a wide window, and the whole screen on a phone, whose
 * way back to the form is the cross (one pane at a time, never stacked).
 */
export function SetupAssistant({
  flow,
  onFlowChange,
  onClose,
  onPrepare,
  onReview,
}: {
  flow: ConfigureFlow
  onFlowChange: (next: FlowUpdate) => void
  onClose: () => void
  /** Saves what the page holds, so the assistant starts from what the person sees. */
  onPrepare: () => Promise<void>
  onReview: () => void
}) {
  const router = useRouter()
  const settings = useAssistantSettings(true)
  const view = settings.data
  const draftId = flow.draft.id
  const scope = useMemo(() => draftScope(flow), [flow])
  const session = useAgentSession(scope)
  const busy = agentBusy(session)
  const status = agentStatus(session.transcript)
  const product = assistantProduct(view)

  // It begins on being handed the job, from the plan as it stands.
  const begun = useRef(false)
  useEffect(() => {
    if (begun.current || !view?.configured || session.transcript.turns.length > 0) return
    begun.current = true
    void onPrepare().finally(() => askAgent(session, SETUP_QUESTION))
  }, [view?.configured, session, onPrepare])

  // What it saved, the page reads back, so the fields and the plan say the same.
  const seen = useRef(session.changes)
  useEffect(() => {
    if (session.changes === seen.current) return
    seen.current = session.changes
    void loadDraft(draftId)
      .then(resumeFlow)
      .then((next) =>
        onFlowChange((current) =>
          current?.draft.id === draftId
            ? { ...next, sourceLabel: current.sourceLabel, githubRepo: current.githubRepo }
            : current,
        ),
      )
      .catch(() => undefined)
  }, [session.changes, draftId, onFlowChange])

  return (
    <aside
      aria-label="The assistant setting this up"
      data-slot="setup-assistant"
      className="z-40 flex min-h-0 min-w-0 flex-col overflow-hidden bg-background max-xl:fixed max-xl:inset-0 xl:col-start-1 xl:row-start-1 xl:rounded-xl xl:border xl:border-hairline"
    >
      <div className="flex shrink-0 items-center gap-2.5 border-b border-hairline bg-surface-header p-2.5">
        <AssistantMark product={product} working={busy} size="sm" />
        <div className="min-w-0 flex-1">
          <p className="truncate text-body font-medium">Setting up {scope.name}</p>
          <p className="truncate text-hint text-muted-foreground">
            {status.tone === "working" ? (
              <TextShimmer className="max-w-full truncate align-bottom">{status.text}</TextShimmer>
            ) : (
              status.text
            )}
          </p>
        </div>
        <IconAction label="Back to the form" onClick={onClose}>
          <Cross />
        </IconAction>
      </div>
      {view && !view.configured ? (
        <AssistantOff
          className="m-auto"
          settings={view}
          onSetUp={() => router.push("/assistant")}
          what="it can choose the settings for you."
        />
      ) : (
        <AgentChat
          compact
          session={session}
          settings={view}
          title={`Set up ${scope.name}`}
          description="It reads what the dashboard found in the source and chooses the settings. Nothing runs until you press Deploy."
          placeholder="Tell it what you want…"
          sends="what the dashboard read of the source"
        />
      )}
      {!busy && status.tone === "done" && (
        <div className="shrink-0 border-t border-hairline p-2.5">
          <Button className="h-11 w-full animate-rise sm:h-9" onClick={onReview}>
            Review and deploy
            <ArrowRight />
          </Button>
        </div>
      )}
    </aside>
  )
}

/** The way to hand it over: at the head of the plan, and on the screen that chooses a source. */
export function SetupAssistantOffer({ onClick }: { onClick: () => void }) {
  const settings = useAssistantSettings(true)
  const view = settings.data
  if (!view?.configured || !view.tools) return null
  return (
    <button
      type="button"
      data-slot="setup-assistant-offer"
      onClick={onClick}
      className="mb-4 flex w-full min-w-0 items-center gap-3 rounded-xl border border-rule-brand bg-wash-brand p-3 text-left focus-ring transition-colors hover:border-brand"
    >
      <AssistantMark product={assistantProduct(view)} size="sm" />
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5 text-body font-medium">
          <Sparkles aria-hidden className="size-3.5 text-signal" />
          Let the assistant set this up
        </span>
        <span className="block text-hint leading-relaxed text-muted-foreground">
          It chooses the settings. You just press Deploy.
        </span>
      </span>
    </button>
  )
}
