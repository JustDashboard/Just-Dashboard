"use client"

import { useState } from "react"
import Link from "next/link"
import { errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { GitWorktree } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { Modal } from "@/components/modal"
import { Field, FieldRow } from "@/components/form"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { SourceBranch } from "@/components/git/glyphs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Checkbox } from "@/components/ui/checkbox"

export function WorktreesDialog({
  open,
  onOpenChange,
  repoPath,
  canControl,
  canDestruct,
  canTerminal,
  confirm,
  onChanged,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  repoPath: string
  canControl: boolean
  canDestruct: boolean
  canTerminal: boolean
  confirm: (request: ConfirmRequest) => void
  onChanged: () => void
}) {
  const [parent, setParent] = useState(repoPath.slice(0, repoPath.lastIndexOf("/")) || "/")
  const [name, setName] = useState("")
  const [branch, setBranch] = useState("")
  const [from, setFrom] = useState("HEAD")
  const [create, setCreate] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const trees = usePoll(
    (signal) => get<GitWorktree[]>("/git/worktrees", { path: repoPath }, signal),
    0,
    [repoPath],
    { enabled: open },
  )
  const add = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await post(
        "/git/worktree",
        {
          parent: parent.trim(),
          name: name.trim(),
          branch: branch.trim(),
          from: from.trim(),
          create,
        },
        { query: { path: repoPath } },
      )
      notify.success("Worktree created")
      setName("")
      setBranch("")
      trees.refresh()
      onChanged()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  const remove = (tree: GitWorktree) =>
    confirm({
      title: `Remove ${tree.branch || "detached worktree"}`,
      description: (
        <p>
          The checkout at <span className="font-mono break-all">{tree.path}</span> is removed. The
          branch and commits are kept. Git refuses if the checkout contains uncommitted work.
        </p>
      ),
      confirmLabel: "Remove worktree",
      action: async () => {
        await post("/git/worktree/remove", { path: tree.path }, { query: { path: repoPath } })
        trees.refresh()
        onChanged()
      },
    })
  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title="Worktrees"
      size="lg"
      footer={
        <Button variant="ghost" disabled={busy} onClick={() => onOpenChange(false)}>
          Close
        </Button>
      }
    >
      <div className="space-y-4">
        {trees.error && <ErrorState error={trees.error} />}
        {trees.loading && !trees.data && <LoadingRows rows={3} />}
        {trees.data?.length === 0 && <EmptyState icon={SourceBranch} title="No worktrees" />}
        <ul className="divide-y divide-hairline">
          {trees.data?.map((tree) => (
            <li key={tree.path} className="flex flex-wrap items-center gap-2 py-2">
              <div className="min-w-0 flex-1">
                <p className="truncate font-mono text-body">{tree.branch || "detached HEAD"}</p>
                <p className="truncate font-mono text-hint text-muted-foreground" title={tree.path}>
                  {tree.path}
                </p>
              </div>
              {tree.current && <Tag>current</Tag>}
              {tree.main && <Tag>main checkout</Tag>}
              {tree.locked && <Tag>locked</Tag>}
              {tree.accessible ? (
                <>
                  {!tree.current && (
                    <Button size="xs" variant="ghost" asChild>
                      <Link
                        href={`/git?repo=${encodeURIComponent(tree.path)}`}
                        onClick={() => onOpenChange(false)}
                      >
                        Open
                      </Link>
                    </Button>
                  )}
                  {canTerminal && (
                    <Button size="xs" variant="ghost" asChild>
                      <Link href={`/terminal?cwd=${encodeURIComponent(tree.path)}`}>Terminal</Link>
                    </Button>
                  )}
                  {canDestruct && !tree.main && !tree.current && !tree.locked && (
                    <Button
                      size="xs"
                      variant="ghost"
                      className="text-destructive"
                      disabled={busy}
                      onClick={() => remove(tree)}
                    >
                      Remove
                    </Button>
                  )}
                </>
              ) : (
                <Tag>outside roots or missing</Tag>
              )}
            </li>
          ))}
        </ul>
        {canControl && (
          <form
            className="space-y-3 border-t border-hairline pt-3"
            onSubmit={(e) => {
              e.preventDefault()
              void add()
            }}
          >
            {error && (
              <Notice tone="danger" title="Could not create the worktree">
                {error}
              </Notice>
            )}
            <Field
              label="Parent folder"
              htmlFor="worktree-parent"
              hint="An existing directory within the configured Git roots."
            >
              <Input
                id="worktree-parent"
                value={parent}
                onChange={(e) => setParent(e.target.value)}
                required
                className="font-mono"
              />
            </Field>
            <FieldRow>
              <Field label="Folder name" htmlFor="worktree-name">
                <Input
                  id="worktree-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  required
                  placeholder="hotfix"
                  className="font-mono"
                />
              </Field>
              <Field label="Branch" htmlFor="worktree-branch">
                <Input
                  id="worktree-branch"
                  value={branch}
                  onChange={(e) => setBranch(e.target.value)}
                  required
                  placeholder="fix/the-thing"
                  className="font-mono"
                />
              </Field>
            </FieldRow>
            <label className="flex items-center gap-2 text-body">
              <Checkbox
                checked={create}
                onCheckedChange={(checked) => setCreate(checked === true)}
              />
              Create a new branch
            </label>
            {create && (
              <Field label="Start from" htmlFor="worktree-from">
                <Input
                  id="worktree-from"
                  value={from}
                  onChange={(e) => setFrom(e.target.value)}
                  required
                  className="font-mono"
                />
              </Field>
            )}
            <Button
              size="sm"
              type="submit"
              disabled={
                busy || !parent.trim() || !name.trim() || !branch.trim() || (create && !from.trim())
              }
              pending={busy}
            >
              Create worktree
            </Button>
          </form>
        )}
      </div>
    </Modal>
  )
}
