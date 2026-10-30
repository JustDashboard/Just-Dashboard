"use client"

import { useCallback, useMemo, useState } from "react"
import { get, put } from "@/lib/api"
import type { UsagePingStatus } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"

/**
 * The optional daily count of servers, and this server's answer to it.
 *
 * Nothing here moves more than once a day, so the poll only exists to notice an answer given in
 * another tab. Answering responds with the new status, first report included, and that is drawn at
 * once.
 */

const POLL = 60_000

export function useUsagePing({ enabled = true }: { enabled?: boolean } = {}) {
  const poll = usePoll(
    (signal) => get<UsagePingStatus>("/dashboard/usage-ping", undefined, signal),
    POLL,
    [],
    { enabled },
  )
  const [adopted, setAdopted] = useState<{ status: UsagePingStatus; at: number }>()
  const [answering, setAnswering] = useState(false)

  const answer = useCallback(async (on: boolean) => {
    setAnswering(true)
    try {
      const status = await put<UsagePingStatus>("/dashboard/usage-ping", { on })
      setAdopted({ status, at: Date.now() })
    } finally {
      setAnswering(false)
    }
  }, [])

  const status = adopted && (poll.lastSuccess ?? 0) < adopted.at ? adopted.status : poll.data

  return useMemo(() => ({ status, answering, answer }), [status, answering, answer])
}
