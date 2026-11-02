"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { Archive, Gauge, Globe, Key, Logs, Sparkles, type Icon } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Notice } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import { useProject } from "@/components/deploy/project-context"
import { AssistantMark } from "@/components/assistant/mark"
import { AssistantOff } from "@/components/assistant/off"
import { AssistantSettingsSheet } from "@/components/assistant/settings-sheet"
import { assistantProduct, useAssistantSettings } from "@/components/assistant/settings"
import { AgentChat, type AgentSuggestion } from "@/components/assistant/agent/chat"
import { Shortcut } from "@/components/assistant/agent/step"
import { stepMark } from "@/components/assistant/agent/marks"
import {
  DIAGNOSE_QUESTION,
  agentStatus,
  changesOf,
  shownSteps,
  totalUsage,
} from "@/components/assistant/agent/model"
import {
  agentBusy,
  askAgent,
  useAgentSession,
  type AgentScope,
  type AgentSession,
} from "@/components/assistant/agent/sessions"
import { ProductGlyph } from "@/components/product-logo"
import { TextShimmer } from "@/components/ui/text-shimmer"

const SUGGESTIONS: AgentSuggestion[] = [
  { label: "Diagnose it", question: DIAGNOSE_QUESTION, icon: Sparkles },
  {
    label: "Check the logs for errors",
    question: "Read the logs of the live containers and tell me about any errors.",
    product: "docker",
  },
  {
    label: "Which requests are failing?",
    question: "Which requests are failing or slow right now, and why?",
    icon: Globe,
  },
  {
    label: "Is it sized right?",
    question: "How are its CPU and memory against its limits? Are the limits right?",
    icon: Gauge,
  },
  {
    label: "Add a domain",
    question: "I want to add a domain to this deployment. What do you need from me?",
    product: "caddy",
  },
  {
    label: "Set a variable",
    question: "I want to set an environment variable. Which ones does it have now?",
    icon: Key,
  },
  {
    label: "Back up its data",
    question:
      "What backs up this deployment's data, and when did it last run? Run a backup now if one is set up.",
    icon: Archive,
  },
  {
    label: "What happened lately?",
    question: "Summarise its last few runs and container events.",
    icon: Logs,
  },
]

/**
 * A deployment's assistant, as a page of its own: the conversation, and beside it
 * what it has done so far with a shortcut to each change. It goes on while you
 * follow one, and the dock brings you back. On a phone the two are one pane at a
 * time with a bar to switch (the Git workspace's pattern), never stacked.
 */
export function ProjectAssistant() {
  const project = useProject()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const settings = useAssistantSettings(true)
  const [settingUp, setSettingUp] = useState(false)
  const [pane, setPane] = useState<"chat" | "activity">("chat")
  const view = settings.data
  const { deployment, project: record } = project.detail
  const scope = useMemo<AgentScope>(
    () => ({
      kind: "deployment",
      projectId: project.projectId,
      environmentId: project.environmentId,
      environment: project.environmentQuery?.environment,
      name: record.name,
      environmentName: deployment.environmentName,
    }),
    [
      project.projectId,
      project.environmentId,
      project.environmentQuery?.environment,
      record.name,
      deployment.environmentName,
    ],
  )
  const session = useAgentSession(scope)

  // What it changed, the project's own reading of itself follows.
  const refresh = project.refresh
  const changed = useRef(session.changes)
  useEffect(() => {
    if (session.changes !== changed.current) {
      changed.current = session.changes
      refresh()
    }
  }, [session.changes, refresh])

  // The overview's Diagnose arrives with what to ask, once.
  const searchParams = useSearchParams()
  const router = useRouter()
  const asked = useRef(false)
  const wantsDiagnosis = searchParams.get("ask") === "diagnose"
  useEffect(() => {
    if (!wantsDiagnosis || asked.current || !view?.configured) return
    asked.current = true
    if (session.transcript.turns.length === 0) askAgent(session, DIAGNOSE_QUESTION)
    router.replace(`/deploy/${project.projectId}/assistant`, { scroll: false })
  }, [wantsDiagnosis, view?.configured, session, router, project.projectId])

  if (!can("service.control")) {
    return (
      <Notice tone="warning" title="Your role does not ask the assistant">
        Asking it takes the service control capability, because what it does is control services.
      </Notice>
    )
  }
  if (!project.normalized || project.archived) {
    return (
      <Notice title="The assistant works on a live project">
        {project.archived
          ? "This project is archived. Restore it and the assistant can read and change it."
          : "This project was made before environments, which are what the assistant acts through, so it cannot work on it."}
      </Notice>
    )
  }
  return (
    <div
      data-slot="project-assistant"
      className="flex h-[clamp(30rem,calc(100dvh-14rem),60rem)] min-w-0 flex-col overflow-hidden rounded-xl border border-hairline bg-background lg:flex-row"
    >
      {!view ? (
        settings.error ? (
          <Notice tone="danger" title="The assistant's settings could not be read" className="m-4">
            {errorMessage(settings.error)}
          </Notice>
        ) : (
          <div aria-hidden className="m-4 space-y-3">
            <Skeleton className="h-3 w-40" />
            <Skeleton className="h-3 w-56" />
          </div>
        )
      ) : !view.configured ? (
        <AssistantOff
          className="m-auto"
          settings={view}
          onSetUp={() => setSettingUp(true)}
          what="it can read this deployment, answer anything about it, and change its settings for you."
        />
      ) : (
        <>
          <div
            className={cn(
              "min-h-0 min-w-0 flex-1 flex-col",
              pane === "chat" ? "flex" : "hidden lg:flex",
            )}
          >
            <AgentChat
              session={session}
              settings={view}
              title={`Ask anything about ${record.name}`}
              description="It can read its logs, requests and metrics, change its settings, domains and variables, take backups and deploy it, as you and under your role."
              placeholder={`Ask anything about ${record.name}…`}
              suggestions={SUGGESTIONS}
              sends={`what the overview shows about ${deployment.environmentName} and what it reads to answer`}
            />
          </div>
          <div
            className={cn(
              "min-h-0 w-full shrink-0 flex-col overflow-y-auto border-hairline lg:w-72 lg:border-l",
              pane === "activity" ? "flex flex-1 lg:flex-none" : "hidden lg:flex",
            )}
          >
            <ActivityRail session={session} product={assistantProduct(view)} />
          </div>
          <PaneBar pane={pane} onPane={setPane} session={session} />
        </>
      )}
      {can("system.admin") && (
        <AssistantSettingsSheet
          open={settingUp}
          settings={view}
          onClose={() => setSettingUp(false)}
          onSaved={settings.refresh}
          confirm={confirm}
        />
      )}
      {dialog}
    </div>
  )
}

function PaneBar({
  pane,
  onPane,
  session,
}: {
  pane: "chat" | "activity"
  onPane: (pane: "chat" | "activity") => void
  session: AgentSession
}) {
  const changes = changesOf(session.transcript).length
  const stop = (key: "chat" | "activity", label: string, icon: Icon, count?: number) => {
    const Mark = icon
    return (
      <button
        type="button"
        onClick={() => onPane(key)}
        aria-current={pane === key ? "page" : undefined}
        className={cn(
          "flex h-14 flex-1 flex-col items-center justify-center gap-1 text-micro focus-ring",
          pane === key ? "text-foreground" : "text-muted-foreground",
        )}
      >
        <Mark aria-hidden className="size-5" />
        {label}
        {count ? ` (${count})` : ""}
      </button>
    )
  }
  return (
    <nav
      aria-label="Assistant panes"
      className="flex shrink-0 border-t border-hairline bg-surface-header pb-[env(safe-area-inset-bottom)] lg:hidden"
    >
      {stop("chat", "Chat", Sparkles)}
      {stop("activity", "What it did", Logs, changes)}
    </nav>
  )
}

/** What it is doing, what it has changed with a press to each, and what it has read. */
function ActivityRail({ session, product }: { session: AgentSession; product: string }) {
  const { transcript } = session
  const busy = agentBusy(session)
  const status = agentStatus(transcript)
  const changes = changesOf(transcript)
  const reads = useMemo(() => {
    const seen = new Map<string, ReturnType<typeof shownSteps>[number]>()
    for (const turn of transcript.turns)
      for (const step of shownSteps(turn))
        if (step.call.kind === "read" && step.state !== "waiting") seen.set(step.call.label, step)
    return [...seen.values()].reverse()
  }, [transcript])
  const usage = totalUsage(transcript)
  return (
    <div data-slot="agent-activity" className="flex min-h-0 flex-1 flex-col gap-5 p-4">
      <div className="flex min-w-0 items-center gap-3">
        <AssistantMark product={product} working={busy} size="sm" />
        <div className="min-w-0">
          <p className="truncate text-body font-medium">
            {transcript.modelLabel ?? "The assistant"}
          </p>
          <p
            role="status"
            className={cn(
              "truncate text-hint",
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
      <section className="space-y-2">
        <h2 className="eyebrow">Changes</h2>
        {changes.length === 0 ? (
          <p className="text-hint text-muted-foreground">Nothing yet.</p>
        ) : (
          <ul className="space-y-2">
            {changes.map((step) => (
              <li
                key={step.call.id}
                className="animate-rise space-y-1.5 rounded-lg border border-rule-success bg-wash-success p-2.5"
              >
                <p className="text-body">{step.call.label}</p>
                <Shortcut label={step.change!.label} href={step.change!.href} />
              </li>
            ))}
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
      {usage.input + usage.output > 0 && (
        <p className="numeric mt-auto text-micro text-muted-foreground">
          {usage.input.toLocaleString("en-US")} tokens in · {usage.output.toLocaleString("en-US")}{" "}
          out
        </p>
      )}
    </div>
  )
}
