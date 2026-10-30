"use client"

import { useCallback, useMemo, useState } from "react"
import { ApiError, del, get, post, put } from "@/lib/api"
import type { AccountLinkScope, AccountLinkStatus } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"

/**
 * This server's link to a just-dashboard.com account.
 *
 * While a request waits for the person to approve it on the site, the page asks every five seconds,
 * so approving in the other tab flips this one to linked without a reload. Once there is nothing to
 * wait for the heartbeat is the only thing that moves, and it moves every thirty seconds.
 *
 * Each mutation answers with the new status (or, for the two that answer 204, is followed by a
 * read), and that answer is drawn at once. Waiting for the next poll to notice would put the form
 * back on screen, button live again, for the length of one more request.
 */

const PENDING_POLL = 5000
const STEADY_POLL = 15_000

export function useAccountLink() {
  const [state, setState] = useState<AccountLinkStatus["state"]>()
  // The answer to the last mutation, until a read newer than it replaces it.
  const [adopted, setAdopted] = useState<{ status: AccountLinkStatus; at: number }>()

  const read = useCallback(async (signal?: AbortSignal) => {
    const next = await get<AccountLinkStatus>("/dashboard/link", undefined, signal)
    setState(next.state)
    return next
  }, [])

  const poll = usePoll(read, state === "pending" ? PENDING_POLL : STEADY_POLL)

  const adopt = useCallback((status: AccountLinkStatus) => {
    setState(status.state)
    setAdopted({ status, at: Date.now() })
  }, [])

  /**
   * A refusal that says the page is out of date (another tab or the terminal got there first, or
   * linking was switched off) is followed by a read, so the page shows what is true before the
   * error is.
   */
  const guarded = useCallback(
    async <T>(run: () => Promise<T>) => {
      try {
        return await run()
      } catch (err) {
        if (
          err instanceof ApiError &&
          (err.status === 409 || err.code === "account_link_disabled")
        ) {
          adopt(await read())
        }
        throw err
      }
    },
    [adopt, read],
  )

  const begin = useCallback(
    (scope: AccountLinkScope) =>
      guarded(async () => adopt(await post<AccountLinkStatus>("/dashboard/link", { scope }))),
    [adopt, guarded],
  )

  const cancel = useCallback(
    () =>
      guarded(async () => {
        await del("/dashboard/link/request")
        adopt(await read())
      }),
    [adopt, guarded, read],
  )

  const changeScope = useCallback(
    (scope: AccountLinkScope) =>
      guarded(async () => adopt(await put<AccountLinkStatus>("/dashboard/link/scope", { scope }))),
    [adopt, guarded],
  )

  /** The status after a heartbeat attempt, so the caller can say whether it got through. */
  const sync = useCallback(
    () =>
      guarded(async () => {
        const next = await post<AccountLinkStatus>("/dashboard/link/sync")
        adopt(next)
        return next
      }),
    [adopt, guarded],
  )

  const unlink = useCallback(
    () =>
      guarded(async () => {
        await del("/dashboard/link")
        adopt(await read())
      }),
    [adopt, guarded, read],
  )

  const status = adopted && (poll.lastSuccess ?? 0) < adopted.at ? adopted.status : poll.data

  return useMemo(
    () => ({
      status,
      loading: poll.loading,
      error: poll.error,
      begin,
      cancel,
      changeScope,
      sync,
      unlink,
    }),
    [status, poll.loading, poll.error, begin, cancel, changeScope, sync, unlink],
  )
}
