"use client"

import { createContext, useCallback, useContext, useEffect, useRef, useState } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { ArrowUpRight } from "@/components/icons"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { get } from "@/lib/api"
import { githubRepoApi } from "@/lib/github"
import type { GitHubAbility, GitHubLabel, GitHubRepositoryDetail } from "@/lib/github-types"
import { cn } from "@/lib/utils"
import { labelColour, reasonFor } from "@/components/github/repo/model"

/**
 * What every view of one repository shares: the connection it is read
 * through, the repository as last read, its API paths, and a way to read it
 * again after a change. The views read their own lists; the repository
 * itself is read once by the page, and again after every change.
 */
export type RepoContextValue = {
  connection: number
  repo: GitHubRepositoryDetail
  /** The repository's API path with `suffix` appended. */
  path: (suffix?: string) => string
  /** Reads the repository again: after a change, its abilities and settings may differ. */
  refresh: () => void
  confirm: (request: ConfirmRequest) => void
  /** Why this dashboard account may not change things here (`service.control`), if it may not. */
  controlReason?: string
  /** Why it may not take a destructive act (`destructive`), if it may not. */
  destructiveReason?: string
}

const RepoContext = createContext<RepoContextValue | null>(null)

export const RepoProvider = RepoContext.Provider

export function useRepo(): RepoContextValue {
  const value = useContext(RepoContext)
  if (!value) throw new Error("useRepo outside a GitHub repository page")
  return value
}

export function repoPathOf(connection: number, fullName: string) {
  return (suffix = "") => githubRepoApi(connection, fullName, suffix)
}

/**
 * One query parameter of the page's address beside `tab`: the pull request
 * or issue open in place of its list. Opening one is a step Back returns
 * from, so it is pushed; the view itself is never in the history.
 */
export function useOpenParam(name: string): [string | null, (value: string | null) => void] {
  const search = useSearchParams()
  const router = useRouter()
  const pathname = usePathname()
  const value = search.get(name)
  const set = useCallback(
    (next: string | null) => {
      const params = new URLSearchParams(window.location.search)
      if (next === null) params.delete(name)
      else params.set(name, next)
      const query = params.toString()
      router.push(`${pathname}${query ? `?${query}` : ""}`, { scroll: false })
    },
    [name, pathname, router],
  )
  return [value, set]
}

/**
 * A disabled control with the reason it cannot be used, on hover and on
 * focus. A disabled button takes neither, so the reason hangs on a wrapper
 * the keyboard can reach.
 */
export function Blocked({
  reason,
  className,
  children,
}: {
  reason: string
  className?: string
  children: React.ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        {/* eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- takes the keyboard so that its reason can be read without a pointer */}
        <span
          tabIndex={0}
          data-slot="blocked"
          aria-label={reason}
          className={cn("inline-flex rounded-md focus-ring", className)}
        >
          {children}
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">{reason}</TooltipContent>
    </Tooltip>
  )
}

/**
 * A button for an act one ability governs: live when the repository allows
 * it, drawn disabled with the ability's reason when it does not. An act the
 * repository does not offer at all is not drawn; that is the caller's.
 */
export function AbilityButton({
  ability,
  destructive,
  disabledReason,
  ...props
}: React.ComponentProps<typeof Button> & {
  ability: GitHubAbility
  /** The act is destructive, so this dashboard account needs `destructive` too. */
  destructive?: boolean
  /** A reason of the page's own that holds the act even when the ability allows it. */
  disabledReason?: string
}) {
  const reason = useActReason(ability, { destructive }) ?? disabledReason
  if (!reason) return <Button {...props} />
  return (
    <Blocked reason={reason}>
      <Button {...props} disabled onClick={undefined} />
    </Blocked>
  )
}

/**
 * Why an act cannot be taken, or undefined when it can: this dashboard
 * account's own rights first (the server refuses those before it asks
 * GitHub), then the repository's ability with GitHub's reason.
 */
export function useActReason(
  ability: GitHubAbility,
  { destructive = false }: { destructive?: boolean } = {},
): string | undefined {
  const { repo, controlReason, destructiveReason } = useRepo()
  return (
    controlReason ?? (destructive ? destructiveReason : undefined) ?? reasonFor(repo.can, ability)
  )
}

/** The link that opens a thing on GitHub, as a small outline button. */
export function OnGitHub({
  href,
  label = "GitHub",
  className,
}: {
  href: string
  label?: string
  className?: string
}) {
  return (
    <Button asChild size="sm" variant="outline" className={className}>
      <a href={href} target="_blank" rel="noreferrer">
        <ArrowUpRight className="size-3.5" />
        {label}
      </a>
    </Button>
  )
}

/** A label as GitHub draws it: its own colour as the word's, nothing filled. */
export function LabelTag({ label }: { label: GitHubLabel }) {
  const colour = labelColour(label.color)
  return (
    <span
      data-slot="github-label"
      title={label.description || label.name}
      className="inline-flex h-5 max-w-40 shrink-0 items-center gap-1 rounded-sm bg-surface-sunken px-1.5 text-micro font-medium text-foreground"
    >
      {colour && (
        <span aria-hidden className="size-2 shrink-0 rounded-full" style={{ background: colour }} />
      )}
      <span className="truncate">{label.name}</span>
    </span>
  )
}

/**
 * A list GitHub hands out a page at a time, read a page more with each
 * **Show more**: the pages read so far, whether there are more, and a reset
 * when what is listed changes (another state chip, another branch).
 */
export function usePages<T, P extends { hasMore: boolean }>(
  path: string | null,
  query: Record<string, string | number | undefined>,
  pick: (page: P) => T[],
) {
  const key = path === null ? null : JSON.stringify([path, query])
  const [state, setState] = useState<{
    key: string | null
    items: T[]
    page: number
    hasMore: boolean
    loading: boolean
    error?: Error
  }>({ key: null, items: [], page: 0, hasMore: false, loading: true })
  const [ask, setAsk] = useState(0)
  const pickRef = useRef(pick)
  useEffect(() => {
    pickRef.current = pick
  })
  const queryRef = useRef(query)
  useEffect(() => {
    queryRef.current = query
  })

  const read = useCallback(
    async (page: number, fresh: boolean, signal?: AbortSignal) => {
      if (path === null) return
      setState((held) => ({ ...held, loading: true, error: undefined }))
      try {
        const answer = await get<P>(path, { ...queryRef.current, page }, signal)
        const items = pickRef.current(answer)
        setState((held) => ({
          key,
          items: fresh ? items : [...held.items, ...items],
          page,
          hasMore: answer.hasMore,
          loading: false,
        }))
      } catch (error) {
        if (signal?.aborted) return
        setState((held) => ({
          ...held,
          key,
          items: fresh ? [] : held.items,
          loading: false,
          error: error as Error,
        }))
      }
    },
    [path, key],
  )

  useEffect(() => {
    if (path === null) return
    const controller = new AbortController()
    void read(1, true, controller.signal)
    return () => controller.abort()
  }, [read, path, ask])

  const current = state.key === key
  return {
    items: current ? state.items : [],
    hasMore: current && state.hasMore,
    loading: state.loading || !current,
    error: current ? state.error : undefined,
    more: () => void read(state.page + 1, false),
    reload: () => setAsk((n) => n + 1),
  }
}
