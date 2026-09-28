"use client"

import { forwardRef, type Ref } from "react"
import {
  mdiSourceBranch,
  mdiSourceCommit,
  mdiSourceFork,
  mdiSourceMerge,
  mdiSourcePull,
  mdiSourceRepository,
} from "@mdi/js"
import type { Icon, IconProps } from "@/components/icons"

/**
 * The six glyphs git has and a general UI set does not.
 *
 * `icons.tsx` says it plainly: Heroicons "has no floppy disk, no git branch,
 * no sidebar", so `GitBranch` there is the share glyph, `GitCommit` is a hash
 * and `BranchPlus` is a grid of squares. On any other page that is the right
 * trade — one set, one weight, a near-enough drawing. On this one it is the
 * difference between a screen that manages repositories and a screen that
 * looks like it does: the branch fork, the commit dot on its line and the
 * merge arrows are the marks a reader already knows from every git client
 * they have used, and no amount of layout substitutes for them.
 *
 * So they come from Material Design Icons, which is already a dependency and
 * is already how the file browser draws what Heroicons cannot (`file-icon.tsx`
 * — this is the same adapter, deliberately, rather than a second idea about
 * what an MDI glyph is). Nothing else is imported from MDI here: a general
 * glyph that exists in both sets stays Heroicons, or the git pages would drift
 * into a second icon weight.
 */
function mdi(path: string, name: string): Icon {
  function MdiGlyph({ size = 16, title, ...props }: IconProps, ref: Ref<SVGSVGElement>) {
    return (
      <svg ref={ref} viewBox="0 0 24 24" fill="currentColor" width={size} height={size} {...props}>
        {title ? <title>{title}</title> : null}
        <path d={path} />
      </svg>
    )
  }
  const Forwarded = forwardRef<SVGSVGElement, Omit<IconProps, "ref">>(MdiGlyph)
  Forwarded.displayName = name
  return Forwarded
}

/** A branch: the fork every git UI draws beside a ref name. */
export const SourceBranch: Icon = mdi(mdiSourceBranch, "SourceBranch")
/** A commit: the dot on the line, for a sha or a point in history. */
export const SourceCommit: Icon = mdi(mdiSourceCommit, "SourceCommit")
/** A merge: two lines becoming one. */
export const SourceMerge: Icon = mdi(mdiSourceMerge, "SourceMerge")
/** A pull request. */
export const SourcePull: Icon = mdi(mdiSourcePull, "SourcePull")
/** A repository — a checkout on this host, not a folder. */
export const SourceRepository: Icon = mdi(mdiSourceRepository, "SourceRepository")
/** A fork: the topology view of every branch at once. */
export const SourceFork: Icon = mdi(mdiSourceFork, "SourceFork")
