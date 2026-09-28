"use client"

import { useState } from "react"
import { ClockRewind, Warning } from "@/components/icons"
import { get, post, ApiError, errorMessage } from "@/lib/api"
import { notify } from "@/lib/toast"
import { bytes, plural, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  ProxyHistory,
  ProxyHistoryFiles,
  ProxyRevision,
  ProxyRevisionDetail,
  ProxyValidation,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceRow } from "@/components/flow"
import { DiffView } from "@/components/files/diff-view"
import { unifiedDiff } from "@/components/files/diff"
import { Modal } from "@/components/modal"
import { Pane, Well } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { SidePanel } from "@/components/side-panel"
import {
  EmptyNote,
  EmptyState,
  ErrorState,
  LoadingPanel,
  LoadingRows,
  Notice,
} from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { ProxyGrid } from "@/components/proxy/route-path"
import { relativePath } from "@/components/proxy/config-tree"
import { refusalOf } from "@/components/proxy/engine-lifecycle"

const ACTION_LABEL: Record<string, string> = {
  write: "Saved",
  delete: "Removed",
  enable: "Enabled",
  disable: "Disabled",
  rename: "Renamed",
  restore: "Restored",
  outside: "Changed outside the dashboard",
  baseline: "Before the first recorded change",
}

function actionLabel(revision: ProxyRevision): string {
  return ACTION_LABEL[revision.action] ?? revision.action
}

function when(revision: ProxyRevision): string {
  return relativeTime(new Date(revision.createdAt * 1000).toISOString())
}

/** "Saved by ana · 3m ago": what happened, who asked, and when. */
function revisionLine(revision: ProxyRevision): string {
  const who = revision.actor ? ` by ${revision.actor}` : ""
  return `${actionLabel(revision)}${who} · ${when(revision)}`
}

/**
 * Every file the dashboard changed, newest change first, each saying how it
 * compares to the file on disk now. A removed site's file stays listed while
 * its revisions are kept, which is how it comes back.
 */
export function ConfigHistoryView({
  root,
  onOpen,
}: {
  root: string
  onOpen: (path: string) => void
}) {
  const files = usePoll(
    (signal) => get<ProxyHistoryFiles>("/proxy/history/files", undefined, signal),
    60_000,
  )
  if (files.error) return <ErrorState error={files.error} onRetry={files.refresh} />
  if (!files.data) return <LoadingPanel />
  const list = files.data.files
  if (list.length === 0) {
    return (
      <EmptyState
        icon={ClockRewind}
        title="No changes recorded yet"
        description="Each save, removal and switch the dashboard makes to a configuration file is kept here, with who made it."
      />
    )
  }
  const drifted = list.filter((f) => f.current.drift && f.current.exists).length
  return (
    <div className="min-w-0 space-y-5">
      {drifted > 0 && (
        <Notice tone="warning" icon={Warning} title="Changed outside the dashboard">
          {plural(drifted, "file")} no longer {drifted === 1 ? "holds" : "hold"} what the dashboard
          last wrote. The next change the dashboard makes keeps the edit as a revision of its own;
          open a file to compare it now.
        </Notice>
      )}
      <ProxyGrid className="gap-2" aria-label="Files with a history">
        {list.map((file) => {
          const state = !file.current.exists
            ? file.current.unreadable
              ? { tone: "warning" as const, label: "Unreadable" }
              : { tone: "stopped" as const, label: "Removed" }
            : file.current.drift
              ? { tone: "warning" as const, label: "Changed outside" }
              : undefined
          return (
            <ChoiceRow
              key={file.path}
              onSelect={() => onOpen(file.path)}
              verb={`History of ${relativePath(file.path, root)}`}
              className="min-h-0 gap-3 px-3 py-2.5"
              title={<span className="font-mono">{relativePath(file.path, root)}</span>}
              description={`${revisionLine(file.latest)} · ${plural(file.revisions, "revision")}`}
              trailing={state && <Status tone={state.tone} label={state.label} />}
            />
          )
        })}
      </ProxyGrid>
    </div>
  )
}

type Compare = "previous" | "current"

/**
 * One file's revisions beside a diff of the one picked: against the revision
 * before it, or against the file as it is now — which is what Restore would
 * change. Restore writes the revision through the config editor's own save,
 * so the engine tests it in place and a refusal leaves the file as it was.
 */
export function ConfigHistorySheet({
  path,
  root,
  onOpenChange,
  onRestored,
}: {
  path: string | null
  root: string
  onOpenChange: (open: boolean) => void
  onRestored?: () => void
}) {
  return (
    <SidePanel
      open={path !== null}
      onOpenChange={onOpenChange}
      width="xl"
      title={path ? `History of ${path.split("/").pop()}` : "History"}
      description={path ?? undefined}
      bodyClassName="flex min-h-0 flex-1 flex-col gap-3 p-4"
    >
      {path && <HistoryBody key={path} path={path} root={root} onRestored={onRestored} />}
    </SidePanel>
  )
}

function HistoryBody({
  path,
  root,
  onRestored,
}: {
  path: string
  root: string
  onRestored?: () => void
}) {
  const history = usePoll((signal) => get<ProxyHistory>("/proxy/history", { path }, signal), 0, [
    path,
  ])
  const [picked, setPicked] = useState<number>()
  const [compare, setCompare] = useState<Compare>("previous")
  const [confirming, setConfirming] = useState(false)
  const revisions = history.data?.revisions ?? []
  const selected = picked ?? revisions[0]?.id
  const detail = usePoll(
    (signal) => get<ProxyRevisionDetail>(`/proxy/history/${selected}`, undefined, signal),
    0,
    [selected],
    { enabled: selected !== undefined },
  )

  if (history.error) return <ErrorState error={history.error} onRetry={history.refresh} />
  if (!history.data) return <LoadingRows rows={6} />
  if (revisions.length === 0) {
    return (
      <EmptyNote>
        No revisions of <span className="font-mono">{relativePath(path, root)}</span> are kept.
      </EmptyNote>
    )
  }

  const current = history.data.current
  const shown = detail.data?.revision.id === selected ? detail.data : undefined
  const diff = shown
    ? compare === "previous"
      ? unifiedDiff(shown.previous?.content ?? "", shown.content, path)
      : unifiedDiff(shown.currentContent, shown.content, path)
    : undefined
  const sameAsDisk =
    shown?.current.exists === true && shown.current.sha256 === shown.revision.sha256
  const restorable = shown?.revision.existed === true && !sameAsDisk

  const refresh = () => {
    history.refresh()
    detail.refresh()
    onRestored?.()
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3">
      {current.drift && current.exists && (
        <Notice tone="warning" icon={Warning} title="Changed outside the dashboard">
          The file on disk no longer holds the newest revision here. Compare a revision with the
          file now to see what changed.
        </Notice>
      )}
      {!current.exists && !current.unreadable && (
        <Notice title="The file is not on disk">
          Restore a revision from before its removal to write it back.
        </Notice>
      )}
      <div className="grid min-h-0 flex-1 gap-4 lg:grid-cols-[16rem_minmax(0,1fr)] [&>*]:min-w-0">
        <RowList aria-label="Revisions" className="max-h-64 overflow-y-auto lg:max-h-none">
          {revisions.map((revision) => (
            <li key={revision.id}>
              <Row
                onClick={() => setPicked(revision.id)}
                className={cn("px-3 py-2", revision.id === selected && "bg-accent")}
                title={actionLabel(revision)}
                subtitle={`${revision.actor ? `${revision.actor} · ` : ""}${when(revision)}${
                  revision.existed ? ` · ${bytes(revision.size)}` : ""
                }`}
              />
            </li>
          ))}
        </RowList>

        <div className="flex min-h-0 flex-col gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <ToggleGroup
              type="single"
              value={compare}
              onValueChange={(v) => v && setCompare(v as Compare)}
              variant="outline"
              size="sm"
            >
              <ToggleGroupItem value="previous" className="text-hint">
                Against the one before
              </ToggleGroupItem>
              <ToggleGroupItem value="current" className="text-hint">
                Against the file now
              </ToggleGroupItem>
            </ToggleGroup>
            <span className="flex-1" />
            <Button
              size="sm"
              onClick={() => setConfirming(true)}
              disabled={!restorable}
              title={
                !shown
                  ? undefined
                  : !shown.revision.existed
                    ? "This revision is the file being removed; restore the one before it."
                    : sameAsDisk
                      ? "The file already holds this revision."
                      : undefined
              }
            >
              Restore
            </Button>
          </div>
          {detail.error ? (
            <ErrorState error={detail.error} onRetry={detail.refresh} />
          ) : !shown ? (
            <LoadingRows rows={8} />
          ) : (
            <RevisionDiff
              diff={diff}
              empty={
                compare === "previous"
                  ? shown.previous
                    ? "Nothing in the file changed: the site was switched, or it was saved as it was."
                    : "The first revision kept, so there is nothing before it to compare."
                  : "The file on disk holds exactly this revision."
              }
              className="min-h-64 flex-1"
            />
          )}
        </div>
      </div>

      {shown && (
        <RestoreDialog
          open={confirming}
          onOpenChange={setConfirming}
          detail={shown}
          root={root}
          onRestored={refresh}
        />
      )}
    </div>
  )
}

function RevisionDiff({
  diff,
  empty,
  className,
}: {
  diff: string | null | undefined
  empty: string
  className?: string
}) {
  if (diff === null) {
    return (
      <Notice title="Too many changes to show">
        The two differ in more places than can be lined up here.
      </Notice>
    )
  }
  if (!diff) return <EmptyNote>{empty}</EmptyNote>
  return (
    <Pane className={cn("min-h-0 overflow-hidden", className)}>
      <DiffView body={diff} singleFile lineNumbers className="h-full" />
    </Pane>
  )
}

/**
 * The restore's confirmation shows what it writes: the file now against the
 * revision. The engine's test still decides, and its refusal is drawn here
 * with the test's output, because nothing was written.
 */
function RestoreDialog({
  open,
  onOpenChange,
  detail,
  root,
  onRestored,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  detail: ProxyRevisionDetail
  root: string
  onRestored: () => void
}) {
  const [busy, setBusy] = useState<"only" | "reload">()
  const [refused, setRefused] = useState<ProxyValidation>()
  const { revision } = detail
  const name = relativePath(revision.path, root)
  const diff = unifiedDiff(detail.currentContent, detail.content, revision.path)

  const restore = async (reload: boolean) => {
    setBusy(reload ? "reload" : "only")
    setRefused(undefined)
    try {
      const res = await post<{ validation?: ProxyValidation }>(
        `/proxy/history/${revision.id}/restore`,
        { reload },
      )
      if (res.validation?.note) {
        notify.warning("Restored, but nginx does not read this file", {
          description: res.validation.note,
        })
      } else {
        notify.success(reload ? "Restored and reloaded" : "Restored")
      }
      onOpenChange(false)
      onRestored()
    } catch (err) {
      const refusal = refusalOf(err)
      if (refusal) {
        setRefused(
          refusal.validation ?? {
            valid: false,
            output: refusal.message,
            command: "",
            diagnostics: [],
          },
        )
      } else if (err instanceof ApiError && err.code === "reload_failed") {
        // Written and tested before the reload was tried, so it is restored
        // on disk even though the engine is not serving it yet.
        notify.warning("Restored, not reloaded", { description: errorMessage(err) })
        onOpenChange(false)
        onRestored()
      } else {
        notify.error("Not restored", err)
      }
    } finally {
      setBusy(undefined)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => {
        if (busy) return
        setRefused(undefined)
        onOpenChange(next)
      }}
      size="lg"
      title={`Restore ${name}?`}
      description={`Writes the revision from ${when(revision)} over the file on disk.`}
      footer={
        <>
          <span className="mr-auto text-hint text-muted-foreground">
            The engine tests it first; a file it refuses is left as it was.
          </span>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={busy !== undefined}
          >
            Cancel
          </Button>
          <Button
            variant="outline"
            onClick={() => void restore(false)}
            pending={busy === "only"}
            disabled={busy !== undefined}
          >
            {busy === "only" ? "Restoring…" : "Restore only"}
          </Button>
          <Button
            onClick={() => void restore(true)}
            pending={busy === "reload"}
            disabled={busy !== undefined}
          >
            {busy === "reload" ? "Restoring and reloading…" : "Restore and reload"}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3">
        <p className="text-body leading-relaxed">
          {detail.current.exists ? (
            <>
              What changes in <span className="font-mono">{name}</span>, from the file now to the
              revision ({revisionLine(revision)}):
            </>
          ) : (
            <>
              <span className="font-mono">{name}</span> is not on disk, so the whole revision is
              written back.
              {revision.path.includes("/sites-available/") &&
                " Removing a site also removed its link in sites-enabled, so it comes back disabled until it is enabled again."}
            </>
          )}
        </p>
        <RevisionDiff
          diff={diff}
          empty="The file on disk already holds this revision."
          className="h-[45vh]"
        />
        {refused && (
          <div className="space-y-2">
            <Notice tone="danger" icon={Warning} title="Not restored: the config test refuses it">
              {refused.note && <p>{refused.note}</p>}
            </Notice>
            {refused.output && (
              <Well className="max-h-40 break-words whitespace-pre-wrap">{refused.output}</Well>
            )}
          </div>
        )}
      </div>
    </Modal>
  )
}
