"use client"

import { Clock, GitBranch, GitPullRequest, Key, Link, Servers, type Icon } from "@/components/icons"
import { plural, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { DeploymentEngineRun } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { InitialsMark, UserAvatar } from "@/components/account/user-avatar"
import { OutcomeStrip, type OutcomeTone } from "@/components/outcome-strip"
import { ProductLogo, gitProviderProduct, hostProduct } from "@/components/product-logo"
import {
  RUN_LABELS,
  humanize,
  isActiveRun,
  runActor,
  runTitle,
  type RunActor,
} from "@/components/deploy/vocabulary"

type Starter = Pick<DeploymentEngineRun, "trigger" | "actor" | "metadata">

/**
 * The product a run's starter is drawn as, or nothing for a person (drawn as
 * their face) and for a starter no product names (drawn as its glyph).
 *
 * `runActor` says *what kind* of thing started the run, which is all the
 * words need. The mark can say which one: a push the branch watcher noticed
 * happened on the project's remote, so it is GitHub's when the remote is on
 * GitHub, and git's when the host says nothing; a pull request's preview
 * arrived through the webhook of the forge that holds the repository; a plain
 * webhook is the webhook's own mark, which misattributes nothing.
 */
export function runActorProduct(run: Starter, remote?: string): string | undefined {
  const actor = runActor(run)
  switch (actor.kind) {
    case "push":
      return hostProduct(remote) ?? actor.product
    case "provider":
      return actor.product
    case "preview":
      return gitProviderProduct(hostProduct(remote))
    case "hook":
      return "webhook"
    default:
      return undefined
  }
}

/** The glyph on the tile when no product names the starter, or its file fails. */
const GLYPHS: Record<Exclude<RunActor["kind"], "person">, Icon> = {
  push: GitBranch,
  provider: GitBranch,
  schedule: Clock,
  api: Key,
  hook: Link,
  preview: GitPullRequest,
  system: Servers,
}

/**
 * Who or what started a run, as a mark: a person as their face — your own
 * picture when it was you, otherwise their initials in the hue they have in
 * the rail and the users list — and anything else as the product it is on
 * `ProductLogo`'s tile, or its glyph on the same tile. Every run in a list
 * then has a mark of one size in one place, and "the one the nightly
 * schedule started" is found without reading.
 *
 * `remote` is the project's source remote (`DeploymentSummary.sourceRemote`),
 * which names the host a push or a preview came from. `sm` is `ProductLogo`'s
 * small tile, a row's leading mark; `xs` is the twenty-pixel tile a mark takes
 * inside a line of facts.
 */
export function RunActorMark({
  run,
  remote,
  size = "sm",
  className,
}: {
  run: Starter
  remote?: string
  size?: "xs" | "sm"
  className?: string
}) {
  const { status } = useAuth()
  const actor = runActor(run)
  if (actor.kind === "person") {
    // A face takes the tile's box, so a column of runs started by people and
    // by machines keeps one edge.
    const box = cn(size === "sm" ? "size-8 text-xs" : "size-5", className)
    const me = status?.user?.username === actor.name ? status.user : undefined
    return me ? (
      <UserAvatar user={me} scope="self" size={size === "sm" ? "sm" : "xs"} className={box} />
    ) : (
      <InitialsMark name={actor.name} size={size === "sm" ? "sm" : "xs"} className={box} />
    )
  }
  return (
    <ProductLogo
      id={runActorProduct(run, remote)}
      size="sm"
      fallback={GLYPHS[actor.kind]}
      className={cn(
        size === "xs" && "size-5 rounded-sm [&_img]:size-3.5 [&_svg]:size-3",
        className,
      )}
    />
  )
}

type StripRun = Pick<
  DeploymentEngineRun,
  "id" | "runNumber" | "state" | "operation" | "requestedAt" | "endedAt"
>

/** How a run ended, in the outcome strip's hues; a run still going is the brand's "now". */
function outcomeTone(state: string): OutcomeTone {
  if (state === "succeeded") return "success"
  if (state === "failed" || state === "failed_activation") return "danger"
  if (state === "rolled_back") return "warning"
  if (isActiveRun(state)) return "running"
  return "muted"
}

/**
 * A project's last runs as the shared outcome strip. Takes them newest first —
 * the order the run list and `DeploymentSummary.recentRuns` arrive in — and
 * draws them oldest first, reading left to right into the present. Slice
 * before passing: the strip draws every run it is given.
 */
export function RunStrip({ runs }: { runs: StripRun[] }) {
  const ordered = [...runs].reverse()
  const items = ordered.map((run) => ({
    key: String(run.id),
    tone: outcomeTone(run.state),
    title: `${runTitle(run)} · ${RUN_LABELS[run.state] ?? humanize(run.state)} · ${relativeTime(run.endedAt ?? run.requestedAt)}`,
  }))
  const failed = items.filter((item) => item.tone === "danger").length
  return (
    <OutcomeStrip label={`Last ${plural(items.length, "run")}: ${failed} failed`} items={items} />
  )
}
