"use client"

import { useEffect, useRef, useState } from "react"
import { ArrowLeftRight, CloudUpload, Cross, GitTag, Pencil, Plus, Trash } from "@/components/icons"
import { get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import { SourceBranch, SourceMerge } from "@/components/git/glyphs"
import type { GitBranch, GitResult, GitTag as GitTagType } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { AheadBehind } from "@/components/git/ahead-behind"
import { GitExplain } from "@/components/git/help"
import { NameDialog } from "@/components/git/name-dialog"
import type { GitPreview } from "@/components/git/preview-panel"
import type { GitRun } from "@/components/git/run"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { VerbActions, type Verb } from "@/components/verbs"

/**
 * Branches and tags: the names in this repository, and what to do with them.
 *
 * Local branches first, with the facts a decision needs on the row — current,
 * merged (safe to delete), gone (its remote branch was deleted, the state a
 * branch is left in after its pull request merges) — then the remote ones,
 * then the tags. Switching is offered only for local branches: checking a
 * remote one out directly lands in a detached HEAD, which is precisely the
 * state a newcomer cannot get out of, so a remote branch offers "check out"
 * instead, which makes a local branch that tracks it.
 *
 * Every other verb sits behind one menu, by name: merge into
 * the current branch, compare, rename, delete here, delete on the remote.
 * Force delete is a separate item because the safe delete refuses to lose
 * unmerged commits and the force one exists to override exactly that.
 */
export function BranchesPanel({
  repoPath,
  current,
  busy,
  canControl,
  canDestruct,
  run,
  confirm,
  onSelect,
  onChanged,
}: {
  repoPath: string
  current: string
  busy?: string
  canControl: boolean
  canDestruct: boolean
  run: GitRun
  confirm: (req: ConfirmRequest) => void
  onSelect: (p: GitPreview) => void
  onChanged: () => void
}) {
  const [creating, setCreating] = useState<"branch" | "tag" | null>(null)
  const [renaming, setRenaming] = useState<GitBranch | null>(null)
  const [tracking, setTracking] = useState<GitBranch | null>(null)
  const branches = usePoll(
    (signal) => get<GitBranch[]>("/git/branches", { path: repoPath }, signal),
    0,
    [repoPath],
  )
  const tags = usePoll((signal) => get<GitTagType[]>("/git/tags", { path: repoPath }, signal), 0, [
    repoPath,
  ])
  const q = { path: repoPath }
  const refresh = () => {
    branches.refresh()
    tags.refresh()
    onChanged()
  }

  const act = (label: string, path: string, body: unknown) =>
    void run(label, () => post<GitResult>(path, body, { query: q }))
      .then(refresh)
      .catch(() => undefined)

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
        refresh()
      },
    })

  const removeRemote = (remote: string, name: string) =>
    confirm({
      title: `Delete ${name} on ${remote}`,
      confirmLabel: "Delete on remote",
      description: (
        <p>
          Removes the branch <span className="font-mono">{name}</span> from{" "}
          <span className="font-mono">{remote}</span>. Everyone who fetches will see it gone. The
          commits stay on the remote until it prunes them, and in any local branch that has them.
        </p>
      ),
      action: async () => {
        await post("/git/branch/delete-remote", { remote, ref: name }, { query: q })
        refresh()
      },
    })

  const removeTag = (t: GitTagType) =>
    confirm({
      title: `Delete tag ${t.name}`,
      confirmLabel: "Delete tag",
      description: (
        <p>
          Removes the tag here. The commit it named is untouched, and a copy already pushed stays on
          the remote.
        </p>
      ),
      action: async () => {
        await post("/git/tag/delete", { name: t.name }, { query: q })
        refresh()
      },
    })

  const localVerbs = (b: GitBranch): Verb[] => {
    const verbs: Verb[] = []
    if (!b.current) {
      verbs.push({
        key: "compare",
        label: `Compare with ${current}`,
        icon: ArrowLeftRight,
        run: () => onSelect({ kind: "compare", base: current, head: b.name }),
      })
    }
    if (canControl) {
      if (!b.current && !b.worktree) {
        verbs.push({
          key: "merge",
          label: `Merge into ${current}`,
          icon: SourceMerge,
          disabled: !!busy,
          run: () =>
            confirm({
              title: `Merge ${b.name} into ${current}`,
              confirmLabel: "Merge",
              description: (
                <p>
                  Every commit on <span className="font-mono">{b.name}</span> that{" "}
                  <span className="font-mono">{current}</span> lacks is brought in. A conflict
                  pauses the merge so you can resolve it in Changes and continue.
                </p>
              ),
              action: async () => {
                await run(`Merged ${b.name}`, () =>
                  post<GitResult>(
                    "/git/operation/start",
                    { operation: "merge", ref: b.name },
                    { query: q },
                  ),
                )
                refresh()
              },
            }),
        })
      }
      verbs.push({
        key: "rename",
        label: "Rename",
        icon: Pencil,
        disabled: !!busy || !!b.worktree,
        run: () => setRenaming(b),
      })
      verbs.push({
        key: "upstream",
        label: "Set upstream",
        icon: ArrowLeftRight,
        disabled: !!busy,
        run: () => setTracking(b),
      })
      if (b.upstream)
        verbs.push({
          key: "untrack",
          label: "Stop tracking upstream",
          icon: SourceBranch,
          disabled: !!busy,
          run: () => act("Upstream removed", "/git/upstream", { name: b.name, ref: "" }),
        })
    }
    if (canDestruct && !b.current && !b.worktree) {
      verbs.push(
        {
          key: "delete",
          label: "Delete branch",
          icon: Trash,
          danger: true,
          run: () => remove(b, false),
        },
        {
          key: "force",
          label: "Force delete",
          icon: Trash,
          danger: true,
          run: () => remove(b, true),
        },
      )
      if (b.upstream && !b.gone) {
        const [remote, ...rest] = b.upstream.split("/")
        verbs.push({
          key: "remote",
          label: `Delete on ${remote}`,
          icon: Cross,
          danger: true,
          run: () => removeRemote(remote, rest.join("/")),
        })
      }
    }
    return verbs
  }

  const remoteVerbs = (b: GitBranch): Verb[] => {
    const verbs: Verb[] = [
      {
        key: "compare",
        label: `Compare with ${current}`,
        icon: ArrowLeftRight,
        run: () => onSelect({ kind: "compare", base: current, head: b.name }),
      },
    ]
    if (canDestruct && b.remoteName && b.local) {
      verbs.push({
        key: "remote",
        label: `Delete on ${b.remoteName}`,
        icon: Trash,
        danger: true,
        run: () => removeRemote(b.remoteName!, b.local!),
      })
    }
    return verbs
  }

  const tagVerbs = (t: GitTagType): Verb[] => {
    const verbs: Verb[] = []
    if (t.commit) {
      verbs.push({
        key: "show",
        label: "Show the commit",
        icon: SourceBranch,
        run: () => onSelect({ kind: "commit", sha: t.commit! }),
      })
    }
    if (canControl) {
      verbs.push({
        key: "push",
        label: "Push this tag",
        icon: CloudUpload,
        disabled: !!busy,
        run: () => act(`Pushed ${t.name}`, "/git/push/tags", { ref: t.name }),
      })
    }
    if (canDestruct) {
      verbs.push({
        key: "delete",
        label: "Delete tag",
        icon: Trash,
        danger: true,
        run: () => removeTag(t),
      })
    }
    return verbs
  }

  if (branches.error) return <ErrorState error={branches.error} className="m-3" />
  if (branches.loading && !branches.data) return <LoadingRows className="p-3" rows={6} />

  const local = branches.data?.filter((b) => !b.remote) ?? []
  const remote = branches.data?.filter((b) => b.remote) ?? []
  const localNames = new Set(local.map((b) => b.name))
  const tagList = tags.data ?? []

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {canControl && (
        <div className="flex shrink-0 items-center gap-1.5 border-b border-hairline px-2 py-1.5">
          <Button
            size="xs"
            variant="outline"
            disabled={!!busy || creating !== null}
            onClick={() => setCreating("branch")}
          >
            <Plus className="size-3" />
            New branch
          </Button>
          <Button
            size="xs"
            variant="outline"
            disabled={!!busy || creating !== null}
            onClick={() => setCreating("tag")}
          >
            <GitTag className="size-3" />
            New tag
          </Button>
          <span className="flex-1" />
          {tagList.length > 0 && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  size="xs"
                  variant="ghost"
                  disabled={!!busy}
                  onClick={() => act("Pushed tags", "/git/push/tags", {})}
                >
                  <CloudUpload className="size-3" />
                  Push tags
                </Button>
              </TooltipTrigger>
              <TooltipContent>Publish every local tag to the remote</TooltipContent>
            </Tooltip>
          )}
        </div>
      )}

      <div className="min-h-0 flex-1 overflow-auto">
        {creating === "branch" && (
          <CreateBranchRow
            busy={!!busy}
            branches={branches.data ?? []}
            onCancel={() => setCreating(null)}
            onCreate={(name, from) => {
              void run(`Created ${name}`, () =>
                post<GitResult>("/git/branch", { ref: name, from }, { query: q }),
              )
                .then(() => {
                  setCreating(null)
                  refresh()
                })
                .catch(() => undefined)
            }}
          />
        )}
        {creating === "tag" && (
          <CreateTagRow
            busy={!!busy}
            onCancel={() => setCreating(null)}
            onCreate={(name, message) => {
              void run(`Tagged ${name}`, () =>
                post<GitResult>("/git/tag", { name, message }, { query: q }),
              )
                .then(() => {
                  setCreating(null)
                  refresh()
                })
                .catch(() => undefined)
            }}
          />
        )}

        <GroupLabel label="Local" explain="branch" count={local.length} />
        <ul className="divide-y divide-hairline">
          {local.map((b) => (
            <li
              key={b.name}
              className="group flex min-w-0 items-center gap-2 py-1.5 pr-1.5 pl-3 transition-colors hover:bg-row-hover"
            >
              <div className="min-w-0 flex-1">
                <p className="flex min-w-0 items-center gap-1.5">
                  <span
                    className={cn(
                      "truncate font-mono text-xs",
                      b.current ? "font-medium" : "text-foreground/90",
                    )}
                  >
                    {b.name}
                  </span>
                  {b.current && <Tag tone="success">current</Tag>}
                  {b.gone && <Tag tone="danger">gone</Tag>}
                  {!b.current && b.merged && !b.gone && <Tag>merged</Tag>}
                  {b.worktree && <Tag tone="warning">in use</Tag>}
                </p>
                <p
                  className="truncate text-micro text-muted-foreground"
                  title={b.worktree ?? b.subject}
                >
                  {b.worktree
                    ? `checked out in ${b.worktree}`
                    : b.upstream
                      ? `tracks ${b.upstream}${b.at ? ` · ${relativeTime(b.at)}` : ""}`
                      : (b.subject ?? "")}
                </p>
              </div>
              <AheadBehind ahead={b.ahead} behind={b.behind} />
              {canControl && !b.current && !b.worktree && (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Button
                      size="xs"
                      variant="ghost"
                      disabled={!!busy}
                      className="shrink-0"
                      onClick={() => act(`Switched to ${b.name}`, "/git/checkout", { ref: b.name })}
                    >
                      Switch
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent>{`Check out ${b.name}`}</TooltipContent>
                </Tooltip>
              )}
              <VerbActions verbs={localVerbs(b)} reveal />
            </li>
          ))}
          {local.length === 0 && (
            <li className="px-3 py-3 text-hint text-muted-foreground">No local branches yet.</li>
          )}
        </ul>

        {remote.length > 0 && (
          <>
            <GroupLabel label="Remote" explain="remote" count={remote.length} />
            <ul className="divide-y divide-hairline">
              {remote.map((b) => (
                <li
                  key={b.name}
                  className="group flex min-w-0 items-center gap-2 py-1.5 pr-1.5 pl-3 text-muted-foreground transition-colors hover:bg-row-hover"
                >
                  <div className="min-w-0 flex-1">
                    <p className="truncate font-mono text-xs">{b.name}</p>
                    {b.subject && (
                      <p className="truncate text-micro">
                        {b.subject}
                        {b.at ? ` · ${relativeTime(b.at)}` : ""}
                      </p>
                    )}
                  </div>
                  {canControl && b.local && !localNames.has(b.local) && (
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <Button
                          size="xs"
                          variant="ghost"
                          disabled={!!busy}
                          className="shrink-0"
                          onClick={() =>
                            act(`Checked out ${b.local}`, "/git/checkout", {
                              ref: b.name,
                              local: b.local,
                            })
                          }
                        >
                          Check out
                        </Button>
                      </TooltipTrigger>
                      <TooltipContent>
                        {`Make a local ${b.local} that tracks ${b.name}, and switch to it`}
                      </TooltipContent>
                    </Tooltip>
                  )}
                  <VerbActions verbs={remoteVerbs(b)} reveal />
                </li>
              ))}
            </ul>
          </>
        )}

        <GroupLabel label="Tags" explain="tag" count={tagList.length} />
        {tags.error && <ErrorState error={tags.error} className="m-3" />}
        <ul className="divide-y divide-hairline">
          {tagList.map((t) => (
            <li
              key={t.name}
              className="group flex min-w-0 items-center gap-2 py-1.5 pr-1.5 pl-3 transition-colors hover:bg-row-hover"
            >
              <div className="min-w-0 flex-1">
                <p className="flex min-w-0 items-center gap-1.5">
                  <button
                    type="button"
                    disabled={!t.commit}
                    onClick={() => t.commit && onSelect({ kind: "commit", sha: t.commit })}
                    className="truncate font-mono text-xs focus-ring-inset hover:underline"
                  >
                    {t.name}
                  </button>
                  {t.annotated && <Tag>annotated</Tag>}
                </p>
                <p className="truncate text-micro text-muted-foreground" title={t.message}>
                  {t.commit ? <span className="font-mono">{t.commit}</span> : null}
                  {t.message ? ` · ${t.message}` : ""}
                  {t.at ? ` · ${relativeTime(t.at)}` : ""}
                </p>
              </div>
              <VerbActions verbs={tagVerbs(t)} reveal />
            </li>
          ))}
          {tagList.length === 0 && !tags.loading && (
            <li className="px-3 py-3 text-hint text-muted-foreground">
              No tags. A tag pins a name to one commit — a release, say.
            </li>
          )}
        </ul>

        {local.length === 0 && remote.length === 0 && (
          <EmptyState className="m-2" icon={SourceBranch} title="No branches" />
        )}
      </div>

      <NameDialog
        open={tracking !== null}
        onOpenChange={(open) => !open && setTracking(null)}
        title={`Set upstream for ${tracking?.name ?? "branch"}`}
        label="Upstream branch"
        initial={tracking?.upstream ?? ""}
        placeholder="origin/main"
        hint="Fetch first if the remote branch is new."
        confirmLabel="Set upstream"
        onSubmit={async (ref) => {
          if (!tracking) return
          await run("Upstream updated", () =>
            post<GitResult>("/git/upstream", { name: tracking.name, ref }, { query: q }),
          )
          refresh()
        }}
      />
      <NameDialog
        open={renaming !== null}
        onOpenChange={(o) => !o && setRenaming(null)}
        title={`Rename ${renaming?.name ?? "branch"}`}
        label="New name"
        initial={renaming?.name ?? ""}
        confirmLabel="Rename"
        onSubmit={async (name) => {
          if (!renaming) return
          await run(`Renamed to ${name}`, () =>
            post<GitResult>("/git/branch/rename", { ref: renaming.name, name }, { query: q }),
          )
          refresh()
        }}
      />
    </div>
  )
}

function GroupLabel({ label, explain, count }: { label: string; explain: string; count: number }) {
  return (
    <div className="sticky top-0 z-10 flex h-8 items-center gap-1.5 border-b border-hairline bg-card px-3">
      <span className="eyebrow">{label}</span>
      <span className="numeric text-hint text-muted-foreground">{count}</span>
      <GitExplain name={explain} />
    </div>
  )
}

/**
 * The create row: a name, where it starts from, and Enter. It is a row in the
 * list rather than a dialog because a branch is made a dozen times a day and
 * a modal for a dozen-times-a-day thing is a modal that gets in the way.
 */
function CreateBranchRow({
  busy,
  branches,
  onCancel,
  onCreate,
}: {
  busy: boolean
  branches: GitBranch[]
  onCancel: () => void
  onCreate: (name: string, from: string) => void
}) {
  const [name, setName] = useState("")
  const [from, setFrom] = useState("HEAD")
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => ref.current?.focus(), [])
  return (
    <form
      className="flex flex-wrap items-center gap-1.5 border-b border-hairline bg-surface-header/60 px-2 py-1.5"
      onSubmit={(e) => {
        e.preventDefault()
        if (name.trim()) onCreate(name.trim(), from === "HEAD" ? "" : from)
      }}
    >
      <Input
        ref={ref}
        value={name}
        disabled={busy}
        spellCheck={false}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => e.key === "Escape" && onCancel()}
        placeholder="New branch name"
        aria-label="New branch name"
        className="h-7 min-w-0 flex-1 font-mono text-xs sm:h-7"
      />
      <Select value={from} onValueChange={setFrom}>
        <SelectTrigger size="sm" className="h-7 w-36 text-xs sm:h-7" aria-label="Start from">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="HEAD">from here</SelectItem>
          {branches.map((b) => (
            <SelectItem key={b.name} value={b.name} className="font-mono text-xs">
              {b.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Button type="submit" size="xs" disabled={busy || !name.trim()}>
        Create
      </Button>
      <Button type="button" size="xs" variant="ghost" onClick={onCancel}>
        Cancel
      </Button>
    </form>
  )
}

function CreateTagRow({
  busy,
  onCancel,
  onCreate,
}: {
  busy: boolean
  onCancel: () => void
  onCreate: (name: string, message: string) => void
}) {
  const [name, setName] = useState("")
  const [message, setMessage] = useState("")
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => ref.current?.focus(), [])
  return (
    <form
      className="flex flex-wrap items-center gap-1.5 border-b border-hairline bg-surface-header/60 px-2 py-1.5"
      onSubmit={(e) => {
        e.preventDefault()
        if (name.trim()) onCreate(name.trim(), message.trim())
      }}
    >
      <Input
        ref={ref}
        value={name}
        disabled={busy}
        spellCheck={false}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => e.key === "Escape" && onCancel()}
        placeholder="v1.0.0"
        aria-label="Tag name"
        className="h-7 w-32 font-mono text-xs sm:h-7"
      />
      <Input
        value={message}
        disabled={busy}
        onChange={(e) => setMessage(e.target.value)}
        onKeyDown={(e) => e.key === "Escape" && onCancel()}
        placeholder="Message (optional — makes it annotated)"
        aria-label="Tag message"
        className="h-7 min-w-0 flex-1 text-xs sm:h-7"
      />
      <Button type="submit" size="xs" disabled={busy || !name.trim()}>
        Tag HEAD
      </Button>
      <Button type="button" size="xs" variant="ghost" onClick={onCancel}>
        Cancel
      </Button>
    </form>
  )
}
