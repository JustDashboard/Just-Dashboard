"use client"

import { useState } from "react"
import { get } from "@/lib/api"
import { dayAt, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { AvailabilityDay, DeploymentAvailability } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { InfoTip } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { WireMark, WireNode } from "@/components/deploy/wire"
import {
  WINDOW_SHORT,
  availabilityLead,
  availabilityReading,
  dayTone,
  dayWords,
} from "@/components/deploy/availability-model"

/** A stretch's fill, in the outcome strip's hues: nobody listening is the track. */
const DAY_FILL = {
  unknown: "bg-meter-track",
  up: "bg-success/80",
  dented: "bg-warning",
  down: "bg-destructive",
} as const

const FIGURE_TONE = {
  default: "text-success",
  warning: "text-warning",
  danger: "text-destructive",
} as const

/** "Oct 31", the day a stretch ends on. The last one is the 24-hour window. */
function dayName(day: AvailabilityDay, last: boolean) {
  if (last) return "Last 24 hours"
  return dayAt(new Date(day.from).getTime() + 86400000)
}

/**
 * How much of the time the deployment was up, read from what the dashboard
 * already keeps rather than from a probe. Each release's activation and
 * retirement say when the environment had something live; the container events
 * say when, inside that, it was not. A sentence on Docker's mark, since its
 * events are the evidence; then the ribbon a status page draws, thirty days
 * with the downtime on the day it happened and nothing claimed for days the
 * record does not reach. Pointing at a day puts it on the one line under the
 * ribbon, which otherwise holds the three windows, so the block is one height
 * whichever it says.
 */
export function Availability({
  projectId,
  environmentId,
}: {
  projectId: number
  /** Staging's figure in place of production's. */
  environmentId?: number
}) {
  const poll = usePoll(
    (signal) =>
      get<DeploymentAvailability>(
        `/deploy/${projectId}/availability`,
        { environment: environmentId },
        signal,
      ),
    60000,
    [projectId, environmentId],
  )
  const [pointed, setPointed] = useState<number>()
  const data = poll.data
  const days = data?.days ?? []
  const shown = pointed !== undefined ? days[pointed] : undefined
  const lead = data?.status === "available" ? availabilityLead(data) : undefined
  return (
    <Panel plain>
      <PanelHeader
        title="Availability"
        actions={
          <>
            {data?.recordSince && (
              <span
                className="text-hint text-muted-foreground max-sm:hidden"
                title="The oldest container event the dashboard keeps; the record begins here"
              >
                record since <span className="numeric">{timestamp(data.recordSince)}</span>
              </span>
            )}
            <InfoTip label="How availability is worked out">
              The share of the time the live release was running and healthy, from each
              release&apos;s activation and retirement and the container events the dashboard keeps:
              an exit, an OOM kill, a pause or a failing health check counts as down until the
              container comes back, and so does a stop. Time before the record began is not counted
              either way.
            </InfoTip>
          </>
        }
      />
      <PanelBody className="space-y-4 pt-4">
        {poll.error && !data && <ErrorState error={poll.error} onRetry={poll.refresh} />}
        {poll.loading && !data && <LoadingRows rows={1} />}
        {data?.status === "unavailable" && <EmptyNote>{data.reason}</EmptyNote>}
        {lead && data?.status === "available" && (
          <>
            <WireNode
              mark={
                <span className="relative flex">
                  <WireMark tone="logo" size="md">
                    <ProductGlyph id="docker" />
                  </WireMark>
                  <span className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background">
                    <StatusDot tone={lead.tone} />
                  </span>
                </span>
              }
              title={lead.title}
              hint={lead.hint}
            />
            {days.length > 0 && (
              <div className="min-w-0" onPointerLeave={() => setPointed(undefined)}>
                <ol
                  aria-label={`Availability over the last ${days.length} days`}
                  data-testid="availability-days"
                  className="flex h-6 animate-rise items-stretch gap-0.5"
                >
                  {days.map((day, index) => (
                    // eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-noninteractive-element-interactions -- a thumb's way to point at a day, as onPointerEnter is a mouse's; the day's words are its title as well
                    <li
                      key={day.from}
                      title={`${dayName(day, index === days.length - 1)}: ${dayWords(day)}`}
                      data-tone={dayTone(day)}
                      onPointerEnter={() => setPointed(index)}
                      onClick={() => setPointed(index)}
                      className={cn(
                        "min-w-0 flex-1 rounded-sm [transition:opacity_150ms_ease-out]",
                        DAY_FILL[dayTone(day)],
                        pointed !== undefined && pointed !== index && "opacity-40",
                      )}
                    />
                  ))}
                </ol>
                <div className="mt-2 flex min-h-5 min-w-0 flex-wrap items-baseline justify-between gap-x-4 text-hint text-muted-foreground">
                  {shown ? (
                    <p key={shown.from} className="numeric animate-rise">
                      <span className="font-medium text-foreground">
                        {dayName(shown, pointed === days.length - 1)}
                      </span>{" "}
                      · {dayWords(shown)}
                    </p>
                  ) : (
                    <dl className="numeric flex min-w-0 flex-wrap gap-x-4">
                      {data.windows.map((window) => {
                        const reading = availabilityReading(window, data.down)
                        return (
                          <div key={window.window} title={reading.hint} className="flex gap-1.5">
                            <dt>{WINDOW_SHORT[window.window] ?? window.window}</dt>
                            <dd
                              className={cn(
                                "font-medium",
                                reading.value === "—"
                                  ? "text-muted-foreground"
                                  : FIGURE_TONE[reading.tone as keyof typeof FIGURE_TONE],
                              )}
                            >
                              {reading.value}
                            </dd>
                          </div>
                        )
                      })}
                    </dl>
                  )}
                  <span className="numeric text-micro max-sm:hidden">
                    {days.length} days ago to today
                  </span>
                </div>
              </div>
            )}
          </>
        )}
      </PanelBody>
    </Panel>
  )
}
