"use client"

import Link from "next/link"
import { Heart } from "@/components/icons"
import { cn } from "@/lib/utils"
import { useMediaQuery } from "@/hooks/use-mobile"
import type {
  DeploymentPendingChange,
  DeploymentPendingState,
  DeploymentSummary,
} from "@/lib/types"
import { Disclosure } from "@/components/form"
import { PROJECT_SETTINGS_NAV } from "@/components/nav"
import { ProductGlyph } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { useProject } from "@/components/deploy/project-context"
import { ChangeTag, PENDING_KIND_PAGE, sourceProduct } from "@/components/deploy/vocabulary"

const SHOWN = 6

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
 */
export function PendingChanges({
  pending,
  pageKinds = [],
}: {
  pending: DeploymentPendingState
  pageKinds?: string[]
}) {
  const project = useProject()
  const wide = useMediaQuery("(min-width: 640px)")
  if (!pending.pending) return null
  const { deployment } = project.detail
  const firstRelease = pending.changes.some((change) => change.kind === "deployment")
  const changes = pending.changes.filter((change) => change.kind !== "deployment")
  const shown = changes.slice(0, SHOWN)
  const count = changes.length
  const activeRun = deployment.activeRun
  const goingLive = activeRun && activeRun.planRevision >= pending.desiredRevision
  const changeList = (
    <ul className="flex w-full min-w-0 flex-row flex-wrap items-center gap-x-4 gap-y-1 sm:w-auto">
      {shown.map((change, index) => (
        <li key={`${change.kind}-${change.name}-${index}`} className="min-w-0">
          <PendingChangeItem
            change={change}
            deployment={deployment}
            base={`/deploy/${project.projectId}`}
            current={pageKinds.includes(change.kind)}
          />
        </li>
      ))}
      {count > SHOWN && (
        <li className="text-body text-muted-foreground">and {count - SHOWN} more</li>
      )}
    </ul>
  )

  return (
    <section
      aria-label="Changes not live yet"
      className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2 border-b border-hairline pb-4"
    >
      {goingLive ? (
        <Link
          href={`/deploy/${project.projectId}/runs/${activeRun.id}`}
          className="rounded-sm focus-ring hover:underline"
        >
          <Status
            tone="running"
            label={<TextShimmer>{`Going live in deployment #${activeRun.runNumber}`}</TextShimmer>}
          />
        </Link>
      ) : firstRelease ? (
        <Status tone="notice" label="Not deployed yet — the first deployment takes all of this" />
      ) : (
        <Status
          tone="warning"
          label={
            count === 0
              ? "Saved changes not live"
              : `${count} saved change${count === 1 ? "" : "s"} not live`
          }
        />
      )}
      {project.liveRelease && (
        <p className="min-w-0 text-body text-muted-foreground">
          <span className="numeric">Release #{project.liveRelease.number}</span> is live ·{" "}
          <span className="numeric">revision {pending.desiredRevision}</span> is saved
        </p>
      )}
      {count > 0 &&
        // On a phone the changes fold under the line that counts them: each
        // is a 36px row to press, and three of them put every settings
        // page's first field below the fold.
        (wide ? (
          changeList
        ) : (
          <Disclosure quiet summary="What will go live" className="w-full">
            {changeList}
          </Disclosure>
        ))}
    </section>
  )
}

/**
 * One change: the glyph of the page that holds it, its name, and whether it
 * was added, changed or removed — marked as git marks a file, as the release
 * comparison marks the same change once it ships. A source change is drawn as
 * the host it comes from, because that is what changed. A health check lives
 * on the Runtime page but is its own section there, so it links to that
 * section and draws a heart rather than the page's glyph.
 */
function PendingChangeItem({
  change,
  deployment,
  base,
  current,
}: {
  change: DeploymentPendingChange
  deployment: DeploymentSummary
  base: string
  current: boolean
}) {
  const place = PENDING_KIND_PAGE[change.kind]
  const entry = PROJECT_SETTINGS_NAV.find((item) => item.path === place?.split("#")[0])
  const product = change.kind === "source" ? sourceProduct(deployment) : undefined
  const Glyph = change.kind === "check" ? Heart : entry?.icon
  const body = (
    <>
      {product ? (
        <ProductGlyph id={product} />
      ) : (
        Glyph && <Glyph className="size-3.5 shrink-0 text-muted-foreground" />
      )}
      <span className={cn("min-w-0 truncate", change.kind === "variable" && "font-mono")}>
        {change.name}
      </span>
      <ChangeTag change={change.change} />
    </>
  )
  const className = "flex min-h-9 min-w-0 items-center gap-1.5 text-body sm:min-h-0"
  if (current || !place) {
    return (
      <span aria-current={current ? "page" : undefined} className={className}>
        {body}
      </span>
    )
  }
  return (
    <Link
      href={`${base}${place}`}
      className={cn(className, "rounded-sm focus-ring hover:underline")}
    >
      {body}
    </Link>
  )
}
