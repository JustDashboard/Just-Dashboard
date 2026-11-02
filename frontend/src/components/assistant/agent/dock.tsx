"use client"

import Link from "next/link"
import { useEffect } from "react"
import { usePathname } from "next/navigation"
import { ArrowLeft, Cross } from "@/components/icons"
import { IconAction } from "@/components/icon-action"
import { useHeldSessions } from "@/components/remote-desktop/held-sessions"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { useAuth } from "@/hooks/use-auth"
import { cn } from "@/lib/utils"
import { AssistantMark } from "@/components/assistant/mark"
import { assistantProduct, useAssistantSettings } from "@/components/assistant/settings"
import { agentStatus, changesOf } from "@/components/assistant/agent/model"
import {
  agentBusy,
  agentPageOf,
  dismissAgent,
  endAgentSessions,
  useAgentSessions,
} from "@/components/assistant/agent/sessions"

/**
 * The way back to a conversation from anywhere: the assistant's floating answer to
 * a shortcut, as Remote Desktop's dock is to a session left running. It says who it
 * is about and what it is doing, lights while it works and turns amber while it
 * waits on you, and one press goes back to the chat. It is in the shell, so the
 * conversation it stands for goes on behind any page.
 */
export function AgentDock() {
  const { can } = useAuth()
  const sessions = useAgentSessions()
  const pathname = usePathname()
  const { held } = useHeldSessions()
  const settings = useAssistantSettings(can("service.control"))
  const shown = sessions.filter(
    (session) =>
      session.transcript.turns.length > 0 &&
      !session.dismissed &&
      // On its own page the chat is the page, and a setup's has the same one.
      pathname !== agentPageOf(session.scope).split("?")[0],
  )
  // Signing out, which remounts the shell for the next account, ends them.
  useEffect(() => endAgentSessions, [])
  if (shown.length === 0) return null
  const product = assistantProduct(settings.data)
  // Remote Desktop's dock has this corner first; this sits above it.
  const heldOpen = held.filter((session) => pathname !== `/remote-desktop/${session.id}`).length
  return (
    <aside
      aria-label="Assistant conversations"
      data-slot="agent-dock"
      style={heldOpen > 0 ? { bottom: `${1 + heldOpen * 3.75 + 0.75}rem` } : undefined}
      className="fixed right-4 bottom-4 z-40 flex w-[22rem] max-w-[calc(100vw-2rem)] animate-rise flex-col gap-1 rounded-xl border border-border-strong bg-popover/95 p-2 text-popover-foreground shadow-xl backdrop-blur"
    >
      {shown.map((session) => {
        const status = agentStatus(session.transcript)
        const busy = agentBusy(session)
        const changes = changesOf(session.transcript).length
        return (
          <div
            key={session.key}
            data-slot="agent-dock-row"
            data-tone={status.tone}
            className="flex min-w-0 items-center gap-3 rounded-lg p-1.5"
          >
            <AssistantMark product={product} working={busy} size="sm" />
            <div className="min-w-0 flex-1">
              <p className="truncate text-body font-medium">{session.scope.name}</p>
              <p
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
                  <TextShimmer className="max-w-full truncate align-bottom">
                    {status.text}
                  </TextShimmer>
                ) : (
                  status.text
                )}
                {changes > 0 && status.tone === "working" && ` · ${changes} changed`}
              </p>
            </div>
            <Button asChild size="xs" variant={status.tone === "asking" ? "default" : "outline"}>
              <Link href={agentPageOf(session.scope)}>
                <ArrowLeft />
                Back to chat
              </Link>
            </Button>
            {!busy && (
              <IconAction
                label={`Hide the conversation about ${session.scope.name}`}
                onClick={() => dismissAgent(session)}
              >
                <Cross />
              </IconAction>
            )}
          </div>
        )
      })}
    </aside>
  )
}
