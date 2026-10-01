"use client"

import { useRef } from "react"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { pushSample, type Sample } from "@/components/database/home/samples"

const NONE: readonly Sample[] = []

/** How often a home asks for the snapshot. The server keeps nothing between two asks. */
export const SAMPLE_EVERY_MS = 5_000

/**
 * A database's statistics, read every five seconds and kept: the newest
 * answer, and the run of samples every rate, trend and chart on the home is
 * derived from. A poll that fails leaves both as they were, with the error
 * beside them — the tiles keep their last figures and say they have stopped.
 *
 * `enabled` is whether the server is there to ask: a stopped one is not
 * dialled, here any more than by the summary.
 */
export function useSamples<T>(
  id: number,
  toSample: (answer: T) => Sample | undefined,
  enabled: boolean,
) {
  const held = useRef<readonly Sample[]>(NONE)
  const poll = usePoll(
    async (signal) => {
      const answer = await get<T>(`/databases/${id}/stats`, undefined, signal)
      const sample = toSample(answer)
      // An answer with no clock in it is not this route's: there is nothing
      // to divide a counter by, and nothing a tile could honestly show.
      if (!sample) throw new Error("The server answered in a form this page does not read.")
      held.current = pushSample(held.current, sample)
      return { answer, samples: held.current }
    },
    SAMPLE_EVERY_MS,
    [id],
    { enabled },
  )
  return {
    answer: poll.data?.answer,
    samples: poll.data?.samples ?? NONE,
    /** Nothing has answered yet. */
    loading: poll.loading,
    error: poll.error,
    refresh: poll.refresh,
  }
}
