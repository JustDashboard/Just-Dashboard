"use client"

import Link from "next/link"
import {
  CloudDownload,
  Copy,
  External,
  GitBranch,
  LockClosed,
  Servers,
  Star,
} from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { plural, relativeTime, timestamp } from "@/lib/format"
import { githubRepoHref, localGitHref } from "@/lib/github"
import type { GitHubRepository } from "@/lib/github-types"
import type { GitRepo } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useChoicePress } from "@/components/choice-row"
import { DimActions } from "@/components/icon-action"
import { IssueMark, SourceFork } from "@/components/git/glyphs"
import { RepoMark } from "@/components/git/languages"
import { ForgeFace } from "@/components/git/marks"
import { LabelTag } from "@/components/github/repo/shared"
import { rowTags } from "@/components/github/list-model"
import { LanguageMark } from "@/components/language-icon"
import { Tag } from "@/components/tag"
import { BlurFade } from "@/components/ui/blur-fade"
import { Skeleton } from "@/components/ui/skeleton"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { VerbMenu, type VerbSpec } from "@/components/verbs"

/**
 * A card's height, which every card keeps whatever it says, and its
 * placeholder takes too, so a card lands where its ghost stood (§11).
 */
export const REPO_CARD_HEIGHT = "h-40"

/**
 * The shelf the cards stand on: as many columns as the width holds at twenty
 * rems each — two beside the Overview's profile column on a laptop, three
 * across the Repositories view, one on a phone, where `min(…, 100%)` gives a
 * screen narrower than a card a column rather than a sideways scroll.
 */
export const REPO_CARD_GRID =
  "grid min-w-0 gap-3 grid-cols-[repeat(auto-fill,minmax(min(20rem,100%),1fr))]"

/**
 * One repository, as a card on a shelf — drawn the way GitHub draws a
 * repository on a profile, because that is the picture of one the reader
 * already has, and the way Local Git draws a checkout, so the two pages of
 * one repository look alike.
 *
 * Top to bottom: **what it is** — the logo of its language on the tile, its
 * name, and whose it is, as the owner's face and `owner/name`, with what
 * kind it is (private, a fork, a template, shared with you); **what it is
 * for** — its description in two lines, or its topics, which say the same
 * thing in fewer words; and on the floor **what it is made of and how it
 * moves** — the language by its logo, stars, forks and open issues, a mark
 * when a checkout of it is on this server, and when it was last pushed.
 *
 * Every card is one shape, so a row of them is even (D11). There is no arrow
 * (the operator's word on the Databases and the fleet's cards): the card and
 * its name open the repository's page; its menu holds the rest as words.
 */
export function RepoCard({
  repo,
  connection,
  checkouts,
  control,
  onClone,
  index = 0,
}: {
  repo: GitHubRepository
  connection: number
  checkouts: GitRepo[]
  control: boolean
  onClone: (repo: GitHubRepository) => void
  /** Position on the shelf, for the arrival stagger (capped). */
  index?: number
}) {
  const href = githubRepoHref(repo.fullName, { account: connection })
  const press = useChoicePress({ href })
  const checkout = checkouts[0]
  const pushed = repo.pushedAt ?? repo.updatedAt
  const tags = rowTags(repo)
  const topics = repo.topics ?? []
  const verbs: VerbSpec[] = [
    {
      key: "github",
      label: "Open on GitHub",
      icon: External,
      run: () => window.open(repo.htmlUrl, "_blank", "noopener"),
    },
    ...(control
      ? [{ key: "clone", label: "Clone to server…", icon: CloudDownload, run: () => onClone(repo) }]
      : []),
    ...(checkout
      ? [
          {
            key: "local",
            label: "Open in Local Git",
            icon: GitBranch,
            href: localGitHref(checkout.path),
            internal: true,
          },
        ]
      : []),
    {
      key: "copy",
      label: "Copy clone URL",
      icon: Copy,
      run: () => void copyText(repo.cloneUrl, "Clone URL copied"),
    },
  ]

  return (
    <li className={cn(REPO_CARD_HEIGHT, "min-w-0")} data-slot="github-repo-card">
      <BlurFade delay={Math.min(index, 11) * 0.035} className="h-full">
        <SpotlightBorder className="h-full">
          {/* eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions -- the pointer's larger target: the card's name is the link a keyboard opens it by */}
          <div
            onClick={press.onClick}
            className="group group/choice flex h-full min-w-0 cursor-pointer flex-col rounded-xl"
          >
            <div className="flex min-w-0 flex-col gap-3 p-3.5 pb-0">
              <div className="grid h-10 min-w-0 grid-cols-start-main-end items-center gap-x-3 gap-y-0.5">
                <RepoMark
                  repo={{ languages: repo.language ? [{ name: repo.language, share: 1 }] : [] }}
                  className="row-span-2"
                />
                <span className="flex min-w-0 items-center gap-2 self-end">
                  <Link
                    href={href}
                    aria-label={`Open ${repo.fullName}`}
                    data-workspace-item={repo.fullName}
                    data-workspace-name={repo.fullName}
                    className="block min-w-0 truncate rounded-sm text-title leading-tight font-semibold tracking-tight focus-ring"
                  >
                    {repo.name}
                  </Link>
                  {repo.visibility !== "public" && (
                    <Tag icon={LockClosed} className="shrink-0">
                      {repo.visibility === "internal" ? "Internal" : "Private"}
                    </Tag>
                  )}
                </span>
                <DimActions className="-my-2 -mr-1.5 self-end">
                  <VerbMenu glyphs={false} verbs={verbs} label={`Actions for ${repo.fullName}`} />
                </DimActions>
                <span
                  className="col-span-2 col-start-2 flex min-w-0 items-center gap-1.5 self-start text-hint text-muted-foreground"
                  title={repo.fullName}
                >
                  <ForgeFace login={repo.owner.login} provider="github" size="xs" />
                  <span className="min-w-0 truncate font-mono">{repo.owner.login}</span>
                  {tags.map((tag) => (
                    <Tag
                      key={tag}
                      tone={tag === "Archived" ? "warning" : "default"}
                      className="shrink-0"
                    >
                      {tag}
                    </Tag>
                  ))}
                </span>
              </div>
              <div className="h-10 min-w-0">
                {repo.description ? (
                  <p
                    title={repo.description}
                    className="line-clamp-2 text-body leading-5 text-muted-foreground"
                  >
                    {repo.description}
                  </p>
                ) : topics.length > 0 ? (
                  <p className="flex h-5 min-w-0 gap-1.5 overflow-hidden" aria-label="Topics">
                    {topics.slice(0, 4).map((topic) => (
                      <LabelTag key={topic} label={{ name: topic, color: "" }} />
                    ))}
                  </p>
                ) : (
                  <p className="text-body leading-5 text-faint">No description</p>
                )}
              </div>
            </div>
            {/* One line that never wraps: the figures keep their width and
                the time gives way, so every floor is one height. */}
            <div className="mt-auto flex h-9 min-w-0 items-center gap-3.5 border-t border-hairline px-3.5 text-hint whitespace-nowrap text-muted-foreground">
              {repo.language && (
                <span className="flex min-w-0 shrink items-center text-foreground">
                  <LanguageMark language={repo.language} />
                </span>
              )}
              <Figure icon={Star} value={repo.stars} unit="star" />
              <Figure icon={SourceFork} value={repo.forks} unit="fork" />
              {repo.openIssues > 0 && (
                <Figure icon={IssueMark} value={repo.openIssues} unit="open issue" />
              )}
              <span className="ml-auto flex min-w-0 items-center gap-2">
                {checkout && (
                  <span
                    role="img"
                    data-slot="on-server"
                    aria-label={`On this server at ${checkout.path}`}
                    title={`On this server: ${checkout.path}`}
                    className="flex shrink-0"
                  >
                    <Servers aria-hidden className="size-3.5 text-brand" />
                  </span>
                )}
                {pushed && (
                  <time dateTime={pushed} title={timestamp(pushed)} className="truncate">
                    {relativeTime(pushed)}
                  </time>
                )}
              </span>
            </div>
          </div>
        </SpotlightBorder>
      </BlurFade>
    </li>
  )
}

/** A figure on the card's floor: its glyph and the count, named in full for a screen reader. */
function Figure({
  icon: Glyph,
  value,
  unit,
}: {
  icon: React.ComponentType<{ className?: string; "aria-hidden"?: boolean }>
  value: number
  unit: string
}) {
  return (
    <span className="numeric flex shrink-0 items-center gap-1" title={plural(value, unit)}>
      <Glyph aria-hidden className="size-3.5" />
      {value}
      <span className="sr-only"> {value === 1 ? unit : `${unit}s`}</span>
    </span>
  )
}

/** The shelf before it is read, in its loaded shape. */
export function RepoCardSkeleton({ count = 6, className }: { count?: number; className?: string }) {
  return (
    <ul aria-hidden className={cn(REPO_CARD_GRID, className)}>
      {Array.from({ length: count }, (_, index) => (
        <li key={index} className={REPO_CARD_HEIGHT}>
          <Skeleton className="h-full rounded-xl" />
        </li>
      ))}
    </ul>
  )
}
