"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { Archive, Gauge, Globe, Key, Logs, Sparkles } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Notice } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import { useProject } from "@/components/deploy/project-context"
import { AssistantOff } from "@/components/assistant/off"
import { AssistantSettingsSheet } from "@/components/assistant/settings-sheet"
import { assistantProduct, useAssistantSettings } from "@/components/assistant/settings"
import { AgentChat, type AgentSuggestion } from "@/components/assistant/agent/chat"
import { AgentChatList } from "@/components/assistant/agent/chat-list"
import { useAgentChats } from "@/components/assistant/agent/chats"
import { AgentWorkspace } from "@/components/assistant/agent/workspace"
import { DIAGNOSE_QUESTION } from "@/components/assistant/agent/model"
import {
  agentBusy,
  agentChatId,
  agentScopeKey,
  askAgent,
  newAgentChat,
  openAgentChat,
  useAgentSession,
  type AgentScope,
} from "@/components/assistant/agent/sessions"

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
 * A deployment's assistant, as a page of its own: the conversation filling the
 * window under the project's header, and beside it this deployment's chats and what
 * the open one has done, with a shortcut to each change. It goes on while you
 * follow one, and the dock brings you back. `?chat=` opens a saved chat, read back
 * from the server and ready to carry on; `?ask=diagnose` begins one that asks.
 */
export function ProjectAssistant() {
  const project = useProject()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const settings = useAssistantSettings(true)
  const [settingUp, setSettingUp] = useState(false)
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
  const allowed = can("service.control") && project.normalized && !project.archived
  const chats = useAgentChats(
    { deployment: project.projectId },
    allowed && Boolean(view?.configured),
  )

  // What it changed, the project's own reading of itself follows.
  const refresh = project.refresh
  const changed = useRef(session.changes)
  useEffect(() => {
    if (session.changes !== changed.current) {
      changed.current = session.changes
      refresh()
    }
  }, [session.changes, refresh])

  const searchParams = useSearchParams()
  const router = useRouter()
  const base = `/deploy/${project.projectId}/assistant`

  // The overview's Diagnose arrives with what to ask, once, in a chat of its own.
  const asked = useRef(false)
  const wantsDiagnosis = searchParams.get("ask") === "diagnose"
  useEffect(() => {
    if (!wantsDiagnosis || asked.current || !view?.configured) return
    asked.current = true
    const front = agentBusy(session) ? session : newAgentChat(scope)
    if (front.transcript.turns.length === 0) askAgent(front, DIAGNOSE_QUESTION)
    router.replace(base, { scroll: false })
  }, [wantsDiagnosis, view?.configured, session, scope, router, base])

  // `?chat=` puts a saved chat in front. A chat from the other environment switches
  // the page to it first, and is opened there.
  const wanted = searchParams.get("chat")
  const subject = agentScopeKey(scope)
  const opening = useRef("")
  const { selectEnvironment, staging, production } = project
  useEffect(() => {
    if (!wanted) opening.current = ""
    if (!wanted || !view?.configured || !allowed) return
    const attempt = `${subject}:${wanted}`
    if (opening.current === attempt) return
    opening.current = attempt
    void openAgentChat(scope, wanted).then((other) => {
      if (!other?.environment) return
      if (other.environment === staging?.id) selectEnvironment("staging")
      else if (other.environment === production?.id) selectEnvironment("production")
    })
  }, [wanted, subject, scope, view?.configured, allowed, staging, production, selectEnvironment])

  if (!can("service.control")) {
    return (
      <Notice tone="warning" title="Your role does not ask the assistant" className="mt-6">
        Asking it takes the service control capability, because what it does is control services.
      </Notice>
    )
  }
  if (!project.normalized || project.archived) {
    return (
      <Notice title="The assistant works on a live project" className="mt-6">
        {project.archived
          ? "This project is archived. Restore it and the assistant can read and change it."
          : "This project was made before environments, which are what the assistant acts through, so it cannot work on it."}
      </Notice>
    )
  }
  const startNew = () => {
    newAgentChat(scope)
    if (wanted) router.replace(base, { scroll: false })
  }
  const environmentName = (id?: number) =>
    id === staging?.id ? "Staging" : id === production?.id ? "Production" : undefined
  return (
    <div data-slot="project-assistant" className="flex min-h-0 min-w-0 flex-1 flex-col">
      {!view ? (
        settings.error ? (
          <Notice tone="danger" title="The assistant's settings could not be read" className="mt-6">
            {errorMessage(settings.error)}
          </Notice>
        ) : (
          <div aria-hidden className="mt-6 space-y-3">
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
        <AgentWorkspace
          session={session}
          product={assistantProduct(view)}
          chat={
            <AgentChat
              key={session.key}
              session={session}
              settings={view}
              title={`Ask anything about ${record.name}`}
              description="It can read its logs, requests and metrics, change its settings, domains and variables, take backups and deploy it, as you and under your role."
              placeholder={`Ask anything about ${record.name}…`}
              suggestions={SUGGESTIONS}
              sends={`what the overview shows about ${deployment.environmentName} and what it reads to answer`}
              onNew={startNew}
            />
          }
          chats={
            <AgentChatList
              chats={chats.data}
              error={chats.error}
              activeId={agentChatId(session)}
              onNew={startNew}
              onDeleted={(chat) => {
                if (chat.id === wanted) router.replace(base, { scroll: false })
              }}
              onOpen={(chat) => {
                if (chat.id === agentChatId(session)) return
                router.replace(`${base}?chat=${encodeURIComponent(chat.id)}`, { scroll: false })
              }}
              environmentOf={(chat) =>
                project.staging && chat.scope.environment !== project.environmentId
                  ? environmentName(chat.scope.environment)
                  : undefined
              }
            />
          }
        />
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
