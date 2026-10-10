"use client"

import Link from "next/link"
import { Warning } from "@/components/icons"
import { get } from "@/lib/api"
import type { FailureDiagnosis } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { failureTone, wantsFailureReading } from "@/components/deploy/runtime-model"

/**
 * Why a service is not running, said on its card. Read from the same
 * diagnosis Docker's container page opens with, and only for a container that
 * is restarting or has exited: a running one has nothing to explain, and the
 * request is not made for it. A container somebody stopped on purpose exits
 * cleanly and is left as it is (`failureTone`).
 *
 * The state is a dependency, so a restart that lands re-reads the answer
 * rather than leaving the last crash's cause on a container that is up.
 * Without the diagnosis — Docker slow, the container just removed — the card
 * simply has no notice, which is what it had before.
 */
export function ServiceFailure({
  containerId,
  state,
  projectId,
}: {
  containerId: string
  state: string
  projectId: number
}) {
  const failing = wantsFailureReading(state)
  const failure = usePoll<FailureDiagnosis>(
    (signal) =>
      get<FailureDiagnosis>(
        `/docker/containers/${encodeURIComponent(containerId)}/failure`,
        undefined,
        signal,
      ),
    0,
    [containerId, state],
    { enabled: failing },
  ).data
  const tone = failing && failure ? failureTone(failure) : undefined
  if (!failure || !tone) return null
  const decisive = failure.evidence.filter((item) => item.weight === "decisive").slice(0, 3)

  return (
    <Notice title={failure.headline} icon={Warning} tone={tone} className="sm:ml-11">
      {failure.likely && (
        <p>
          {failure.likely}
          <span className="ml-1 text-hint text-muted-foreground">
            (
            {failure.confidence === "observed"
              ? "recorded by Docker"
              : "inferred from the evidence"}
            )
          </span>
        </p>
      )}
      {decisive.length > 0 && (
        <div className="mt-1.5 space-y-0.5">
          {decisive.map((item) => (
            <p key={item.label} className="text-hint">
              <span className="font-medium">{item.label}: </span>
              <span className="text-muted-foreground">{item.value}</span>
            </p>
          ))}
        </div>
      )}
      <Button size="xs" variant="outline" asChild className="mt-2">
        <Link href={`/deploy/${projectId}/logs?service=${containerId}`}>Read its logs</Link>
      </Button>
    </Notice>
  )
}
