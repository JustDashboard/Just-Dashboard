"use client"

import { useCallback, useEffect, useRef } from "react"
import { ApiError, errorMessage, fetchResponse, get } from "@/lib/api"
import { useMemoryState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import {
  EMPTY_TRANSCRIPT,
  answering,
  applyEvent,
  beginTurn,
  decide,
  endTurn,
  parseEvent,
  readLines,
  type AssistantEvent,
  type AssistantTask,
  type Transcript,
} from "@/components/database/query/assistant/transcript"

export type AssistantProvider = "anthropic" | "openai"

/** `GET /databases/assistant`: the provider as any page may read it. The key is never here. */
export interface AssistantSettings {
  configured: boolean
  provider?: AssistantProvider
  model?: string
  modelLabel?: string
  baseUrl?: string
  keySet: boolean
  updatedAt?: string
  updatedBy?: string
  /** The Claude models the settings offer, newest tier first. */
  models: { id: string; label: string }[]
  /** A saved provider this install cannot read. */
  problem?: string
}

/** What the settings form sends to `PUT /databases/assistant`. */
export interface AssistantSetup {
  provider: AssistantProvider
  model: string
  baseUrl: string
  key: string
}

/**
 * The assistant's settings, read once and again when they are saved. They
 * change when an administrator changes them, so the read is slow.
 */
export function useAssistantSettings(enabled: boolean) {
  return usePoll<AssistantSettings>(
    (signal) => get<AssistantSettings>("/databases/assistant", undefined, signal),
    120_000,
    [],
    { enabled },
  )
}

/**
 * One connection's conversation with the assistant: asking, answering the
 * calls it asks for, and stopping an answer.
 *
 * What was drawn is kept in memory for the browser tab, never in Web
 * Storage, because an attached result is rows of somebody's data. An answer
 * still coming in when the editor is left is stopped: nobody is reading it,
 * and it is billed to the install's key.
 */
export function useAssistant(id: number) {
  const [transcript, setTranscript] = useMemoryState<Transcript>(
    `databases.${id}.query.assistant`,
    EMPTY_TRANSCRIPT,
  )
  const flight = useRef<AbortController | null>(null)
  useEffect(() => () => flight.current?.abort(), [])

  const stream = useCallback(
    async (turn: string, body: object) => {
      const controller = new AbortController()
      flight.current = controller
      const apply = (event: AssistantEvent) => setTranscript((t) => applyEvent(t, turn, event))
      let ended = false
      try {
        const response = await fetchResponse(`/databases/${id}/assistant`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
          signal: controller.signal,
        })
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
    [id, setTranscript],
  )

  const last = transcript.turns.at(-1)
  const busy = answering(last)

  const ask = useCallback(
    (task: AssistantTask, schema: string) => {
      if (flight.current) return
      const turn = crypto.randomUUID()
      const next = beginTurn(transcript, turn, task, schema)
      setTranscript(next)
      void stream(turn, { conversation: next.conversation, schema, task })
    },
    [transcript, setTranscript, stream],
  )

  const answer = useCallback(
    (allow: boolean) => {
      if (flight.current || !last || last.phase !== "asking") return
      setTranscript(decide(transcript, allow))
      void stream(last.id, { conversation: transcript.conversation, allow })
    },
    [last, transcript, setTranscript, stream],
  )

  const stop = useCallback(() => flight.current?.abort(), [])

  const reset = useCallback(() => {
    flight.current?.abort()
    setTranscript(EMPTY_TRANSCRIPT)
  }, [setTranscript])

  return { transcript, busy, ask, answer, stop, reset }
}

export type AssistantState = ReturnType<typeof useAssistant>
