"use client"

import Link from "next/link"
import {
  BranchPlus,
  ChatBubble,
  CheckCircle,
  Eye,
  GitTag,
  StarFill,
  Trash,
  UserPlus,
} from "@/components/icons"
import { relativeTime, timestamp } from "@/lib/format"
import { githubRepoHref } from "@/lib/github"
import type { GitHubEvent } from "@/lib/github-types"
import { cn } from "@/lib/utils"
import {
  IssueMark,
  SourceCommit,
  SourceFork,
  SourceMerge,
  SourcePull,
} from "@/components/git/glyphs"
import { eventWords } from "@/components/github/home/model"
import { Skeleton } from "@/components/ui/skeleton"
import { EmptyNote } from "@/components/state"

type Glyph = React.ComponentType<{ className?: string; style?: React.CSSProperties }>

/**
 * Each event's mark, in the colour GitHub gives the same thing: a merge is
 * violet, an opened request or issue green, a closed one red, a star amber.
 * What has no colour of its own (a push, a comment) is the muted ink.
 */
function markOf(event: GitHubEvent): { glyph: Glyph; colour?: string } {
  const action = event.action ?? ""
  switch (event.kind) {
    case "push":
      return { glyph: SourceCommit }
    case "pull":
      if (action === "merged") return { glyph: SourceMerge, colour: "var(--pull-merged)" }
      if (action === "closed") return { glyph: SourcePull, colour: "var(--pull-closed)" }
      return { glyph: SourcePull, colour: "var(--pull-open)" }
    case "issue":
      return {
        glyph: IssueMark,
        colour: action === "closed" ? "var(--pull-merged)" : "var(--pull-open)",
      }
    case "comment":
      return { glyph: ChatBubble }
    case "review":
      return action === "approved"
        ? { glyph: CheckCircle, colour: "var(--pull-open)" }
        : { glyph: ChatBubble }
    case "create":
      return event.refType === "tag" ? { glyph: GitTag } : { glyph: BranchPlus }
    case "delete":
      return { glyph: Trash, colour: "var(--pull-closed)" }
    case "star":
      return { glyph: StarFill, colour: "var(--tag-amber)" }
    case "fork":
      return { glyph: SourceFork }
    case "release":
      return { glyph: GitTag, colour: "var(--pull-open)" }
    case "public":
      return { glyph: Eye }
    case "member":
      return { glyph: UserPlus }
  }
}

/** One event's line, a fixed height whatever it says (§11): two lines on a phone, one on a desk. */
const LINE = "flex h-12 min-w-0 items-center gap-3 sm:h-10"

/** The repository an event happened in: its page here when the page lists it, else GitHub's. */
function RepositoryLink({
  repository,
  here,
  connection,
  className,
}: {
  repository: string
  here: boolean
  connection: number
  className?: string
}) {
  const look = cn(
    "min-w-0 shrink truncate rounded-sm font-mono text-xs text-foreground focus-ring hover:underline",
    className,
  )
  return here ? (
    <Link href={githubRepoHref(repository, { account: connection })} className={look}>
      {repository}
    </Link>
  ) : (
    <a
      href={`https://github.com/${repository}`}
      target="_blank"
      rel="noopener noreferrer"
      className={look}
    >
      {repository}
    </a>
  )
}

/**
 * What the account did lately in public, as GitHub's feed says it (the App
 * asks for no account permission, so GitHub hands over public events only):
 * one line an event,
 * newest first — its mark in GitHub's colour, the sentence with its object
 * (a branch, a request, a release) set apart and linked to GitHub, the
 * repository, which opens here when the page lists it, and how long ago.
 */
export function ActivityFeed({
  events,
  loading,
  listed,
  connection,
  count = 10,
}: {
  events?: GitHubEvent[]
  loading: boolean
  listed: Set<string>
  connection: number
  count?: number
}) {
  if (!events) {
    return loading ? (
      <ul aria-hidden className="min-w-0">
        {Array.from({ length: 5 }, (_, index) => (
          <li key={index} className={LINE}>
            <Skeleton className="size-7 rounded-md" />
            <Skeleton className="h-4 w-2/3" />
          </li>
        ))}
      </ul>
    ) : (
      <EmptyNote>GitHub did not share the activity.</EmptyNote>
    )
  }
  if (events.length === 0) return <EmptyNote>Nothing public done lately.</EmptyNote>
  return (
    <ol aria-label="Recent activity" data-slot="github-activity" className="min-w-0">
      {events.slice(0, count).map((event) => {
        const { glyph: Mark, colour } = markOf(event)
        const { verb, object } = eventWords(event)
        const here = listed.has(event.repository.toLowerCase())
        const mono = event.kind === "push" || event.kind === "create" || event.kind === "delete"
        return (
          <li key={event.id} className={LINE} data-kind={event.kind}>
            <span className="grid size-7 shrink-0 place-items-center rounded-md bg-surface-sunken">
              <Mark
                aria-hidden
                className={cn("size-3.5", !colour && "text-muted-foreground")}
                style={colour ? { color: colour } : undefined}
              />
            </span>
            <div className="min-w-0 flex-1">
              <p className="flex min-w-0 items-baseline gap-1 text-body">
                <span className="shrink-0 text-muted-foreground">{verb}</span>{" "}
                {object &&
                  (event.htmlUrl ? (
                    <a
                      href={event.htmlUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                      className={cn(
                        "min-w-0 truncate rounded-sm font-medium focus-ring hover:underline",
                        mono && "font-mono text-xs",
                      )}
                    >
                      {object}
                    </a>
                  ) : (
                    <span
                      className={cn("min-w-0 truncate font-medium", mono && "font-mono text-xs")}
                    >
                      {object}
                    </span>
                  ))}
                {/* Where it happened ends the line on a desk; a phone gives it
                    a line of its own under the sentence, with the time. */}
                {object && (
                  <span className="shrink-0 text-muted-foreground max-sm:hidden"> in</span>
                )}{" "}
                <RepositoryLink
                  repository={event.repository}
                  here={here}
                  connection={connection}
                  className="max-w-[45%] max-sm:hidden"
                />
              </p>
              <p className="flex min-w-0 items-baseline gap-1.5 text-hint text-muted-foreground sm:hidden">
                <RepositoryLink repository={event.repository} here={here} connection={connection} />{" "}
                <span aria-hidden>·</span>{" "}
                <time dateTime={event.createdAt} className="numeric shrink-0">
                  {relativeTime(event.createdAt)}
                </time>
              </p>
            </div>
            <time
              dateTime={event.createdAt}
              title={timestamp(event.createdAt)}
              className="numeric shrink-0 text-hint text-muted-foreground max-sm:hidden"
            >
              {relativeTime(event.createdAt)}
            </time>
          </li>
        )
      })}
    </ol>
  )
}
