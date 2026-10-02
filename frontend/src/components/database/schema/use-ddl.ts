"use client"

import { useEffect, useState } from "react"
import { ApiError, api } from "@/lib/api"
import { requestKey, type DdlRequest } from "@/components/database/schema/changes"
import type { DdlAnswer } from "@/components/database/schema/types"

/** How long a form is left alone before the server is asked what it would run. */
const SETTLE_MS = 320

const asError = (err: unknown) => (err instanceof Error ? err : new Error(String(err)))

/** Runs a structure change. The statement in the answer is what ran. */
export function runChange(id: number, request: DdlRequest, signal?: AbortSignal) {
  return api<DdlAnswer>(`/databases/${id}${request.path}`, {
    method: request.method,
    body: request.body,
    signal,
  })
}

/** Asks for the statement a change would run. Nothing runs. */
export function previewChange(id: number, request: DdlRequest, signal?: AbortSignal) {
  return api<DdlAnswer>(`/databases/${id}${request.path}`, {
    method: request.method,
    body: request.body,
    query: { preview: 1 },
    signal,
  })
}

export type Preview = {
  /** The server's plan for the request in hand; absent until it has answered for exactly that one. */
  answer: DdlAnswer | undefined
  /** Why the request in hand cannot be planned: the server's own words. */
  error: Error | undefined
  /** The request in hand has been asked about and has not been answered. */
  pending: boolean
}

type Settled = { key: string; answer?: DdlAnswer; error?: Error }

/**
 * The server's own statement for a form, kept in step with it.
 *
 * Each time the request changes it is asked for again with `?preview=1`,
 * once the reader has stopped typing, and an answer is only ever shown for
 * the request it answers: while the form is ahead of the server there is no
 * statement on screen and no command to press. `request` null is a form not
 * filled in far enough to ask about.
 *
 * Not for the destructive routes — their previews spend the role's budget and
 * are asked once (`use-destroy.tsx`).
 */
export function usePreview(id: number, request: DdlRequest | null, attempt = 0): Preview {
  const key = requestKey(request)
  const [settled, setSettled] = useState<Settled>({ key: "" })

  useEffect(() => {
    if (!request) return
    const controller = new AbortController()
    const timer = setTimeout(() => {
      previewChange(id, request, controller.signal)
        .then((answer) => {
          if (!controller.signal.aborted) setSettled({ key, answer })
        })
        .catch((err: unknown) => {
          if (!controller.signal.aborted) setSettled({ key, error: asError(err) })
        })
    }, SETTLE_MS)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
    // The request is read through its key: a form hands over a new object
    // that says the same thing on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, key, attempt])

  const current = key !== "" && settled.key === key
  return {
    answer: current ? settled.answer : undefined,
    error: current ? settled.error : undefined,
    pending: key !== "" && !current,
  }
}

type Support = { supported: boolean; reason?: string }

/** What the server said of a form's option, per connection, for as long as the tab lives. */
const asked = new Map<string, Support>()

/**
 * Whether the engine can plan one option of a form, asked of the server
 * before the option is offered.
 *
 * Some limits are the engine's own and no flag of the driver catalogue says
 * them: a view that cannot be replaced, a column whose nullability is part of
 * its type. The routes refuse those with the reason, and a refusal read here —
 * from a preview, which runs nothing — is drawn as that reason where the
 * control would be, so nobody learns of the limit by filling a form in.
 *
 * `feature` names the answer, so one question is asked once per connection
 * (or per column, when the name says which). `undefined` while it is being
 * asked, and for a question that could not be put: the form then offers the
 * option and the statement's own preview is what answers.
 */
export function useSupport(
  id: number,
  feature: string,
  request: DdlRequest | null,
): Support | undefined {
  const key = `${id}:${feature}`
  const [answer, setAnswer] = useState<{ key: string; support: Support }>()
  const known = asked.get(key)

  useEffect(() => {
    if (!request || asked.has(key)) return
    const controller = new AbortController()
    previewChange(id, request, controller.signal)
      .then((): Support => ({ supported: true }))
      .catch((err: unknown): Support | undefined =>
        // Only the route's own refusal is an answer; a role's, or a server
        // that did not reply, says nothing about the engine.
        err instanceof ApiError && err.status === 400
          ? { supported: false, reason: err.message }
          : undefined,
      )
      .then((support) => {
        if (!support || controller.signal.aborted) return
        asked.set(key, support)
        setAnswer({ key, support })
      })
    return () => controller.abort()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, key, request === null])

  return known ?? (answer?.key === key ? answer.support : undefined)
}
