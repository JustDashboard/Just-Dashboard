"use client"

import { useEffect, useRef, useState } from "react"
import { duration } from "@/lib/format"
import { Notice } from "@/components/state"
import { BorderBeam } from "@/components/ui/border-beam"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { POWER, type PowerChange } from "@/components/database/home/power"
import { usePowerChange } from "@/components/database/home/verbs"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The place under a home's identity line where the server's state is said
 * when it has to be: that it is being started, stopped or restarted, or — on
 * a home that is not answering — why not, and what to do about it.
 *
 * It is one live region, always in the page, so what appears in it is
 * announced: pressing Start used to swap one notice for another in silence.
 * It also takes the keyboard when a change begins and the control that began
 * it is gone — the notice's own Start is replaced by "Starting…" — so focus
 * lands on what the press did rather than on the top of the window.
 *
 * A change in flight is the reason for whatever a reading taken meanwhile
 * found, so it stands in place of `children`: a server being restarted reads
 * "unreachable" for a moment, and that is not a fault to report.
 */
export function StateRegion({ children }: { children?: React.ReactNode }) {
  const { id } = useDatabase()
  const change = usePowerChange(id)
  const region = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (change && document.activeElement === document.body) region.current?.focus()
  }, [change])

  return (
    <div
      ref={region}
      role="status"
      tabIndex={-1}
      data-slot="database-state"
      // Empty, it must not cost the page a gap: the margin takes back the one
      // the frame puts before every block.
      className="min-w-0 rounded-lg focus-ring empty:-mt-6 md:empty:-mt-8"
    >
      {change ? <ChangeNotice change={change} /> : children}
    </div>
  )
}

/**
 * A change of power in flight (§11): the participle, a beam round the notice,
 * what is happening to the server in one sentence, and how long it has been.
 * A stop is given ninety seconds by Docker, so the count is what tells a
 * reader the page has not hung.
 */
function ChangeNotice({ change }: { change: PowerChange }) {
  const { summary, status } = useDatabase()
  const container = summary?.container
  const unit = summary?.unit
  const where = container
    ? `Its container ${container.name}`
    : unit
      ? `Its unit ${unit.name}`
      : "The server"
  const up = status.state === "running"
  return (
    <Notice
      title={
        <span data-slot="database-changing" className="flex">
          <TextShimmer>{POWER[change.action].progressive}</TextShimmer>
        </span>
      }
      className="relative animate-rise"
    >
      <span aria-hidden className="pointer-events-none absolute -inset-px rounded-lg">
        <BorderBeam size={80} duration={4} />
      </span>
      <p>
        {change.action === "stop"
          ? `${where} is being given time to shut down cleanly; every session on it is disconnected.`
          : change.action === "restart" && up
            ? `${where} is going down and coming back; every session on it is disconnected meanwhile.`
            : `${where} is coming up. This page fills in once the engine accepts connections.`}{" "}
        <Elapsed since={change.since} />
      </p>
    </Notice>
  )
}

/**
 * How long the change has been going, counted by the second. Hidden from the
 * live region around it: a figure that changes every second would be read
 * out every second.
 */
function Elapsed({ since }: { since: number }) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [])
  return (
    <span aria-hidden className="numeric whitespace-nowrap">
      {duration(Math.max(0, (now - since) / 1000))} so far.
    </span>
  )
}
