"use client"

import Link from "next/link"
import { usePathname } from "next/navigation"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentRunSnapshot } from "@/lib/types"
import { Status } from "@/components/status-dot"
import { useProject } from "@/components/deploy/project-context"
import { runFailed } from "@/components/deploy/vocabulary"
import { causeHeadline, failureCause, fixTarget } from "@/components/deploy/failure-cause"

/**
 * The newest failed deployment's remedy, on the settings page that holds the
 * field it changes: "The last deployment failed: package-lock.json is out of
 * sync — Build with Bun". Only while that failure is still the news — newer
 * than the release that is live — and only on its own page, so the operator
 * who followed the run page's button lands on the field with the reason in
 * view, and one who came here another way learns what the last run needed.
 */
export function LastFailureRemedy() {
  const project = useProject()
  const pathname = usePathname()
  const failed = project.runs.find(
    (run) =>
      run.environmentId === project.environmentId &&
      runFailed(run.state) &&
      run.id > (project.liveRun?.id ?? 0),
  )
  const newest = project.runs.find((run) => run.environmentId === project.environmentId)
  const current = failed && newest?.id === failed.id ? failed : undefined
  const snapshot = usePoll(
    (signal) =>
      get<DeploymentRunSnapshot>(
        `/deploy/${project.projectId}/runs/${current?.id}`,
        undefined,
        signal,
      ),
    0,
    [project.projectId, current?.id],
    { enabled: current !== undefined },
  )
  const cause = snapshot.data && failureCause(snapshot.data.steps)?.cause
  const target = cause?.fix && fixTarget(project.projectId, cause.fix)
  if (!current || !cause || !target || target.href.split(/[?#]/)[0] !== pathname) return null
  return (
    <p className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-body">
      <Status
        tone="danger"
        label={`Deployment #${current.runNumber} failed: ${causeHeadline(cause)}`}
      />
      <span className="text-muted-foreground">{target.label} on this page.</span>
      <Link
        href={`/deploy/${project.projectId}/runs/${current.id}`}
        className="rounded-sm text-muted-foreground focus-ring hover:text-foreground hover:underline"
      >
        Open the deployment
      </Link>
    </p>
  )
}
