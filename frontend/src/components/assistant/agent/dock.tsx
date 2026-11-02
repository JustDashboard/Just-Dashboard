"use client"

import Link from "next/link"
import { useEffect } from "react"
import { usePathname } from "next/navigation"
import { AnimatePresence, MotionConfig, motion, useReducedMotion } from "motion/react"
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
  agentHref,
  agentPageOf,
  agentWorking,
  dismissAgent,
  endAgentSessions,
  isCurrentAgent,
  useAgentSessions,
} from "@/components/assistant/agent/sessions"

const EASE = [0.16, 1, 0.3, 1] as const

/**
 * The way back to a conversation from anywhere: the assistant's floating answer to
 * a shortcut, as Remote Desktop's dock is to a session left running. It says who it
 * is about and what it is doing, lights while it works and turns amber while it
 * waits on you, and one press goes back to the chat. It is in the shell, so the
 * conversation it stands for goes on behind any page. It rises in when there is a
 * chat to go back to and sinks away when there is not; a row joining or leaving it
 * eases rather than jumping the rows beside it.
 */
export function AgentDock() {
  const { can } = useAuth()
  const sessions = useAgentSessions()
  const pathname = usePathname()
  const { held } = useHeldSessions()
  const reduced = useReducedMotion()
  const settings = useAssistantSettings(can("service.control"))
  const shown = sessions.filter(
    (session) =>
      session.transcript.turns.length > 0 &&
      !session.dismissed &&
      // A chat read back from the history and put aside is not going on anywhere;
      // the one in front for its subject is, and so is one still answering.
      (isCurrentAgent(session) || agentBusy(session)) &&
      // On its own page the chat is the page, and a setup's has the same one.
      pathname !== agentPageOf(session.scope).split("?")[0],
  )
  // Signing out, which remounts the shell for the next account, ends them.
  useEffect(() => endAgentSessions, [])
  const product = assistantProduct(settings.data)
  // Remote Desktop's dock has this corner first; this sits above it.
  const heldOpen = held.filter((session) => pathname !== `/remote-desktop/${session.id}`).length
  return (
    <MotionConfig reducedMotion="user">
      <AnimatePresence>
        {shown.length > 0 && (
          <motion.aside
            key="dock"
            aria-label="Assistant conversations"
            data-slot="agent-dock"
            initial={{ opacity: 0, y: 12 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: 12 }}
            transition={{ duration: 0.22, ease: EASE }}
            style={heldOpen > 0 ? { bottom: `${1 + heldOpen * 3.75 + 0.75}rem` } : undefined}
            className="fixed right-4 bottom-4 z-40 flex w-[22rem] max-w-[calc(100vw-2rem)] flex-col gap-1 rounded-xl border border-border-strong bg-popover/95 p-2 text-popover-foreground shadow-xl backdrop-blur"
          >
            <AnimatePresence initial={false}>
              {shown.map((session) => {
                const status = agentStatus(session.transcript)
                const busy = agentBusy(session)
                const changes = changesOf(session.transcript).length
                return (
                  <motion.div
                    key={session.key}
                    layout="position"
                    initial={{ opacity: 0, height: 0 }}
                    animate={{ opacity: 1, height: "auto" }}
                    exit={{ opacity: 0, height: 0 }}
                    transition={{ duration: reduced ? 0 : 0.2, ease: EASE }}
                    data-slot="agent-dock-row"
                    data-tone={status.tone}
                    className="overflow-hidden"
                  >
                    <div className="flex min-w-0 items-center gap-3 rounded-lg p-1.5">
                      <AssistantMark product={product} working={agentWorking(session)} size="sm" />
                      <div className="min-w-0 flex-1">
                        <p className="truncate text-body font-medium">{session.scope.name}</p>
                        <p
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
                            <TextShimmer className="max-w-full truncate align-bottom">
                              {status.text}
                            </TextShimmer>
                          ) : (
                            status.text
                          )}
                          {changes > 0 && status.tone === "working" && ` · ${changes} changed`}
                        </p>
                      </div>
                      <Button
                        asChild
                        size="xs"
                        variant={status.tone === "asking" ? "default" : "outline"}
                      >
                        <Link href={agentHref(session)}>
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
                  </motion.div>
                )
              })}
            </AnimatePresence>
          </motion.aside>
        )}
      </AnimatePresence>
    </MotionConfig>
  )
}
