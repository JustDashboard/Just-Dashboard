"use client"

import { useCallback, useMemo, useState } from "react"
import { ApiError, del, get, post, put } from "@/lib/api"
import type { AppDomainStatus } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"

/**
 * This server's just-dashboard.com address (`/dashboard/link/domain`).
 *
 * Opening the page is what asks the site again (the backend reads `GET /domain` on each read),
 * so the steady cadence is slow; while something is under way — the wildcard being issued, or
 * an offered address being claimed by itself — the page asks every three seconds until it settles.
 * Each mutation's answer is drawn at once, as the link's own hook does.
 *
 * A route that answers 404 is a backend without the feature: the hook reports `missing` and the
 * pages leave the offer out rather than show an error for something that is not there.
 */

const SETTLING_POLL = 3000
const STEADY_POLL = 60_000

/** Whether the server is in the middle of something the page should watch closely. */
function settling(status: AppDomainStatus) {
  const claiming = Boolean(status.offer && !status.offer.error && !status.domain)
  return claiming || status.certificate?.state === "issuing"
}

export function useAppDomain({ enabled = true }: { enabled?: boolean } = {}) {
  const [settle, setSettle] = useState(false)
  const [adopted, setAdopted] = useState<{ status: AppDomainStatus; at: number }>()

  const read = useCallback(async (signal?: AbortSignal) => {
    const next = await get<AppDomainStatus>("/dashboard/link/domain", undefined, signal)
    setSettle(settling(next))
    return next
  }, [])

  const poll = usePoll(read, settle ? SETTLING_POLL : STEADY_POLL, [], {
    enabled,
    key: "/dashboard/link/domain",
  })

  const adopt = useCallback((status: AppDomainStatus) => {
    setSettle(settling(status))
    setAdopted({ status, at: Date.now() })
  }, [])

  /** A refusal that says the page is out of date is followed by a read, so both are true together. */
  const guarded = useCallback(
    async <T>(run: () => Promise<T>) => {
      try {
        return await run()
      } catch (err) {
        if (err instanceof ApiError && (err.status === 409 || err.status === 403)) {
          try {
            adopt(await read())
          } catch {
            // The refusal is what the person needs to hear; a failed re-read says nothing more.
          }
        }
        throw err
      }
    },
    [adopt, read],
  )

  const claim = useCallback(
    (handle: string) =>
      guarded(async () => adopt(await put<AppDomainStatus>("/dashboard/link/domain", { handle }))),
    [adopt, guarded],
  )

  const release = useCallback(
    () =>
      guarded(async () => {
        await del("/dashboard/link/domain")
        adopt(await read())
      }),
    [adopt, guarded, read],
  )

  const issue = useCallback(
    () =>
      guarded(async () => adopt(await post<AppDomainStatus>("/dashboard/link/domain/certificate"))),
    [adopt, guarded],
  )

  const setAutoAssign = useCallback(
    (autoAssign: boolean) =>
      guarded(async () =>
        adopt(await put<AppDomainStatus>("/dashboard/link/domain/settings", { autoAssign })),
      ),
    [adopt, guarded],
  )

  const missing = poll.error instanceof ApiError && poll.error.status === 404
  const status = adopted && (poll.lastSuccess ?? 0) < adopted.at ? adopted.status : poll.data

  return useMemo(
    () => ({
      status: missing ? undefined : status,
      missing,
      error: missing ? undefined : poll.error,
      loading: poll.loading,
      refresh: poll.refresh,
      claim,
      release,
      issue,
      setAutoAssign,
    }),
    [missing, status, poll.error, poll.loading, poll.refresh, claim, release, issue, setAutoAssign],
  )
}

export type AppDomainControls = ReturnType<typeof useAppDomain>
