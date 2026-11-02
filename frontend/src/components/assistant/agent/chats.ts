"use client"

import { useEffect } from "react"
import { del, get, patch } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import {
  agentChatDeleted,
  agentChatRenamed,
  onAgentChatsChanged,
} from "@/components/assistant/agent/sessions"

/**
 * The person's saved chats with the assistant. Each is kept on the server under its
 * owner, so it outlives the tab, the process and the browser it began in, and can
 * be carried on from any of them; another person's are never listed.
 */
export interface AgentChatSummary {
  id: string
  title: string
  scope: {
    kind: "deployment" | "draft" | "server"
    deployment?: number
    environment?: number
    draft?: string
    name: string
  }
  createdAt: string
  updatedAt: string
  provider: string
  model: string
  /** A tab somewhere is answering it now. */
  running: boolean
  /** Calls in it wait on the person's Allow or Decline. */
  waiting: boolean
  steps: number
  changes: number
}

/** Which chats a list holds: one deployment's, the server's, a draft's, or all. */
export type AgentChatsQuery =
  { deployment: number } | { scope: "server" } | { draft: string } | Record<string, never>

/**
 * A list of saved chats, read again whenever this tab begins or finishes one, and
 * every little while for what other tabs do.
 */
export function useAgentChats(query: AgentChatsQuery, enabled = true) {
  const signature = JSON.stringify(query)
  const poll = usePoll<AgentChatSummary[]>(
    async (signal) =>
      (
        await get<{ chats: AgentChatSummary[] | null }>(
          "/assistant/chats",
          query as Record<string, string | number>,
          signal,
        )
      ).chats ?? [],
    20_000,
    [signature],
    { enabled },
  )
  const { refresh } = poll
  useEffect(() => onAgentChatsChanged(refresh), [refresh])
  return poll
}

export async function renameAgentChat(id: string, title: string) {
  const { chat } = await patch<{ chat: AgentChatSummary }>(
    `/assistant/chats/${encodeURIComponent(id)}`,
    { title },
  )
  agentChatRenamed(id, chat?.title ?? title)
  return chat
}

export async function deleteAgentChat(id: string) {
  await del(`/assistant/chats/${encodeURIComponent(id)}`)
  agentChatDeleted(id)
}
