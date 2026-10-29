"use client"

import { useCallback, useEffect, useRef } from "react"
import { ApiError, errorMessage, fetchResponse } from "@/lib/api"
import { useMemoryState } from "@/lib/view-state"
import {
  EMPTY_OPERATOR,
  answering,
  applyEvent,
  beginTurn,
  decideAll,
  decideRead,
  endTurn,
  parseEvent,
  readLines,
  readyDecisions,
  sendDecisions,
  settleProposal,
  type OperatorEvent,
  type OperatorTranscript,
  type ProposalOutcome,
} from "@/components/deploy/operator/operator-model"

/**
 * One environment's conversation with the deployment operator: asking,
 * answering the reads it asks for one by one, and stopping an answer.
 *
 * What was drawn is kept in memory for the browser tab, never in Web
 * Storage: an answer quotes the application's own log lines. An answer
 * still coming in when the page is left is stopped, because nobody is
 * reading it and it is billed to the install's key.
 */
export function useOperator(projectId: number, environmentId: number, environment?: number) {
  const [transcript, setTranscript] = useMemoryState<OperatorTranscript>(
    `deploy.${projectId}.${environmentId}.operator`,
    EMPTY_OPERATOR,
  )
  const flight = useRef<AbortController | null>(null)
  useEffect(() => () => flight.current?.abort(), [])

  const stream = useCallback(
    async (turn: string, body: object) => {
      const controller = new AbortController()
      flight.current = controller
      const apply = (event: OperatorEvent) => setTranscript((t) => applyEvent(t, turn, event))
      let ended = false
      try {
        const response = await fetchResponse(
          `/deploy/${projectId}/assistant`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
            signal: controller.signal,
          },
          environment ? { environment } : undefined,
        )
        if (!response.body) throw new Error("The answer arrived with nothing in it")
        const reader = response.body.getReader()
        const decoder = new TextDecoder()
        let held = ""
        const take = (lines: string[]) => {
          for (const line of lines) {
            const event = parseEvent(line)
            if (!event) continue
            if (event.type === "done" || event.type === "error") ended = true
            apply(event)
          }
        }
        for (;;) {
          const { done, value } = await reader.read()
          if (done) break
          const read = readLines(held, decoder.decode(value, { stream: true }))
          held = read.rest
          take(read.lines)
        }
        take(readLines(held, "\n").lines)
        if (!ended) {
          setTranscript((t) => endTurn(t, turn, "failed", "The answer stopped before it finished."))
        }
      } catch (err) {
        if (controller.signal.aborted) {
          setTranscript((t) => endTurn(t, turn, "stopped"))
        } else if (
          err instanceof ApiError &&
          ["conversation_gone", "conversation_ended"].includes(err.code)
        ) {
          // The next question begins a conversation of its own.
          setTranscript((t) => ({ ...endTurn(t, turn, "failed", err.message), conversation: "" }))
        } else if (!ended) {
          setTranscript((t) => endTurn(t, turn, "failed", errorMessage(err)))
        }
      } finally {
        if (flight.current === controller) flight.current = null
      }
    },
    [projectId, environment, setTranscript],
  )

  const last = transcript.turns.at(-1)
  const busy = answering(last)

  const ask = useCallback(
    (question: string) => {
      if (flight.current) return
      const turn = crypto.randomUUID()
      const next = beginTurn(transcript, turn, question)
      setTranscript(next)
      void stream(turn, { conversation: next.conversation, question })
    },
    [transcript, setTranscript, stream],
  )

  /**
   * One read answered; once every read the answer waits on has an answer,
   * they go to the server together.
   */
  const decide = useCallback(
    (id: string | "all", allow: boolean) => {
      if (flight.current || !last || last.phase !== "asking") return
      const next = id === "all" ? decideAll(transcript, allow) : decideRead(transcript, id, allow)
      const decisions = readyDecisions(next)
      if (!decisions) {
        setTranscript(next)
        return
      }
      setTranscript(sendDecisions(next))
      void stream(last.id, { conversation: transcript.conversation, decisions })
    },
    [last, transcript, setTranscript, stream],
  )

  const settle = useCallback(
    (proposalId: string, outcome: ProposalOutcome) =>
      setTranscript((t) => settleProposal(t, proposalId, outcome)),
    [setTranscript],
  )

  const stop = useCallback(() => flight.current?.abort(), [])

  const reset = useCallback(() => {
    flight.current?.abort()
    setTranscript(EMPTY_OPERATOR)
  }, [setTranscript])

  return { transcript, busy, ask, decide, settle, stop, reset }
}

export type OperatorState = ReturnType<typeof useOperator>
