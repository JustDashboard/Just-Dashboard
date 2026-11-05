"use client"

import { createContext, useCallback, useContext, useMemo, useState } from "react"
import { ApiError, del, get, post, put } from "@/lib/api"
import { selfServer, shellHub, type ShellHub } from "@/lib/hub-link"
import type { HubLinkCheck, HubLinkScope, HubLinkServer, HubLinkStatus } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"

/**
 * This server's link to a Just Hub, in the two shapes its readers need.
 *
 * The shell's: `HubLinkProvider` reads `GET /dashboard/hub` for the server switcher, the palette's
 * Servers, the tab's host name and the rail's notice card — and only while the session says a hub
 * link is stored (`auth.hub?.linked`, hub-link-protocol §8.3). An unlinked server makes no request
 * at all, so its shell is the shell it was. Every reader asks `useShellHub()`, which is `null`
 * until there is something to draw.
 *
 * The Settings page's: `useHubLink()`, the account link's hook (`use-account-link.ts`) pointed at
 * the hub's routes — it reads whatever the state, asks every few seconds while a request waits so
 * approving in the hub flips the page to linked, and draws each mutation's answer at once.
 */

/** The fleet's states move on the hub's heartbeat, which is fifteen seconds. */
const SHELL_POLL = 15_000

export type ShellHubValue = ShellHub & {
  /** This server's own entry, once the hub has replied to a heartbeat. */
  self?: HubLinkServer
  /** `/dashboard/hub` has answered; before it, an empty fleet means "not read yet". */
  answered: boolean
  /** The last read failed; what was drawn before it stays. */
  failed: boolean
  refresh: () => void
}

const ShellHubContext = createContext<ShellHubValue | null>(null)

export function HubLinkProvider({ children }: { children: React.ReactNode }) {
  const { status: auth } = useAuth()
  const session = auth?.authenticated ? auth.hub : undefined
  const linked = Boolean(session?.linked)
  const poll = usePoll(
    (signal) => get<HubLinkStatus>("/dashboard/hub", undefined, signal),
    SHELL_POLL,
    [],
    { enabled: linked },
  )
  const { data, refresh } = poll
  const answered = linked && data !== undefined
  const failed = Boolean(poll.error)

  const value = useMemo<ShellHubValue | null>(() => {
    const hub = shellHub(session, answered ? data : undefined)
    return hub ? { ...hub, self: selfServer(hub.fleet), answered, failed, refresh } : null
  }, [session, answered, data, failed, refresh])

  return <ShellHubContext.Provider value={value}>{children}</ShellHubContext.Provider>
}

/** The hub the shell draws, or `null`: with no hub link nothing hub-shaped renders. */
export function useShellHub() {
  return useContext(ShellHubContext)
}

/** A request waits on a person in the hub; three seconds is how long the page lags their click. */
const PENDING_POLL = 3000
const STEADY_POLL = 15_000

export function useHubLink() {
  const [state, setState] = useState<HubLinkStatus["state"]>()
  // The answer to the last mutation, until a read newer than it replaces it.
  const [adopted, setAdopted] = useState<{ status: HubLinkStatus; at: number }>()

  const read = useCallback(async (signal?: AbortSignal) => {
    const next = await get<HubLinkStatus>("/dashboard/hub", undefined, signal)
    setState(next.state)
    return next
  }, [])

  const poll = usePoll(read, state === "pending" ? PENDING_POLL : STEADY_POLL)

  const adopt = useCallback((status: HubLinkStatus) => {
    setState(status.state)
    setAdopted({ status, at: Date.now() })
  }, [])

  /**
   * A refusal that says the page is out of date — the terminal connected first, a request is
   * already waiting, the link was switched off — is followed by a read, so the page shows what is
   * true before the error is.
   */
  const guarded = useCallback(
    async <T,>(run: () => Promise<T>) => {
      try {
        return await run()
      } catch (err) {
        if (err instanceof ApiError && (err.status === 409 || err.code === "hub_link_disabled")) {
          adopt(await read())
        }
        throw err
      }
    },
    [adopt, read],
  )

  const begin = useCallback(
    (url: string, scope: HubLinkScope) =>
      guarded(async () => adopt(await post<HubLinkStatus>("/dashboard/hub", { url, scope }))),
    [adopt, guarded],
  )

  const cancel = useCallback(
    () =>
      guarded(async () => {
        await del("/dashboard/hub/request")
        adopt(await read())
      }),
    [adopt, guarded, read],
  )

  const changeScope = useCallback(
    (scope: HubLinkScope) =>
      guarded(async () => adopt(await put<HubLinkStatus>("/dashboard/hub/scope", { scope }))),
    [adopt, guarded],
  )

  /** The status after a heartbeat attempt, so the caller can say whether it got through. */
  const sync = useCallback(
    () =>
      guarded(async () => {
        const next = await post<HubLinkStatus>("/dashboard/hub/sync")
        adopt(next)
        return next
      }),
    [adopt, guarded],
  )

  const disconnect = useCallback(
    () =>
      guarded(async () => {
        await del("/dashboard/hub")
        adopt(await read())
      }),
    [adopt, guarded, read],
  )

  /**
   * The URL rule without starting anything (§8.2): the address as Connect would store it, and
   * whose machine answers there. It contacts nothing but this server's own tailscaled.
   */
  const check = useCallback(
    (url: string, signal?: AbortSignal) =>
      get<HubLinkCheck>("/dashboard/hub/check", { url }, signal),
    [],
  )

  const status = adopted && (poll.lastSuccess ?? 0) < adopted.at ? adopted.status : poll.data

  return useMemo(
    () => ({
      status,
      error: poll.error,
      begin,
      cancel,
      changeScope,
      sync,
      disconnect,
      check,
    }),
    [status, poll.error, begin, cancel, changeScope, sync, disconnect, check],
  )
}
