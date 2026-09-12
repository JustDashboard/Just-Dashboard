"use client"

import { useState } from "react"
import { GitBranch as GitBranchIcon, MoreHorizontal, Plus } from "@/components/icons"
import { get, post } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { GitBranch, GitResult } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { AheadBehind } from "@/components/git/ahead-behind"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Input } from "@/components/ui/input"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { rowReveal } from "@/components/icon-action"

type GitRun = (label: string, fn: () => Promise<GitResult>) => Promise<GitResult>

/**
 * Branch management: create one, switch to it, delete it. Switching is offered
 * only for local branches — checking out a remote one directly lands you in a
 * detached HEAD, which is precisely the state a newcomer cannot get out of, so
 * remotes are shown for reference and not as a trap. Force delete is behind its
 * own menu item because the safe delete refuses to lose unmerged commits and
 * the force one exists to override exactly that.
 */
export function BranchesPanel({
  repoPath,
  busy,
  canControl,
  canDestruct,
  run,
  confirm,
  onChanged,
}: {
  repoPath: string
  busy?: string
  canControl: boolean
  canDestruct: boolean
  run: GitRun
  confirm: (req: ConfirmRequest) => void
  onChanged: () => void
}) {
  const [newBranch, setNewBranch] = useState("")
  const branches = usePoll(
    (signal) => get<GitBranch[]>("/git/branches", { path: repoPath }, signal),
    0,
    [repoPath],
  )
  const q = { path: repoPath }

  const create = () => {
    const name = newBranch.trim()
    if (!name) return
    run(`Created ${name}`, () => post<GitResult>("/git/branch", { ref: name }, { query: q }))
      .then(() => {
        setNewBranch("")
        branches.refresh()
        onChanged()
      })
      .catch(() => {})
  }

  const switchTo = (name: string) =>
    run(`Switched to ${name}`, () => post<GitResult>("/git/checkout", { ref: name }, { query: q }))
      .then(() => {
        branches.refresh()
        onChanged()
      })
      .catch(() => {})

  const remove = (b: GitBranch, force: boolean) =>
    confirm({
      title: `${force ? "Force delete" : "Delete"} branch ${b.name}`,
      confirmLabel: force ? "Force delete" : "Delete",
      description: force ? (
        <p className="text-destructive">
          Deletes <span className="font-mono">{b.name}</span> even if it has commits that exist
          nowhere else. Those commits may become unreachable.
        </p>
      ) : (
        <p>
          Deletes the local branch <span className="font-mono">{b.name}</span>. Git refuses if it
          has commits not merged anywhere, so nothing is lost by accident.
        </p>
      ),
      action: async () => {
        await post("/git/branch/delete", { ref: b.name, hard: force }, { query: q })
        branches.refresh()
        onChanged()
      },
    })

  if (branches.error) return <ErrorState error={branches.error} className="m-3" />
  if (branches.loading && !branches.data) return <LoadingRows className="p-3" rows={6} />

  const local = branches.data?.filter((b) => !b.remote) ?? []
  const remote = branches.data?.filter((b) => b.remote) ?? []

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {canControl && (
        <form
          className="flex shrink-0 gap-2 border-b border-hairline bg-surface-header/60 p-2.5"
          onSubmit={(e) => {
            e.preventDefault()
            create()
          }}
        >
          <Input
            value={newBranch}
            onChange={(e) => setNewBranch(e.target.value)}
            placeholder="New branch from here"
            className="h-8 text-body"
          />
          <Button type="submit" size="sm" variant="outline" disabled={!!busy || !newBranch.trim()}>
            <Plus className="size-3.5" />
            Create
          </Button>
        </form>
      )}

      <div className="min-h-0 flex-1 overflow-auto p-1.5">
        {local.map((b) => (
          <div
            key={b.name}
            className={cn(
              "group flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5 hover:bg-row-hover",
              b.current && "bg-wash-primary",
            )}
          >
            <GitBranchIcon
              className={cn(
                "size-3.5 shrink-0",
                b.current ? "text-success" : "text-muted-foreground",
              )}
            />
            <div className="min-w-0 flex-1">
              <p className="truncate font-mono text-body">
                {b.name}
                {b.current && <span className="ml-2 text-micro text-success">current</span>}
              </p>
              {b.worktree ? (
                <p className="truncate text-hint text-muted-foreground" title={b.worktree}>
                  checked out in {b.worktree}
                </p>
              ) : (
                b.subject && <p className="truncate text-hint text-muted-foreground">{b.subject}</p>
              )}
            </div>
            <AheadBehind ahead={b.ahead} behind={b.behind} />
            {/* A branch another worktree has checked out cannot be switched to
                or deleted — not even with -D — so the actions are withheld and
                the row says why rather than offering a button that errors. */}
            {b.worktree && (
              <span className="shrink-0 text-micro text-muted-foreground">in use</span>
            )}
            {canControl && !b.current && !b.worktree && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    size="xs"
                    variant="ghost"
                    disabled={!!busy}
                    className={cn("shrink-0", rowReveal())}
                    onClick={() => switchTo(b.name)}
                  >
                    Switch
                  </Button>
                </TooltipTrigger>
                <TooltipContent>{`Check out ${b.name}`}</TooltipContent>
              </Tooltip>
            )}
            {canDestruct && !b.current && !b.worktree && (
              <DropdownMenu>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <DropdownMenuTrigger asChild>
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={!!busy}
                        aria-label={`More actions for ${b.name}`}
                        className={cn(
                          "size-6 shrink-0 p-0 text-muted-foreground hover:text-foreground",
                          rowReveal(),
                        )}
                      >
                        <MoreHorizontal className="size-3.5" />
                      </Button>
                    </DropdownMenuTrigger>
                  </TooltipTrigger>
                  <TooltipContent>{`Delete ${b.name}`}</TooltipContent>
                </Tooltip>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem onSelect={() => remove(b, false)}>
                    Delete branch
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem
                    className="text-destructive focus:text-destructive"
                    onSelect={() => remove(b, true)}
                  >
                    Force delete
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            )}
          </div>
        ))}

        {remote.length > 0 && (
          <>
            <p className="px-2 pt-3 pb-1 text-micro font-medium tracking-wide text-muted-foreground uppercase">
              Remote branches
            </p>
            {remote.map((b) => (
              <div
                key={b.name}
                className="flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-muted-foreground"
              >
                <GitBranchIcon className="size-3.5 shrink-0" />
                <p className="min-w-0 flex-1 truncate font-mono text-xs">{b.name}</p>
                {b.subject && (
                  <p className="hidden max-w-[10rem] min-w-0 truncate text-hint sm:block">
                    {b.subject}
                  </p>
                )}
              </div>
            ))}
          </>
        )}

        {local.length === 0 && remote.length === 0 && (
          <EmptyState className="m-2" icon={GitBranchIcon} title="No branches" />
        )}
      </div>
    </div>
  )
}
