"use client"

import { useState } from "react"
import { ApiError, errorMessage, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { GitPullBlocked, GitResult } from "@/lib/types"
import { FormNote } from "@/components/form"
import { Modal } from "@/components/modal"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"

/** What a refused pull was blocked on, or nothing for any other failure. */
export function pullBlocked(err: unknown): GitPullBlocked | undefined {
  if (!(err instanceof ApiError) || err.code !== "pull_blocked") return undefined
  return (err.body as { blocked?: GitPullBlocked } | undefined)?.blocked
}

type Way = "own" | "stash" | "discard"

/**
 * The ways past a pull that did not start.
 *
 * It replaces a toast holding git's paragraph — "Your local changes to the
 * following files would be overwritten by merge", a list, "Please commit your
 * changes or stash them" — which told the operator what to type in a shell
 * they did not have open. Each way is one request that does the step and the
 * pull together, and the server works out again what is in the way, so the
 * list here is only what it showed. A step that clears one obstacle can meet
 * the next — a folder given back, then the files a half-finished pull left in
 * it — and the dialog turns into that one rather than closing on a failure.
 *
 * Discard is destructive and is offered beside the list of exactly what it
 * throws away, which is the confirmation; stash keeps everything.
 */
export function PullBlockedDialog({
  repoPath,
  blocked,
  onClose,
  onChanged,
}: {
  repoPath: string
  blocked: GitPullBlocked
  onClose: () => void
  onChanged: () => void
}) {
  const [current, setCurrent] = useState(blocked)
  const [busy, setBusy] = useState<Way>()
  const [error, setError] = useState<string>()

  const take = async (way: Way) => {
    setBusy(way)
    setError(undefined)
    try {
      const res = await post<GitResult>(`/git/pull/${way}`, undefined, {
        query: { path: repoPath },
      })
      notify.success("Pulled", { description: res.output?.split("\n").slice(0, 3).join("\n") })
      onClose()
    } catch (err) {
      const next = pullBlocked(err)
      if (next) setCurrent(next)
      else setError(errorMessage(err))
    } finally {
      setBusy(undefined)
      onChanged()
    }
  }

  const owner = current.owner || "this checkout's owner"
  const unwritable = current.unwritable ?? []
  const changed = current.changed ?? []
  const untracked = current.untracked ?? []
  const holders = [...new Set(unwritable.map((dir) => dir.owner))].join(" and ")

  return (
    <Modal
      open
      onOpenChange={(open) => !open && !busy && onClose()}
      title={unwritable.length > 0 ? "The pull would stop halfway" : "Local changes are in the way"}
      size="md"
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={!!busy}>
            Cancel
          </Button>
          {unwritable.length > 0 ? (
            <Button onClick={() => void take("own")} disabled={!!busy} pending={busy === "own"}>
              Give them to {owner} and pull
            </Button>
          ) : (
            <>
              <Button
                variant="destructive"
                onClick={() => void take("discard")}
                disabled={!!busy}
                pending={busy === "discard"}
              >
                Discard and pull
              </Button>
              <Button
                onClick={() => void take("stash")}
                disabled={!!busy}
                pending={busy === "stash"}
              >
                Stash and pull
              </Button>
            </>
          )}
        </>
      }
    >
      <div className="space-y-3">
        {error && (
          <Notice title="git refused" tone="danger">
            {error}
          </Notice>
        )}
        {unwritable.length > 0 ? (
          <>
            <p className="text-body">
              Git runs here as <span className="font-medium">{owner}</span>, and the pull writes
              into {unwritable.length === 1 ? "a folder that belongs" : "folders that belong"} to{" "}
              <span className="font-medium">{holders}</span>. It would write the files it could and
              stop at the first it couldn&apos;t, so nothing has been pulled yet.
            </p>
            <PathList label={plural(unwritable.length, "folder")}>
              {unwritable.map((dir) => (
                <li key={dir.path} className="flex min-w-0 items-baseline gap-3 px-3 py-1.5">
                  <span className="min-w-0 flex-1 truncate font-mono">{dir.path}</span>
                  <span className="shrink-0 text-muted-foreground">{dir.owner}</span>
                </li>
              ))}
            </PathList>
            <FormNote>
              Folders like these are left by something run here as another account, such as a
              command run with sudo.
            </FormNote>
          </>
        ) : (
          <>
            <p className="text-body">
              The pull brings new versions of files that are different here, and git won&apos;t
              overwrite them. Nothing has been pulled yet.
            </p>
            {changed.length > 0 && (
              <PathList label={`Changed here · ${changed.length}`}>
                {changed.map((path) => (
                  <PathRow key={path} path={path} />
                ))}
              </PathList>
            )}
            {untracked.length > 0 && (
              <PathList label={`Untracked, where the pull adds files · ${untracked.length}`}>
                {untracked.map((path) => (
                  <PathRow key={path} path={path} />
                ))}
              </PathList>
            )}
            <FormNote>
              Stash and pull puts every local change aside, to bring back from Stashes. Discard and
              pull throws away the changes to these files for good.
            </FormNote>
          </>
        )}
      </div>
    </Modal>
  )
}

function PathList({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <section className="space-y-1.5">
      <p className="eyebrow">{label}</p>
      <ul className="max-h-48 divide-y divide-hairline overflow-y-auto rounded-lg border border-hairline text-hint">
        {children}
      </ul>
    </section>
  )
}

function PathRow({ path }: { path: string }) {
  return <li className="truncate px-3 py-1.5 font-mono">{path}</li>
}
