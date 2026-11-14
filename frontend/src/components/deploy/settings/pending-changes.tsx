"use client"

import Link from "next/link"
import { Heart } from "@/components/icons"
import type {
  DeploymentPendingChange,
  DeploymentPendingState,
  DeploymentSummary,
} from "@/lib/types"
import { PROJECT_SETTINGS_NAV } from "@/components/nav"
import { ProductGlyph } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { useProject } from "@/components/deploy/project-context"
import { PENDING_KIND_PAGE, sourceProduct } from "@/components/deploy/vocabulary"
import { PendingStrip, type PendingItem } from "@/components/deploy/settings/pending-strip"

/**
 * Saved changes the live release does not have yet, named, each one a way
 * to the page that holds it. Drawn once at the top of a settings page and
 * nowhere else: a dot on the Settings nav already says *that* something is
 * pending, and a warning on every card said it six times.
 *
 * It is a strip, not a notice, and it has no button. It was an amber box
 * with its own brand "Deploy changes" directly under the project header's
 * "Deploy changes", so every settings page carried two faces of one command
 * a hundred and fifty pixels apart (§3, §16: one command per surface). The
 * header's is the one; this says what that command will take live, and once
 * a deployment carrying these changes is running it says so, lit, and links
 * to it.
 *
 * `pageKinds` names the kinds the current page edits, so a change already on
 * screen is named rather than offered as a link back to where the reader is.
 *
 * It feeds a deployment's configuration to `PendingStrip`, which draws it and
 * takes any settings form's pending items.
 */
export function PendingChanges({
  pending,
  pageKinds = [],
}: {
  pending: DeploymentPendingState
  pageKinds?: string[]
}) {
  const project = useProject()
  if (!pending.pending) return null
  const { deployment } = project.detail
  const base = `/deploy/${project.projectId}`
  const firstRelease = pending.changes.some((change) => change.kind === "deployment")
  const changes = pending.changes.filter((change) => change.kind !== "deployment")
  const count = changes.length
  const activeRun = deployment.activeRun
  const goingLive = activeRun && activeRun.planRevision >= pending.desiredRevision

  return (
    <PendingStrip
      label="Changes not live yet"
      fold="What will go live"
      headline={
        goingLive ? (
          <Link
            href={`${base}/runs/${activeRun.id}`}
            className="flex rounded-sm focus-ring hover:underline"
          >
            <Status
              tone="running"
              label={
                <TextShimmer>{`Going live in deployment #${activeRun.runNumber}`}</TextShimmer>
              }
            />
          </Link>
        ) : firstRelease ? (
          <Status tone="notice" label="Not deployed yet — the first deployment takes all of this" />
        ) : (
          <Status
            tone="warning"
            label={`${count} saved change${count === 1 ? "" : "s"} not live`}
          />
        )
      }
      detail={
        project.liveRelease && (
          <>
            <span className="numeric">Release #{project.liveRelease.number}</span> is live ·{" "}
            <span className="numeric">revision {pending.desiredRevision}</span> is saved
          </>
        )
      }
      items={changes.map((change) => pendingItem(change, deployment, base, pageKinds))}
    />
  )
}

/**
 * One saved change as the strip names it: the glyph of the page that holds
 * it, its name, and whether it was added, changed or removed. A source change
 * is drawn as the host it comes from, because that is what changed. A health
 * check lives on the Runtime page but is its own section there, so it links
 * to that section and draws a heart rather than the page's glyph.
 */
function pendingItem(
  change: DeploymentPendingChange,
  deployment: DeploymentSummary,
  base: string,
  pageKinds: string[],
): PendingItem {
  const place = PENDING_KIND_PAGE[change.kind]
  const entry = PROJECT_SETTINGS_NAV.find((item) => item.path === place?.split("#")[0])
  const product = change.kind === "source" ? sourceProduct(deployment) : undefined
  const Glyph = change.kind === "check" ? Heart : entry?.icon
  return {
    mark: product ? (
      <ProductGlyph id={product} />
    ) : (
      Glyph && <Glyph className="size-3.5 shrink-0 text-muted-foreground" />
    ),
    name: change.name,
    mono: change.kind === "variable",
    state: change.change,
    href: place ? `${base}${place}` : undefined,
    current: pageKinds.includes(change.kind),
  }
}
