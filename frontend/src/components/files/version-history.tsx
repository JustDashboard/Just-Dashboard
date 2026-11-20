"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import {
  Archive,
  ClockRewind,
  CloudDownload,
  Download,
  Pencil,
  Shield,
  Terminal,
} from "@/components/icons"
import { ApiError, downloadUrl, get, post, shaped } from "@/lib/api"
import { bytes, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { BackupJob, FileContent } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { Button } from "@/components/ui/button"
import { IconAction } from "@/components/icon-action"
import { Pane, PaneHeader } from "@/components/panel"
import { PaneBack, paneShown } from "@/components/phone-panes"
import { ProductLogo } from "@/components/product-logo"
import { RailLine, RailName, RailRow, railInk } from "@/components/rail"
import { Segments } from "@/components/segments"
import { EmptyNote, EmptyState, ErrorState, Notice } from "@/components/state"
import { LoadingRows, Spinner } from "@/components/loading"
import { destinationProduct } from "@/components/backups/marks"
import { isImage } from "@/components/files/media"
import { VersionDiff } from "@/components/files/version-diff"
import type {
  FileVersion,
  FileVersionContent,
  FileVersions,
} from "@/components/files/versions-types"
import {
  pickedVersion,
  previousVersion,
  versionLine,
  versionName,
  whenAgo,
} from "@/components/files/versions"

export type HistoryPane = "versions" | "compare"
export type HistoryAgainst = "current" | "previous"

/**
 * A file's history as a view of the editor page (`?view=history`): its
 * earlier contents on the left, newest first — what each save through the
 * dashboard replaced and each backup run that holds a copy — and the one
 * picked compared on the right, against the file as it is now or the version
 * before it, with Restore on it. The address carries the pick
 * (`&version=e12`), so a comparison is a link.
 *
 * Text is Monaco's diff; a picture is the two pictures side by side; any
 * other binary offers download and restore. A backup's copy on another
 * machine is fetched only on a press, which says it is fetching.
 *
 * The editor stays mounted beside this, hidden, so a draft survives a look
 * here. A restore is a save of its own: what it replaces is kept, so it can
 * be undone from this list.
 */
export function VersionHistory({
  path,
  versionId,
  against,
  pane,
  onPick,
  onAgainst,
  onPane,
  onRestored,
  refreshTick,
  hrefFor,
}: {
  path: string
  /** The address of a version in the history, which a row links to. */
  hrefFor: (version: string) => string
  versionId: string | null
  against: HistoryAgainst
  /** Which of the two panes a phone shows. */
  pane: HistoryPane
  onPick: (version: string) => void
  onAgainst: (against: HistoryAgainst) => void
  onPane: (pane: HistoryPane) => void
  /** A version was put back: the editor reloads what is on disk if it holds no draft. */
  onRestored: () => void
  /** Moves when the file is saved from the editor, so the list reads again. */
  refreshTick: number
}) {
  const [history, setHistory] = useState<FileVersions>()
  const [error, setError] = useState<Error>()
  const [current, setCurrent] = useState<FileContent | null>()
  const [currentError, setCurrentError] = useState<Error>()
  const [reload, setReload] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    get<unknown>("/files/versions", { path }, controller.signal)
      .then((answer) => {
        setHistory(shaped<FileVersions>(answer, "versions", "the file's versions"))
        setError(undefined)
      })
      .catch((err) => !controller.signal.aborted && setError(err))
    get<FileContent>("/files/read", { path }, controller.signal)
      .then((next) => {
        setCurrent(next)
        setCurrentError(undefined)
      })
      .catch((err) => {
        if (controller.signal.aborted) return
        // A file that is gone compares against nothing and restores as new.
        if (err instanceof ApiError && err.status === 404) setCurrent(null)
        else setCurrentError(err)
      })
    return () => controller.abort()
  }, [path, refreshTick, reload])

  const picked = pickedVersion(history, versionId)
  const previous = picked ? previousVersion(history, picked) : undefined

  return (
    <div data-version-history className="flex min-h-0 min-w-0 flex-1">
      <Pane
        flush
        aria-label="Versions"
        className={cn(
          "border-hairline max-lg:flex-1 lg:w-80 lg:shrink-0 lg:border-r",
          paneShown(pane === "versions"),
        )}
      >
        <PaneHeader className="justify-between">
          <h2 className="text-body font-medium">Versions</h2>
          {history && (
            <span className="numeric text-hint text-muted-foreground">
              {history.versions.length}
            </span>
          )}
        </PaneHeader>
        <VersionList
          history={history}
          error={error}
          picked={picked}
          hrefFor={hrefFor}
          onRetry={() => setReload((v) => v + 1)}
          onPick={(id) => {
            onPick(id)
            onPane("compare")
          }}
        />
      </Pane>
      <Pane
        flush
        aria-label="Compare"
        className={cn("min-w-0 flex-1", paneShown(pane === "compare"))}
      >
        {picked ? (
          <Compare
            key={picked.id}
            path={path}
            version={picked}
            previous={previous}
            against={against}
            onAgainst={onAgainst}
            current={current}
            currentError={currentError}
            onBack={() => onPane("versions")}
            onRestored={() => {
              setReload((v) => v + 1)
              onRestored()
            }}
          />
        ) : (
          <>
            <PaneHeader>
              <PaneBack label="Back to the versions" onClick={() => onPane("versions")} />
              <h2 className="text-body font-medium">Compare</h2>
            </PaneHeader>
            <div className="flex min-h-0 flex-1 items-center justify-center p-6">
              {error ? (
                <ErrorState error={error} />
              ) : !history ? (
                <Spinner className="size-5 text-muted-foreground" />
              ) : (
                <EmptyState
                  icon={ClockRewind}
                  title="No earlier versions yet"
                  description={emptyReason(history)}
                />
              )}
            </div>
          </>
        )}
      </Pane>
    </div>
  )
}

function emptyReason(history: FileVersions) {
  if (history.tooLarge) return "The file is too large for its earlier contents to be kept."
  if (!history.keeping) return "This server keeps no earlier contents of saved files."
  return "Each save through the dashboard keeps what it replaced, and backups that hold the file appear here."
}

function VersionList({
  history,
  error,
  picked,
  hrefFor,
  onRetry,
  onPick,
}: {
  history?: FileVersions
  error?: Error
  picked?: FileVersion
  hrefFor: (version: string) => string
  onRetry: () => void
  onPick: (id: string) => void
}) {
  if (error && !history)
    return <ErrorState error={error} onRetry={onRetry} retryable className="m-3" />
  if (!history) return <LoadingRows rows={6} className="p-3" />
  return (
    <ol
      aria-label="Earlier versions"
      className="min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto p-2"
    >
      {history.versions.length === 0 && (
        <EmptyNote as="li" dense>
          {history.tooLarge
            ? "Too large to keep earlier versions."
            : history.keeping
              ? "Nothing yet."
              : "Earlier versions are not kept."}
        </EmptyNote>
      )}
      {history.versions.map((version) => (
        <VersionRow
          key={version.id}
          version={version}
          current={version.id === picked?.id}
          href={hrefFor(version.id)}
          onPick={onPick}
        />
      ))}
    </ol>
  )
}

function VersionRow({
  version,
  current,
  href,
  onPick,
}: {
  version: FileVersion
  current: boolean
  href: string
  onPick: (id: string) => void
}) {
  const name = versionName(version)
  return (
    <RailRow
      item={version.id}
      name={name}
      title={timestamp(version.at)}
      current={current}
      href={href}
      onOpen={() => onPick(version.id)}
      mark={<VersionMark version={version} />}
      end={
        version.kind === "edit" && version.size !== undefined ? (
          <span className="numeric font-mono text-hint text-muted-foreground">
            {bytes(version.size)}
          </span>
        ) : version.backup && !version.backup.local ? (
          <CloudDownload
            aria-label="On another machine"
            className="size-3.5 text-muted-foreground"
          />
        ) : undefined
      }
    >
      <RailName current={current}>{name}</RailName>
      <RailLine className="numeric" ink={railInk(version.outside ? "warning" : undefined)}>
        {versionLine(version, whenAgo(version.at))}
      </RailLine>
    </RailRow>
  )
}

/** A version drawn as what made it: an edit, a change from outside, or the backup's destination. */
function VersionMark({ version }: { version: FileVersion }) {
  if (version.kind === "backup" && version.backup) {
    const product = destinationProduct({
      targetKind: version.backup.targetKind,
      target: { endpoint: version.backup.endpoint },
    } as BackupJob)
    return <ProductLogo id={product} size="sm" fallback={Archive} />
  }
  return <ProductLogo size="sm" fallback={version.outside ? Terminal : Pencil} />
}

type Loaded =
  | { state: "idle" }
  | { state: "loading" }
  | { state: "ready"; content: FileVersionContent }
  | { state: "failed"; error: Error }

/** Whether reading a version stays on this server, so it may be read without a press. */
const readsHere = (version: FileVersion) =>
  version.kind === "edit" || Boolean(version.backup?.local)

/**
 * One version's text, read when it is on this server and on a press when it
 * is not. A picture is not read here: it is drawn from its address.
 */
function useVersionText(path: string, version: FileVersion | undefined, enabled: boolean) {
  // Kept with the version it is of, so a pick that moves on reads as loading
  // until its own answer lands rather than showing the last one's.
  const [loaded, setLoaded] = useState<{ id: string; result: Loaded }>()
  const [asked, setAsked] = useState(false)
  const want = Boolean(version) && enabled && (asked || (version ? readsHere(version) : false))
  const id = version?.id
  useEffect(() => {
    if (!want || !id) return
    const controller = new AbortController()
    get<FileVersionContent>("/files/versions/content", { path, version: id }, controller.signal)
      .then((content) => setLoaded({ id, result: { state: "ready", content } }))
      .catch(
        (error) =>
          !controller.signal.aborted && setLoaded({ id, result: { state: "failed", error } }),
      )
    return () => controller.abort()
  }, [want, path, id])
  const result: Loaded =
    !want || !id ? { state: "idle" } : loaded?.id === id ? loaded.result : { state: "loading" }
  return { loaded: result, fetch: () => setAsked(true) }
}

function versionUrl(path: string, version: FileVersion, as: "image" | "download") {
  return downloadUrl("/files/versions/content", { path, version: version.id, as })
}

function Compare({
  path,
  version,
  previous,
  against,
  onAgainst,
  current,
  currentError,
  onBack,
  onRestored,
}: {
  path: string
  version: FileVersion
  previous?: FileVersion
  against: HistoryAgainst
  onAgainst: (against: HistoryAgainst) => void
  current: FileContent | null | undefined
  currentError?: Error
  onBack: () => void
  onRestored: () => void
}) {
  const { can } = useAuth()
  const picture = isImage(path)
  const base = against === "previous" ? previous : undefined
  // A backup's copy leaves its archive only for an administrator, as the
  // archive's own download does. Anyone else may still restore it in place.
  const opens = (v: FileVersion | undefined) => !v || v.kind === "edit" || can("system.admin")
  const sealed = !opens(version)
  const mine = useVersionText(path, version, !picture && !sealed)
  const theirs = useVersionText(path, base, !picture && against === "previous" && opens(base))
  const [fetched, setFetched] = useState(readsHere(version))
  const [restoring, setRestoring] = useState(false)
  const [conflict, setConflict] = useState(false)
  const restoreRef = useRef(false)

  const canRestore =
    can("file.write") && (version.kind === "edit" || can("destructive")) && current !== undefined
  const restore = useCallback(async () => {
    if (restoreRef.current || current === undefined) return
    restoreRef.current = true
    setRestoring(true)
    setConflict(false)
    try {
      await post(
        version.kind === "edit" ? "/files/versions/restore" : "/files/versions/restore-backup",
        { path, version: version.id, expectedRevision: current?.revision ?? "" },
      )
      notify.success("Version restored", {
        description: "What it replaced is kept as a version.",
      })
      onRestored()
    } catch (err) {
      if (err instanceof ApiError && err.code === "content_conflict") setConflict(true)
      else notify.error("Could not restore", err)
    } finally {
      restoreRef.current = false
      setRestoring(false)
    }
  }, [current, onRestored, path, version])

  const remote = !readsHere(version) && !fetched
  const where = version.backup?.destination
  const head = (
    <PaneHeader className="gap-2">
      <PaneBack label="Back to the versions" onClick={onBack} />
      <h2 className="min-w-0 flex-1 truncate text-body font-medium" title={timestamp(version.at)}>
        {versionName(version)}
      </h2>
      {!picture && (
        <Segments
          size="inline"
          label="Compare with"
          value={against}
          onChange={onAgainst}
          options={[
            { value: "current", label: "Now" },
            { value: "previous", label: "Before it", disabled: !previous },
          ]}
        />
      )}
      {!sealed && (fetched || readsHere(version)) && (
        <IconAction label="Download this version" className="size-8" asChild>
          <a href={versionUrl(path, version, "download")} download>
            <Download />
          </a>
        </IconAction>
      )}
      {canRestore && (
        <Button
          size="sm"
          onClick={() => void restore()}
          disabled={restoring || (remote && !sealed)}
          aria-label="Restore this version"
        >
          <ClockRewind />
          <span className="max-sm:hidden">{restoring ? "Restoring…" : "Restore"}</span>
        </Button>
      )}
    </PaneHeader>
  )

  let body: React.ReactNode
  if (sealed) {
    body = (
      <EmptyState
        icon={Shield}
        title="Only an administrator can open a backup's copy"
        description={
          canRestore
            ? "You can still restore it over the file. What it replaces is kept as a version."
            : "It is listed here so you know it exists."
        }
      />
    )
  } else if (remote) {
    body = (
      <EmptyState
        icon={CloudDownload}
        title={`Held in ${where || "a backup"} on another machine`}
        description="Comparing or restoring it fetches the run's archive from there."
        action={
          <Button
            size="sm"
            variant="outline"
            onClick={() => {
              setFetched(true)
              mine.fetch()
            }}
          >
            <CloudDownload />
            Fetch this copy
          </Button>
        }
      />
    )
  } else if (picture) {
    body = (
      <div className="grid min-h-0 flex-1 grid-cols-2 gap-px overflow-auto bg-border">
        <Picture label={versionName(version)} src={versionUrl(path, version, "image")} />
        <Picture
          label="Now"
          src={
            current === null
              ? undefined
              : downloadUrl("/files/raw", { path, r: current?.revision?.slice(0, 16) })
          }
        />
      </div>
    )
  } else {
    const left = against === "previous" ? theirs.loaded : mine.loaded
    const right =
      against === "previous"
        ? mine.loaded
        : current === undefined
          ? ({ state: "loading" } as Loaded)
          : ({
              state: "ready",
              content: {
                content: current?.content ?? "",
                binary: Boolean(current?.binary),
                size: current?.size ?? 0,
                revision: current?.revision ?? "",
              },
            } as Loaded)
    const failed = [left, right].find((side) => side.state === "failed")
    if (failed && failed.state === "failed") body = <Failure error={failed.error} />
    else if (currentError && against === "current") body = <Failure error={currentError} />
    else if (base && !opens(base))
      body = (
        <EmptyState
          icon={Shield}
          className="m-6"
          title="The version before it is a backup's copy"
          description="Only an administrator can open one. Compare with the file as it is now instead."
        />
      )
    else if (base && !readsHere(base) && theirs.loaded.state === "idle")
      body = (
        <EmptyState
          icon={CloudDownload}
          className="m-6"
          title={`The version before it is in ${base.backup?.destination || "a backup"}`}
          description="Comparing with it fetches that run's archive from another machine."
          action={
            <Button size="sm" variant="outline" onClick={theirs.fetch}>
              <CloudDownload />
              Fetch it
            </Button>
          }
        />
      )
    else if (left.state !== "ready" || right.state !== "ready")
      body = (
        <div className="flex min-h-0 flex-1 items-center justify-center gap-2 text-hint text-muted-foreground">
          <Spinner className="size-4" />
          {!readsHere(version) ? `Fetching from ${where || "the backup"}…` : null}
        </div>
      )
    else if (left.content.binary || right.content.binary)
      body = (
        <EmptyState
          icon={Archive}
          className="m-6"
          title="Not text"
          description="This version can be downloaded or restored, but not compared here."
        />
      )
    else
      body = (
        <VersionDiff
          original={left.content.content}
          modified={right.content.content}
          language={current?.language}
        />
      )
  }

  return (
    <>
      {head}
      {conflict && (
        <div className="shrink-0 p-3">
          <Notice tone="warning" live="alert" title="The file changed since this was opened">
            Nothing was restored. Compare again with the file as it is now, then restore.
          </Notice>
        </div>
      )}
      <p className="sr-only" aria-live="polite">
        {restoring ? "Restoring the version" : ""}
      </p>
      <div className="flex min-h-0 flex-1 flex-col">{body}</div>
    </>
  )
}

function Failure({ error }: { error: Error }) {
  if (error instanceof ApiError && error.status === 410)
    return (
      <EmptyState
        icon={Archive}
        className="m-6"
        title="Not in this backup"
        description={error.message}
      />
    )
  return <ErrorState error={error} className="m-6" />
}

function Picture({ label, src }: { label: string; src?: string }) {
  return (
    <figure className="flex min-h-0 min-w-0 flex-col bg-card">
      <figcaption className="truncate px-3 py-2 text-hint text-muted-foreground">
        {label}
      </figcaption>
      <div className="flex min-h-0 flex-1 items-center justify-center p-3">
        {src ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={src} alt={label} className="max-h-full max-w-full object-contain" />
        ) : (
          <span className="text-hint text-muted-foreground">No file here now</span>
        )}
      </div>
    </figure>
  )
}
