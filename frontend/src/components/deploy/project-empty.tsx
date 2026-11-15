"use client"

import Link from "next/link"
import { ArrowRight } from "@/components/icons"
import { cn } from "@/lib/utils"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { useProject } from "@/components/deploy/project-context"
import { ProjectMark } from "@/components/deploy/project-mark"

/**
 * A project page with nothing to show yet, drawn as Runtime draws a release
 * with no containers: the project as itself, one sentence, and the next step
 * as a button.
 *
 * Console, Players and a game's settings each said this as a grey notice with
 * the daemon's own words for a headline ("Docker runtime evidence could not be
 * loaded…"). The reason is still on the page — it is what an operator
 * searches for — but under a sentence that says what the page is waiting for,
 * in the product's words. The brand command that would fix most of these
 * (Start, Deploy) is already in the project's header, so the steps here are
 * the quiet ones: where to look.
 */
export function ProjectEmpty({
  title,
  reason,
  className,
  children,
}: {
  /** The one sentence: what this page is waiting for. */
  title: string
  /** The backend's own reason, when it gave one, under the sentence. */
  reason?: React.ReactNode
  className?: string
  /** The next steps, as `EmptyStep`s. */
  children?: React.ReactNode
}) {
  const project = useProject()
  return (
    <EmptyState
      mark={<ProjectMark deployment={project.detail.deployment} product={project.product} />}
      title={title}
      description={reason}
      action={
        children && (
          <div className="flex flex-wrap items-center justify-center gap-2">{children}</div>
        )
      }
      className={cn("border-0 py-10", className)}
    />
  )
}

/** One next step of a `ProjectEmpty`: a page to open. */
export function EmptyStep({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <Button size="sm" variant="outline" asChild>
      <Link href={href}>
        {children} <ArrowRight className="size-3.5" />
      </Link>
    </Button>
  )
}
