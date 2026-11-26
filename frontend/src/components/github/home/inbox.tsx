"use client"

import { useSearchParams } from "next/navigation"
import { ChatBubble, RefreshClockwise } from "@/components/icons"
import { plural, relativeTime, timestamp } from "@/lib/format"
import type { GitHubInboxFilter, GitHubInboxItem, GitHubInboxPage } from "@/lib/github-types"
import { cn } from "@/lib/utils"
import { ChipCount, ChipStrip, FilterChip } from "@/components/chips"
import { ChoiceList, ChoiceListSkeleton, ChoiceRow } from "@/components/choice-row"
import { IssueMark } from "@/components/git/glyphs"
import { ForgeFace } from "@/components/git/marks"
import { PullStateMark } from "@/components/git/pull-state"
import { LabelTag, ShowMore, usePages } from "@/components/github/repo/shared"
import { IconAction } from "@/components/icon-action"
import { Section } from "@/components/section"
import { Segments } from "@/components/segments"
import { EmptyNote, ErrorState, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import {
  ISSUE_FILTERS,
  ISSUE_STATE_WORD,
  PULL_FILTERS,
  inboxFilterOf,
  inboxHref,
  inboxStateOf,
  inboxWhen,
  issueStateOf,
  type IssueState,
} from "@/components/github/home/model"

export type InboxKind = "pull" | "issue"

const ISSUE_STATE_COLOUR: Record<IssueState, string> = {
  open: "var(--pull-open)",
  completed: "var(--pull-merged)",
  not_planned: "var(--pull-draft)",
}

const NOUN: Record<InboxKind, [one: string, many: string]> = {
  pull: ["pull request", "pull requests"],
  issue: ["issue", "issues"],
}

function apiPath(connection: number, kind: InboxKind) {
  return `/github/connections/${connection}/${kind === "pull" ? "pulls" : "issues"}`
}

/** An issue's state, as the glyph in front of its title in GitHub's colours. */
function IssueStateMark({ item }: { item: Pick<GitHubInboxItem, "state" | "stateReason"> }) {
  const state = issueStateOf(item)
  return (
    <IssueMark
      role="img"
      aria-label={ISSUE_STATE_WORD[state]}
      className="size-4 shrink-0"
      style={{ color: ISSUE_STATE_COLOUR[state] }}
    />
  )
}

function StateMark({ item, kind }: { item: GitHubInboxItem; kind: InboxKind }) {
  return kind === "pull" ? (
    <PullStateMark
      pull={{ state: item.state, draft: item.draft, merged: item.state === "merged" }}
    />
  ) : (
    <IssueStateMark item={item} />
  )
}

/**
 * One pull request or issue from anywhere the account reaches, read as GitHub
 * lists them: the state as the coloured glyph in front, the title, then where
 * it is and who opened it when. Its labels in their own colours, the
 * conversation's size and the author's face stand at the end, from a tablet
 * up; on a phone the row keeps its height and loses them.
 *
 * One in a repository the page lists opens on that repository's page with
 * the request or issue open, where it can be merged or closed; one anywhere
 * else opens on GitHub.
 */
export function InboxRow({
  item,
  kind,
  listed,
  connection,
  index,
}: {
  item: GitHubInboxItem
  kind: InboxKind
  listed: Set<string>
  connection: number
  index?: number
}) {
  const where = inboxHref(item, kind, listed, connection)
  const when = inboxWhen(item)
  const at = (
    <time dateTime={when.at} title={timestamp(when.at)}>
      {relativeTime(when.at)}
    </time>
  )
  return (
    <ChoiceRow
      index={index}
      arrow={false}
      leading={<StateMark item={item} kind={kind} />}
      verb={`Open ${item.repository}#${item.number}`}
      href={where.external ? undefined : where.href}
      onSelect={where.external ? () => window.open(where.href, "_blank", "noopener") : undefined}
      workspaceItem={{
        id: `${item.repository}#${item.number}`,
        name: `${item.repository}#${item.number}`,
      }}
      title={item.title}
      description={
        <span className="flex min-w-0 items-center gap-1">
          <span className="min-w-0 truncate font-mono">{item.repository}</span>{" "}
          <span className="numeric shrink-0">#{item.number}</span>{" "}
          <span aria-hidden className="shrink-0">
            ·
          </span>{" "}
          <span className="shrink-0 truncate max-sm:hidden">
            {when.open ? <>opened {at} by</> : "by"}
          </span>{" "}
          <span className="min-w-0 shrink-0 truncate font-mono max-sm:hidden">{item.author}</span>{" "}
          {!when.open && (
            <span className="shrink-0 truncate max-sm:hidden">
              was {when.verb} {at}
            </span>
          )}
          <time dateTime={when.at} title={timestamp(when.at)} className="shrink-0 sm:hidden">
            {relativeTime(when.at)}
          </time>
        </span>
      }
      trailing={
        <span className="flex items-center gap-3 text-hint text-muted-foreground max-sm:hidden">
          <span className="hidden max-w-56 items-center gap-1.5 overflow-hidden lg:flex">
            {item.draft && <Tag>Draft</Tag>}
            {item.labels.slice(0, 2).map((label) => (
              <LabelTag key={label.name} label={label} />
            ))}
          </span>
          <span
            className={cn(
              "numeric flex w-9 items-center justify-end gap-1",
              item.comments === 0 && "invisible",
            )}
            title={plural(item.comments, "comment")}
          >
            <ChatBubble aria-hidden className="size-3.5" />
            {item.comments}
            <span className="sr-only"> comments</span>
          </span>
          <ForgeFace login={item.author} provider="github" size="md" />
        </span>
      }
    />
  )
}

/**
 * A few of the account's open pull requests or issues, for the Overview: the
 * first five of one filter under a section head that counts them all and
 * leads to the whole list. Nothing to list is one quiet line.
 */
export function InboxPreview({
  title,
  kind,
  filter,
  connection,
  listed,
  href,
  count = 5,
  round,
}: {
  title: string
  kind: InboxKind
  filter: GitHubInboxFilter
  connection: number
  listed: Set<string>
  /** The whole list: the page's own tab, on this filter. */
  href: string
  count?: number
  /** Bumped by the page's Refresh: the preview is read again from GitHub. */
  round?: number
}) {
  const pages = usePages<GitHubInboxItem, GitHubInboxPage>(
    apiPath(connection, kind),
    { filter, state: "open" },
    (page) => page.items,
    { kept: true, round },
  )
  const total = pages.last?.total
  const [, many] = NOUN[kind]
  return (
    <Section
      title={title}
      count={total !== undefined ? <span className="numeric">{total}</span> : undefined}
      link={total ? { href, label: `All ${many}` } : undefined}
    >
      <div data-slot={`github-inbox-${kind}-${filter}`}>
        {pages.error && pages.items.length === 0 ? (
          <ErrorState error={pages.error} onRetry={pages.reload} retryable />
        ) : pages.loading && pages.items.length === 0 ? (
          <ChoiceListSkeleton rows={3} />
        ) : pages.items.length === 0 ? (
          <EmptyNote>
            No open {kind === "pull" && filter === "review" ? "review requests" : many}.
          </EmptyNote>
        ) : (
          <ChoiceList aria-label={title}>
            {pages.items.slice(0, count).map((item, index) => (
              <InboxRow
                key={`${item.repository}#${item.number}`}
                item={item}
                kind={kind}
                listed={listed}
                connection={connection}
                index={index}
              />
            ))}
          </ChoiceList>
        )}
      </div>
    </Section>
  )
}

/**
 * Every pull request or issue the account is part of, as GitHub's own
 * Pull requests and Issues pages list them: which part (opened, asked to
 * review, assigned, mentioned) as chips, open or closed beside them, thirty
 * at a time with Show more. GitHub's count of the whole is on the chosen
 * chip. The filter and the state are in the address (`?filter=`, `?state=`),
 * so the Overview's sections open the list on their own filter, and going
 * into a request and Back finds the list as it was left. The refresh button,
 * like the page's Refresh, reads it from GitHub again rather than from the
 * server's minute of memory.
 */
export function InboxView({
  kind,
  connection,
  listed,
  round,
}: {
  kind: InboxKind
  connection: number
  listed: Set<string>
  /** Bumped by the page's Refresh: the list is read again from GitHub. */
  round?: number
}) {
  const filters = kind === "pull" ? PULL_FILTERS : ISSUE_FILTERS
  const search = useSearchParams()
  const filter = inboxFilterOf(kind, search.get("filter"))
  const state = inboxStateOf(search.get("state"))
  // A chip is not a step Back returns from, as the view itself is not: the
  // address entry is replaced, and the defaults leave it.
  const choose = (change: { filter?: GitHubInboxFilter; state?: "open" | "closed" }) => {
    const next = { filter, state, ...change }
    const url = new URL(window.location.href)
    if (next.filter === "created") url.searchParams.delete("filter")
    else url.searchParams.set("filter", next.filter)
    if (next.state === "open") url.searchParams.delete("state")
    else url.searchParams.set("state", next.state)
    if (url.href !== window.location.href) {
      window.history.replaceState(null, "", `${url.pathname}${url.search}${url.hash}`)
    }
  }
  const pages = usePages<GitHubInboxItem, GitHubInboxPage>(
    apiPath(connection, kind),
    { filter, state },
    (page) => page.items,
    { kept: true, round },
  )
  const [, many] = NOUN[kind]
  const total = pages.last?.total
  return (
    <div className="flex min-w-0 flex-col gap-4" data-slot={`github-inbox-${kind}`}>
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <ChipStrip aria-label={`Which ${many}`}>
          {filters.map((option) => (
            <FilterChip
              key={option.key}
              selected={filter === option.key}
              onClick={() => choose({ filter: option.key })}
            >
              {option.label}
              {filter === option.key && total !== undefined && <ChipCount>{total}</ChipCount>}
            </FilterChip>
          ))}
        </ChipStrip>
        <div className="flex items-center gap-2 sm:ml-auto">
          <Segments
            label="State"
            value={state}
            onChange={(next) => choose({ state: next })}
            options={[
              { value: "open", label: "Open" },
              { value: "closed", label: "Closed" },
            ]}
          />
          <IconAction
            label={`Read the ${many} again`}
            variant="outline"
            size="icon-sm"
            onClick={pages.reload}
          >
            <RefreshClockwise className="size-4" />
          </IconAction>
        </div>
      </div>
      {pages.last?.incomplete && (
        <Notice tone="warning" title="GitHub's search ran out of time">
          Some {many} may be missing from this list. Read it again in a moment.
        </Notice>
      )}
      {pages.error && pages.items.length === 0 ? (
        <ErrorState error={pages.error} onRetry={pages.reload} retryable />
      ) : pages.loading && pages.items.length === 0 ? (
        <ChoiceListSkeleton rows={6} />
      ) : pages.items.length === 0 ? (
        <EmptyNote>
          No {state} {many}
          {filter === "created"
            ? " you opened"
            : filter === "review"
              ? " waiting on your review"
              : filter === "assigned"
                ? " assigned to you"
                : " that mention you"}
          .
        </EmptyNote>
      ) : (
        <ChoiceList aria-label={`${many} ${filter}`}>
          {pages.items.map((item, index) => (
            <InboxRow
              key={`${item.repository}#${item.number}`}
              item={item}
              kind={kind}
              listed={listed}
              connection={connection}
              index={index}
            />
          ))}
        </ChoiceList>
      )}
      <ShowMore pages={pages} />
    </div>
  )
}
