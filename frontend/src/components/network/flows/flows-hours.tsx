"use client"

import { useState } from "react"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Skeleton } from "@/components/ui/skeleton"
import { cn } from "@/lib/utils"
import type { FlowCoverageHour } from "@/lib/network-flows"
import { ObserverQualityReading } from "@/components/network/kernel-observer"
import { UTCTime, dayWords } from "@/components/network/flows/marks"

const troubled = (hour: FlowCoverageHour) =>
  hour.failedSources + hour.truncatedSources + hour.omittedSources + hour.discardedIntervals > 0

/**
 * The twenty-four hours of the day being read, a block each: recorded,
 * recorded with a source that failed, was capped or was left out, or not
 * recorded. The line under the strip reads the chosen hour, at one height
 * whichever it is. An hour with no block was not sampled, which says nothing
 * about what happened in it.
 */
export function FlowsHours({
  day,
  hours,
  loading,
}: {
  day: string
  hours: FlowCoverageHour[]
  loading: boolean
}) {
  const byHour = new Map(hours.map((hour) => [new Date(hour.hour).getUTCHours(), hour]))
  const latest = hours.length ? Math.max(...byHour.keys()) : 0
  const [picked, setPicked] = useState<{ day: string; hour: number }>()
  const chosen = picked?.day === day ? picked.hour : latest
  const reading = byHour.get(chosen)
  const stamp = `${day}T${String(chosen).padStart(2, "0")}:00:00Z`
  return (
    <Panel plain id="flow-hours" aria-label="Recorded hours" className="scroll-mt-6">
      <PanelHeader
        title="Recorded hours"
        actions={
          !loading && (
            <span className="numeric text-hint text-muted-foreground">
              {hours.length} of 24 · {dayWords(day)} UTC
            </span>
          )
        }
      />
      <PanelBody className="space-y-2">
        {loading ? (
          <Skeleton className="h-7 w-full rounded-sm max-sm:h-[3.75rem]" />
        ) : (
          <ol
            aria-label={`Hours of ${dayWords(day)}, UTC`}
            className="grid grid-cols-12 gap-1 sm:grid-cols-[repeat(24,minmax(0,1fr))]"
          >
            {Array.from({ length: 24 }, (_, hour) => {
              const recorded = byHour.get(hour)
              const label = `${String(hour).padStart(2, "0")}:00`
              return (
                <li key={hour}>
                  <button
                    type="button"
                    aria-pressed={chosen === hour}
                    aria-label={
                      recorded
                        ? `${label} UTC, ${recorded.samples} samples${troubled(recorded) ? ", incomplete" : ""}`
                        : `${label} UTC, not recorded`
                    }
                    title={`${label} UTC`}
                    onClick={() => setPicked({ day, hour })}
                    className={cn(
                      "block h-7 w-full rounded-sm focus-ring transition-opacity hover:opacity-80",
                      !recorded && "bg-meter-track",
                      recorded && !troubled(recorded) && "bg-success/70",
                      recorded && troubled(recorded) && "bg-warning/80",
                      chosen === hour && "outline-2 outline-offset-2 outline-foreground/60",
                    )}
                  />
                </li>
              )
            })}
          </ol>
        )}
        <div
          data-slot="flow-hour-reading"
          className="flex h-8 min-w-0 items-center gap-x-4 text-hint text-muted-foreground"
        >
          {loading ? (
            <Skeleton className="h-3 w-72" />
          ) : hours.length === 0 ? (
            <span className="truncate">
              No retained samples for this period. Recorder downtime and missing hours remain
              unknown.
            </span>
          ) : (
            <>
              <span className="shrink-0 font-mono text-foreground">
                <UTCTime value={stamp} form="short" />
              </span>
              {reading ? (
                <>
                  <span className="numeric min-w-0 truncate">
                    {reading.samples} samples · {reading.failedSources} failed source reads ·{" "}
                    {reading.truncatedSources} capped · {reading.omittedSources} omitted sources ·{" "}
                    {reading.discardedIntervals} skipped intervals
                  </span>
                  <Popover>
                    <PopoverTrigger asChild>
                      <Button size="xs" variant="ghost" className="ml-auto shrink-0">
                        Retained observer gaps
                      </Button>
                    </PopoverTrigger>
                    <PopoverContent align="end" className="w-[min(34rem,calc(100vw-2rem))]">
                      <ObserverQualityReading quality={reading.observerQuality} />
                    </PopoverContent>
                  </Popover>
                </>
              ) : (
                <span className="truncate">not recorded; what happened in it remains unknown</span>
              )}
            </>
          )}
        </div>
      </PanelBody>
    </Panel>
  )
}
