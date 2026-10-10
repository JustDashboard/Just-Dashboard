"use client"

import { useState } from "react"
import { Archive, CloudUpload, Pencil, Trash, Warning } from "@/components/icons"
import { get, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { GitRemoval, GitRepo } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useConfirm } from "@/components/confirm-dialog"
import { FormFact } from "@/components/form"
import { SourceBranch } from "@/components/git/glyphs"
import { RepoMark } from "@/components/git/languages"
import { BranchChip, branchLabel } from "@/components/git/marks"
import { Modal } from "@/components/modal"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"

/**
 * Taking a checkout off this server: removing a linked worktree, and deleting
 * a repository's folder for good.
 *
 * They are two acts of very different weight and are kept apart. Removing a
 * clean worktree loses nothing — its branch and every commit live on in the
 * repository it belongs to — so it is an ordinary confirmation, and git
 * itself refuses one with uncommitted work in it. Deleting a checkout from
 * the server is the one thing on the Git page that cannot be undone: the
 * folder goes, with whatever in it was never pushed, never committed, or
 * never meant to be committed (`.env`, build output, a stash). So it asks
 * the server first what exactly exists only here (`GET /git/removal`), says
 * that in the dialog in counts, and takes the folder's name typed out — the
 * server re-checks the phrase (invariant 3).
 *
 * A main checkout with worktrees still attached cannot be deleted under them:
 * each worktree's `.git` file points into the folder being deleted. The
 * dialog says so and offers to remove them first, rather than letting the
 * server refuse after the name was typed.
 */
export function useCheckoutRemoval(onRemoved: (path: string) => void) {
  const { confirm, dialog } = useConfirm()
  const [checking, setChecking] = useState<string>()
  const [blocked, setBlocked] = useState<{ repo: GitRepo; removal: GitRemoval }>()

  const deleteCheckout = async (repo: GitRepo) => {
    setChecking(repo.path)
    try {
      const removal = await get<GitRemoval>("/git/removal", { path: repo.path })
      if (removal.protected) {
        setBlocked({ repo, removal })
        return
      }
      confirm({
        title: removal.worktree ? "Delete worktree from server" : "Delete repository from server",
        subject: {
          mark: <RepoMark repo={repo} size="sm" />,
          name: repo.name,
          facts: (
            <>
              <FormFact label="at" mono>
                {repo.path}
              </FormFact>
              {!repo.detached && repo.branch && (
                <FormFact label="on" mono>
                  {repo.branch}
                </FormFact>
              )}
            </>
          ),
        },
        description: <Losses removal={removal} />,
        phrase: removal.name,
        confirmLabel: "Delete forever",
        action: async (phrase) => {
          await post("/git/repository/delete", { path: repo.path }, { confirm: phrase })
        },
        onDone: () => onRemoved(repo.path),
      })
    } catch (error) {
      notify.error("Could not read what deleting would lose", error)
    } finally {
      setChecking(undefined)
    }
  }

  // A worktree with work in it goes straight to the delete that says what is
  // lost: the ordinary removal would only be refused by git after the press.
  const removeWorktree = (worktree: GitRepo, main: string) => {
    if (worktree.dirty) {
      void deleteCheckout(worktree)
      return
    }
    confirm({
      title: "Remove worktree",
      subject: {
        mark: <RepoMark repo={worktree} size="sm" />,
        name: branchLabel(worktree.branch, worktree.detached),
        facts: (
          <FormFact label="at" mono>
            {worktree.path}
          </FormFact>
        ),
      },
      description: (
        <p>
          The folder is removed from the server.{" "}
          {worktree.detached ? "Its commits stay" : <>The branch and its commits stay</>} in the
          repository at <span className="font-mono break-all">{main}</span>, so nothing committed is
          lost.
        </p>
      ),
      confirmLabel: "Remove worktree",
      action: async () => {
        await post("/git/worktree/remove", { path: worktree.path }, { query: { path: main } })
      },
      onDone: () => onRemoved(worktree.path),
    })
  }

  const blockedDialog = blocked && (
    <Modal
      open
      onOpenChange={(open) => !open && setBlocked(undefined)}
      title={`${blocked.repo.name} cannot be deleted yet`}
      size="sm"
      footer={
        <Button variant="outline" onClick={() => setBlocked(undefined)}>
          Close
        </Button>
      }
    >
      {blocked.removal.worktrees.length === 0 ? (
        <div className="space-y-3">
          <Notice tone="warning" icon={Warning} title="The server will not delete this folder">
            {blocked.removal.protected}
          </Notice>
          {blocked.removal.nested.length > 0 && (
            <ul className="space-y-1">
              {blocked.removal.nested.map((path) => (
                <li key={path} className="truncate font-mono text-hint text-muted-foreground">
                  {path}
                </li>
              ))}
            </ul>
          )}
        </div>
      ) : (
        <div className="space-y-3">
          <p className="text-body">
            {plural(blocked.removal.worktrees.length, "worktree")} still{" "}
            {blocked.removal.worktrees.length === 1 ? "points" : "point"} into this folder. Remove
            them first — each one&apos;s branch stays in this repository.
          </p>
          <ul className="divide-y divide-hairline rounded-lg border border-hairline">
            {blocked.removal.worktrees.map((tree) => (
              <li key={tree.path} className="flex min-w-0 items-center gap-2 px-3 py-2">
                <div className="min-w-0 flex-1 space-y-0.5">
                  <BranchChip branch={tree.branch || "detached"} className="max-w-full" />
                  <p className="truncate font-mono text-hint text-muted-foreground">{tree.path}</p>
                </div>
                {tree.dirty && <span className="shrink-0 text-hint text-warning">changes</span>}
                <Button
                  size="xs"
                  variant="outline"
                  onClick={() => {
                    const main = blocked.repo.path
                    setBlocked(undefined)
                    const repo: GitRepo = {
                      path: tree.path,
                      name: tree.path.split("/").pop() ?? tree.path,
                      branch: tree.branch,
                      dirty: tree.dirty,
                      changes: 0,
                      staged: 0,
                      untracked: 0,
                      conflicts: 0,
                      ahead: 0,
                      behind: 0,
                      detached: !tree.branch,
                      worktree: true,
                      main,
                    }
                    removeWorktree(repo, main)
                  }}
                >
                  <Trash />
                  Remove
                </Button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </Modal>
  )

  return {
    deleteCheckout: (repo: GitRepo) => void deleteCheckout(repo),
    removeWorktree,
    checking,
    dialog: (
      <>
        {dialog}
        {blockedDialog}
      </>
    ),
  }
}

/**
 * What exists only on this disk, counted — the part of the dialog that
 * decides whether the reader goes ahead. A line per kind of thing, each in
 * the hue of what it is, and the folder itself said last because it is always
 * true: ignored files are never on any remote.
 */
function Losses({ removal }: { removal: GitRemoval }) {
  const lines: { icon: typeof Trash; tone: string; text: React.ReactNode }[] = []
  if (removal.remotes === 0 && !removal.worktree) {
    lines.push({
      icon: Warning,
      tone: "text-destructive",
      text: <>No remote — this repository and its whole history exist nowhere else.</>,
    })
  }
  if (removal.changes > 0) {
    lines.push({
      icon: Pencil,
      tone: "text-(--git-modified)",
      text: (
        <>
          {plural(removal.changes, "uncommitted change")}
          {removal.untracked > 0 && <> ({plural(removal.untracked, "new file")} never committed)</>}
        </>
      ),
    })
  }
  if (removal.unpushed > 0) {
    const names = removal.localBranches.map((b) => b.name)
    lines.push({
      icon: CloudUpload,
      tone: "text-warning",
      text: (
        <>
          {plural(removal.unpushed, "commit")} pushed nowhere
          {names.length > 0 && (
            <>
              {" "}
              — on{" "}
              {names.slice(0, 3).map((name, i) => (
                <span key={name}>
                  {i > 0 && ", "}
                  <span className="font-mono">{name}</span>
                </span>
              ))}
              {names.length > 3 && <> and {names.length - 3} more</>}
            </>
          )}
        </>
      ),
    })
  }
  if (removal.stashes > 0) {
    lines.push({
      icon: Archive,
      tone: "text-(--git-untracked)",
      text: <>{plural(removal.stashes, "stash", "stashes")}</>,
    })
  }

  return (
    <div className="space-y-3">
      <Notice tone="danger" icon={Trash} title="This cannot be undone">
        The folder and everything in it is deleted from this server. Nothing goes to a trash, and
        the dashboard keeps no copy.
      </Notice>
      {lines.length > 0 ? (
        <div className="space-y-1.5">
          <p className="eyebrow">Lost forever — it exists only here</p>
          <ul className="space-y-1">
            {lines.map((line, i) => (
              <li key={i} className="flex items-start gap-2 text-body">
                <line.icon aria-hidden className={cn("mt-0.5 size-4 shrink-0", line.tone)} />
                <span>{line.text}</span>
              </li>
            ))}
          </ul>
        </div>
      ) : (
        <p className="flex items-start gap-2 text-body text-muted-foreground">
          <SourceBranch aria-hidden className="mt-0.5 size-4 shrink-0 text-success" />
          Every commit here is on a remote, so a fresh clone brings the history back.
        </p>
      )}
      <p className="text-hint text-muted-foreground">
        Ignored files — <span className="font-mono">.env</span>, build output, local data — are
        never on a remote and go with the folder.
      </p>
    </div>
  )
}
