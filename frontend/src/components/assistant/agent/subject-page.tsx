"use client"

import { useEffect, useRef, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Notice } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import { AssistantOff } from "@/components/assistant/off"
import { AssistantSettingsSheet } from "@/components/assistant/settings-sheet"
import { assistantProduct, useAssistantSettings } from "@/components/assistant/settings"
import { AgentChat, type AgentSuggestion } from "@/components/assistant/agent/chat"
import { AgentChatList } from "@/components/assistant/agent/chat-list"
import {
  useAgentChats,
  type AgentChatSummary,
  type AgentChatsQuery,
} from "@/components/assistant/agent/chats"
import { AgentWorkspace } from "@/components/assistant/agent/workspace"
import {
  agentBusy,
  agentChatId,
  agentPageOf,
  agentScopeKey,
  askAgent,
  newAgentChat,
  openAgentChat,
  useAgentSession,
  type AgentScope,
} from "@/components/assistant/agent/sessions"

/**
 * One subject's assistant as a page of its own — a deployment's, a database's: the
 * conversation filling the window under the subject's header, and beside it the
 * subject's chats and what the open one has done, with a shortcut to each change.
 * It goes on while you follow one, and the dock brings you back. `?chat=` opens a
 * saved chat, read back from the server and ready to carry on; `?ask=` begins one
 * with a question the page names (a deployment's Diagnose).
 *
 * What differs between subjects is only what is said, what is suggested, and when
 * the subject cannot be asked about — an archived project, a protected connection
 * — so each page passes those and this is the rest.
 */
export function SubjectAssistant({
  scope,
  chats: query,
  refusal,
  title,
  description,
  placeholder,
  sends,
  what,
  suggestions,
  asks,
  onChanged,
  onOtherEnvironment,
  environmentOf,
  slot,
  className,
}: {
  scope: AgentScope
  /** Which saved chats the rail lists. */
  chats: AgentChatsQuery
  /** Why this subject cannot be asked about now, said in place of the chat. */
  refusal?: React.ReactNode
  /** Said on an empty conversation. */
  title: string
  description: string
  placeholder: string
  /** What goes with a question, in the line under the box. */
  sends: string
  /** What it would do here once it is on, for the page that says it is off. */
  what: string
  suggestions: AgentSuggestion[]
  /** The questions `?ask=` may begin a chat with, by the word in the address. */
  asks?: Record<string, string>
  /** It changed something: the page reads its subject again. */
  onChanged?: () => void
  /** A saved chat from another environment of the same subject: the page switches to it. */
  onOtherEnvironment?: (environment: number) => void
  /** A word for a chat from another environment, on its row. */
  environmentOf?: (chat: AgentChatSummary) => string | undefined
  slot: string
  className?: string
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const settings = useAssistantSettings(true)
  const [settingUp, setSettingUp] = useState(false)
  // The failed read a retry was pressed for: the button waits while that same
  // failure is still the latest, and a new one hands it back.
  const [retried, setRetried] = useState<unknown>()
  const view = settings.data
  const session = useAgentSession(scope)
  const allowed = can("service.control") && !refusal
  const chats = useAgentChats(query, allowed && Boolean(view?.configured))

  // What it changed, the page's own reading of its subject follows.
  const changed = useRef(session.changes)
  useEffect(() => {
    if (session.changes !== changed.current) {
      changed.current = session.changes
      onChanged?.()
    }
  }, [session.changes, onChanged])

  const searchParams = useSearchParams()
  const router = useRouter()
  const base = agentPageOf(scope)

  // A page that sends people here to ask something arrives with what to ask,
  // once, in a chat of its own.
  const asked = useRef(false)
  const asking = asks?.[searchParams.get("ask") ?? ""]
  useEffect(() => {
    if (!asking || asked.current || !view?.configured || !allowed) return
    asked.current = true
    const front = agentBusy(session) ? session : newAgentChat(scope)
    if (front.transcript.turns.length === 0) askAgent(front, asking)
    router.replace(base, { scroll: false })
  }, [asking, view?.configured, allowed, session, scope, router, base])

  // `?chat=` puts a saved chat in front. A chat from another environment of the
  // subject switches the page to it first, and is opened there.
  const wanted = searchParams.get("chat")
  const subject = agentScopeKey(scope)
  const opening = useRef("")
  useEffect(() => {
    if (!wanted) opening.current = ""
    if (!wanted || !view?.configured || !allowed) return
    const attempt = `${subject}:${wanted}`
    if (opening.current === attempt) return
    opening.current = attempt
    void openAgentChat(scope, wanted).then((other) => {
      if (other?.environment) onOtherEnvironment?.(other.environment)
    })
  }, [wanted, subject, scope, view?.configured, allowed, onOtherEnvironment])

  if (!can("service.control")) {
    return (
      <Notice tone="warning" title="Your role does not ask the assistant" className="mt-6">
        Asking it takes the service control capability, because what it does is control services.
      </Notice>
    )
  }
  if (refusal) return <>{refusal}</>
  const startNew = () => {
    newAgentChat(scope)
    if (wanted) router.replace(base, { scroll: false })
  }
  return (
    <div data-slot={slot} className={cn("flex min-h-0 min-w-0 flex-1 flex-col", className)}>
      {!view ? (
        settings.error ? (
          // The assistant's off state with the read to try again, not the
          // transport's error as a headline: the page does not know whether
          // it is on, and the next step is to ask again.
          <AssistantOff
            className="m-auto"
            settings={undefined}
            onSetUp={() => setSettingUp(true)}
            what={what}
            failed={{
              retrying: retried === settings.error,
              onRetry: () => {
                setRetried(settings.error)
                settings.refresh()
              },
            }}
          />
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
          what={what}
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
              title={title}
              description={description}
              placeholder={placeholder}
              suggestions={suggestions}
              sends={sends}
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
              environmentOf={environmentOf}
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
