"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { drainBoardSaves } from "@/lib/board-save"
import { createEditorDraftStore } from "@/lib/editor-drafts"
import {
  CaptureUpdateAction,
  convertToExcalidrawElements,
  Excalidraw,
  hashElementsVersion,
  isLinearElement,
  reconcileElements,
  restoreElements,
  serializeAsJSON,
  viewportCoordsToSceneCoords,
} from "@excalidraw/excalidraw"
import type {
  AppState,
  BinaryFiles,
  ExcalidrawImperativeAPI,
  ExcalidrawInitialDataState,
  ExcalidrawProps,
} from "@excalidraw/excalidraw/types"
import type { RemoteExcalidrawElement } from "@excalidraw/excalidraw/data/reconcile"
import type {
  ExcalidrawElement,
  OrderedExcalidrawElement,
} from "@excalidraw/excalidraw/element/types"
import "@excalidraw/excalidraw/index.css"
import {
  ArrowLeft,
  CloudUpload,
  Cross,
  Database,
  FloppyDisk,
  Plus,
  RefreshClockwise,
  Servers,
  Trash,
} from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { IconAction } from "@/components/icon-action"
import { Page } from "@/components/page"
import { PaneHeader } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Modal } from "@/components/modal"
import { VerbMenu, type Verb } from "@/components/verbs"
import { sectionHref } from "@/components/database/engine"
import { ApiError, del, get, put } from "@/lib/api"
import { dashboardResource, embeddable } from "@/lib/board-embeds"
import { EXCALIDRAW_ASSETS } from "@/lib/vendored"
import { BOARD_FILE_TYPES, shownFileIds } from "@/lib/board-files"
import type { Board, BoardSaved, BoardScene } from "@/lib/boards"
import type { DbFleet, DeployProject } from "@/lib/types"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { BoardMenus } from "@/components/boards/board-menus"
import { listOf } from "@/components/boards/drawing-list"
import { LiveCard, LiveCardProvider } from "@/components/boards/live-card"
import { useBoardFiles } from "@/components/boards/use-board-files"
import { useBoardLibrary } from "@/components/boards/use-board-library"
import { useBoardLive } from "@/components/boards/use-board-live"
import { ProjectFavicon } from "@/components/deploy/project-mark"

type SaveState = "saved" | "pending" | "saving" | "error" | "unnamed" | "conflict" | "missing"

/** The save state as the head bar says it: a dot in the state's colour and its words. */
const SAVE_WORDS: Record<SaveState, { tone: DotTone; label: string }> = {
  saved: { tone: "running", label: "Saved on this server" },
  pending: { tone: "notice", label: "Unsaved changes" },
  saving: { tone: "notice", label: "Saving…" },
  error: { tone: "danger", label: "Save failed" },
  unnamed: { tone: "warning", label: "Name the drawing to save it" },
  conflict: { tone: "warning", label: "Changed in another tab" },
  missing: { tone: "danger", label: "This drawing was deleted — changes can’t be saved" },
}

type Snapshot = {
  elements: readonly OrderedExcalidrawElement[]
  appState: AppState
  files: BinaryFiles
  signature: string
}

type DrawingDraft = {
  board: Board
  name: string
  savedName: string
  scene: Snapshot | null
  savedSignature: string | null
  revision: number
  state: SaveState
  stopped: "conflict" | "missing" | null
}
const drawingDrafts = createEditorDraftStore<DrawingDraft>()

// Excalidraw calls onChange for every pointer move and selection, and a scene
// can carry megabytes of image data. Element versions and the appState fields
// a saved file keeps say whether anything worth saving changed; the whole
// scene is serialized only when it is saved.
function signature(elements: Snapshot["elements"], appState: AppState) {
  return `${hashElementsVersion(elements)}:${serializeAsJSON([], appState, {}, "local")}`
}

/**
 * The scene as it is stored: Excalidraw's file format, with its deleted
 * elements kept and its images left to the file store. A deletion is an
 * element at a newer version; dropped from the save, as Excalidraw's own
 * export drops it, a merge with an editor that had not seen it yet put the
 * element back. The server forgets one a day after it was deleted. A file of
 * a type the store refuses stays in the scene, as every file used to.
 */
function sceneBody(snapshot: Snapshot): BoardScene {
  const scene = JSON.parse(serializeAsJSON([], snapshot.appState, {}, "local")) as BoardScene
  scene.elements = snapshot.elements.map((element) =>
    isLinearElement(element) ? { ...element, lastCommittedPoint: null } : element,
  )
  const shown = shownFileIds(snapshot.elements)
  scene.files = Object.fromEntries(
    Object.entries(snapshot.files).filter(
      ([id, file]) => shown.has(id) && !BOARD_FILE_TYPES.has(file.mimeType),
    ),
  )
  return scene
}

declare global {
  interface Window {
    EXCALIDRAW_ASSET_PATH?: string
  }
}

if (typeof window !== "undefined") window.EXCALIDRAW_ASSET_PATH = EXCALIDRAW_ASSETS

/**
 * One drawing, in the workbench frame the terminal, Files and Git share: one
 * frame held to the window, a head bar, and the canvas under it. The head
 * reads left to right as back, the drawing's name and whether it is saved,
 * then the one verb (Add server item) and a menu for the rest. From `lg` the
 * frame is rounded and stands in the page's gutter; below it the frame is
 * the screen.
 *
 * A drawing that is not there is not a failure: it is said as an empty state
 * with the product's mark and the way back to the drawings.
 */
export default function BoardWorkspace({ id }: { id: number }) {
  const [board, setBoard] = useState<Board | null>(null)
  const [error, setError] = useState<Error | null>(null)
  const [attempt, setAttempt] = useState(0)
  const valid = Number.isSafeInteger(id) && id >= 1

  useEffect(() => {
    if (!valid) return
    let cancelled = false
    drawingDrafts
      .settled(String(id))
      .then(() => get<Board>(`/boards/${id}`))
      .then((data) => {
        if (!cancelled) setBoard(data)
      })
      .catch((cause) => {
        if (cancelled) return
        const draft = drawingDrafts.get(String(id))
        if (draft) {
          const missing = cause instanceof ApiError && cause.code === "board_not_found"
          drawingDrafts.set(
            String(id),
            {
              ...draft,
              state: missing ? "missing" : "error",
              stopped: missing ? "missing" : draft.stopped,
            },
            drawingDrafts.generation(),
          )
          setBoard(draft.board)
        } else setError(cause instanceof Error ? cause : new Error(String(cause)))
      })
    return () => {
      cancelled = true
    }
  }, [id, valid, attempt])

  // An address that names no drawing, whether because the number is no
  // drawing's or because it is no number.
  const absent =
    !valid ||
    (error instanceof ApiError && (error.code === "board_not_found" || error.status === 404))
  if (absent)
    return (
      <Page className="animate-rise">
        <h1 className="sr-only">Drawing</h1>
        <EmptyState
          mark={<ProductLogo id="excalidraw" size="lg" />}
          title="This drawing is not here"
          description="It may have been deleted, or the address is not a drawing's. The drawings that are still there are on the Excalidraw page."
          action={
            <Button size="sm" asChild>
              <Link href="/excalidraw">
                <ArrowLeft />
                Back to drawings
              </Link>
            </Button>
          }
        />
      </Page>
    )
  if (error)
    return (
      <Page className="animate-rise">
        <h1 className="sr-only">Drawing</h1>
        {/* Reading a drawing changes nothing, so it is always worth asking again. */}
        <ErrorState
          error={error}
          retryable
          onRetry={() => {
            setError(null)
            setAttempt((held) => held + 1)
          }}
        />
        <Link
          href="/excalidraw"
          className="inline-flex items-center gap-1.5 self-start rounded-sm text-body font-medium focus-ring hover:underline"
        >
          <ArrowLeft className="size-3.5" />
          Back to drawings
        </Link>
      </Page>
    )
  if (!board)
    return (
      <Page fill className="max-w-none p-2 lg:p-3">
        <h1 className="sr-only">Drawing</h1>
        <LoadingPanel />
      </Page>
    )
  return <LoadedBoard key={board.id} board={board} />
}

function LoadedBoard({ board }: { board: Board }) {
  const router = useRouter()
  const { can } = useAuth()
  const canWrite = can("service.control")
  const { confirm, dialog } = useConfirm()
  const [retained] = useState(() => drawingDrafts.get(String(board.id)))
  const [owner] = useState(() => drawingDrafts.generation())
  const [name, setName] = useState(retained?.name ?? board.name)
  const [saveState, setSaveState] = useState<SaveState>(retained?.state ?? "saved")
  const [leaving, setLeaving] = useState<string | null>(null)
  const [insertOpen, setInsertOpen] = useState(false)
  const [projects, setProjects] = useState<DeployProject[]>([])
  const [databases, setDatabases] = useState<DbFleet["connections"]>([])
  const [catalogError, setCatalogError] = useState<Error | null>(null)
  const apiRef = useRef<ExcalidrawImperativeAPI | null>(null)
  const [api, setApi] = useState<ExcalidrawImperativeAPI | null>(null)
  // Once the live room has been joined, saves merge with the stored scene
  // instead of refusing a revision another editor moved on: the editors are
  // drawing together, and each sees the other's changes as they are made.
  const mergeRef = useRef(false)
  const nameRef = useRef(retained?.name ?? board.name)
  const savedNameRef = useRef(retained?.savedName ?? board.name)
  const sceneRef = useRef<Snapshot | null>(retained?.scene ?? null)
  const savedSignatureRef = useRef<string | null>(retained?.savedSignature ?? null)
  const revisionRef = useRef(retained?.revision ?? board.revision)
  const stoppedRef = useRef<"conflict" | "missing" | null>(retained?.stopped ?? null)
  const abandonedRef = useRef(false)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const savingRef = useRef<Promise<boolean> | null>(null)
  const errorToastRef = useRef<string | number | undefined>(undefined)
  const cardCountRef = useRef(0)
  const files = useBoardFiles(board.id, board.fileIds)
  const onLibraryChange = useBoardLibrary(api, canWrite)

  // sonner's dismiss() with no id clears every toast on the page.
  const clearSaveError = useCallback(() => {
    if (errorToastRef.current === undefined) return
    notify.dismiss(errorToastRef.current)
    errorToastRef.current = undefined
  }, [])

  // Dirty is whatever the server does not have yet, so typing a name back to
  // what it was, or a blank name restored on blur, leaves nothing to save.
  const isDirty = useCallback(
    () =>
      nameRef.current.trim() !== savedNameRef.current ||
      (sceneRef.current !== null && sceneRef.current.signature !== savedSignatureRef.current),
    [],
  )

  const retain = useCallback(
    (state: SaveState) => {
      const key = String(board.id)
      if (abandonedRef.current || !isDirty()) {
        drawingDrafts.forget(key, owner)
        return
      }
      drawingDrafts.set(
        key,
        {
          board,
          name: nameRef.current,
          savedName: savedNameRef.current,
          scene: sceneRef.current,
          savedSignature: savedSignatureRef.current,
          revision: revisionRef.current,
          state,
          stopped: stoppedRef.current,
        },
        owner,
      )
    },
    [board, isDirty, owner],
  )

  // Another editor's changes, from the live room or merged in by a save.
  // They are theirs to save: a drawing that had nothing unsaved before them
  // still has nothing, or every editor in the room would save every change.
  const applyRemote = useCallback(
    (remote: readonly ExcalidrawElement[]) => {
      const api = apiRef.current
      if (!api) return
      const clean =
        sceneRef.current === null || sceneRef.current.signature === savedSignatureRef.current
      api.updateScene({
        elements: reconcileElements(
          api.getSceneElementsIncludingDeleted(),
          restoreElements(remote, null) as RemoteExcalidrawElement[],
          api.getAppState(),
        ),
        captureUpdate: CaptureUpdateAction.NEVER,
      })
      const elements = api.getSceneElementsIncludingDeleted()
      if (clean) savedSignatureRef.current = signature(elements, api.getAppState())
      void files.read(api, elements)
    },
    [files],
  )

  const flush = useCallback(async (): Promise<boolean> => {
    if (timerRef.current) clearTimeout(timerRef.current)
    if (savingRef.current) return savingRef.current
    const task = drainBoardSaves(isDirty, async () => {
      if (owner !== drawingDrafts.generation() || stoppedRef.current || abandonedRef.current)
        return false
      const title = nameRef.current.trim()
      if (!title) {
        setSaveState("unnamed")
        return false
      }
      const snapshot = sceneRef.current
      setSaveState("saving")
      // The images go first: a scene naming a file the server lacks would
      // draw a hole for everyone else until this editor sent it.
      return (snapshot ? files.upload(snapshot.elements, snapshot.files) : Promise.resolve())
        .then(() =>
          put<BoardSaved>(`/boards/${board.id}`, {
            name: title,
            scene: snapshot ? sceneBody(snapshot) : board.scene,
            revision: revisionRef.current,
            ...(mergeRef.current && { merge: true }),
          }),
        )
        .then((saved) => {
          revisionRef.current = saved.revision
          savedNameRef.current = saved.name
          if (snapshot) savedSignatureRef.current = snapshot.signature
          files.settle(saved.fileIds)
          if (saved.remoteElements?.length) applyRemote(saved.remoteElements as ExcalidrawElement[])
          clearSaveError()
          setSaveState(!isDirty() ? "saved" : nameRef.current.trim() ? "pending" : "unnamed")
          retain("pending")
          return true
        })
        .catch((cause) => {
          if (cause instanceof ApiError && cause.code === "board_conflict") {
            stoppedRef.current = "conflict"
            setSaveState("conflict")
          } else if (cause instanceof ApiError && cause.code === "board_not_found") {
            stoppedRef.current = "missing"
            setSaveState("missing")
          } else {
            setSaveState("error")
            clearSaveError()
            errorToastRef.current = notify.error("Drawing was not saved", cause)
          }
          retain(stoppedRef.current ?? "error")
          return false
        })
    })
    savingRef.current = task
    drawingDrafts.track(String(board.id), task, owner)
    try {
      return await task
    } finally {
      if (savingRef.current === task) savingRef.current = null
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [applyRemote, board.id, board.scene, clearSaveError, files, isDirty, owner, retain])

  const changed = useCallback(() => {
    if (timerRef.current) clearTimeout(timerRef.current)
    retain(stoppedRef.current ?? "pending")
    if (stoppedRef.current || abandonedRef.current) return
    if (!isDirty()) {
      if (!savingRef.current) setSaveState("saved")
      return
    }
    if (!nameRef.current.trim()) {
      setSaveState("unnamed")
      return
    }
    setSaveState("pending")
    timerRef.current = setTimeout(() => void flush(), 900)
  }, [flush, isDirty, retain])

  const live = useBoardLive({
    boardId: board.id,
    api,
    canWrite,
    onScene: applyRemote,
    onFiles: () => {
      if (apiRef.current)
        void files.read(apiRef.current, apiRef.current.getSceneElementsIncludingDeleted())
    },
    onDeleted: () => {
      stoppedRef.current = "missing"
      setSaveState("missing")
      retain("missing")
    },
  })
  useEffect(() => {
    if (live.joined) mergeRef.current = true
  }, [live.joined])

  const { broadcast, announceFiles } = live
  const onChange: NonNullable<ExcalidrawProps["onChange"]> = useCallback(
    (elements, appState, held) => {
      const next = signature(elements, appState)
      // The first call is the scene as Excalidraw restored it. Comparing with
      // the stored JSON instead made every visit a save, which bumped the
      // revision and put anyone else looking at the board into a conflict.
      const first = savedSignatureRef.current === null
      if (first) savedSignatureRef.current = next
      const previous = sceneRef.current?.signature ?? savedSignatureRef.current
      sceneRef.current = { elements, appState, files: held, signature: next }
      // The images stored apart are read once the scene they belong to is
      // there; another editor's are read as their elements arrive.
      if (first && apiRef.current) void files.read(apiRef.current, elements)
      if (!canWrite || next === previous) return
      changed()
      broadcast()
      // A failure here is retried by the save, which says so.
      files.upload(elements, held).then(announceFiles, () => {})
    },
    [announceFiles, broadcast, canWrite, changed, files],
  )

  const renderEmbeddable: NonNullable<ExcalidrawProps["renderEmbeddable"]> = useCallback(
    (element) => {
      const resource = dashboardResource(element.link)
      return resource ? <LiveCard resource={resource} href={element.link!} /> : null
    },
    [],
  )

  useEffect(() => {
    // Excalidraw's library site sends an added pack back to the tab of this
    // name, so it lands in the open editor rather than in a second one.
    window.name = "just-dashboard-excalidraw"
  }, [])

  useEffect(() => {
    const warn = (event: BeforeUnloadEvent) => {
      if (abandonedRef.current || (!isDirty() && !savingRef.current)) return
      event.preventDefault()
    }
    window.addEventListener("beforeunload", warn)
    return () => {
      window.removeEventListener("beforeunload", warn)
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [flush, isDirty])

  useEffect(() => {
    // Excalidraw's own Ctrl+S writes a .excalidraw file to disk (it is turned
    // off below); on a board the operator means this server. The physical key
    // stands in on a non-Latin layout, where Russian's S key reports "ы".
    const save = (event: KeyboardEvent) => {
      if (!(event.ctrlKey || event.metaKey) || event.shiftKey || event.altKey) return
      const key = event.key.toLowerCase()
      if (key !== "s" && (/^[a-z]$/.test(key) || event.code !== "KeyS")) return
      event.preventDefault()
      void flush()
    }
    window.addEventListener("keydown", save)
    return () => window.removeEventListener("keydown", save)
  }, [flush])

  const loadCatalog = useCallback(async () => {
    const [projectResult, databaseResult] = await Promise.allSettled([
      get<DeployProject[]>("/deploy/"),
      get<DbFleet>("/databases/fleet"),
    ])
    if (projectResult.status === "fulfilled") {
      setProjects(projectResult.value.filter((project) => !project.archivedAt))
    }
    if (databaseResult.status === "fulfilled") setDatabases(databaseResult.value.connections)
    setCatalogError(
      projectResult.status === "rejected" || databaseResult.status === "rejected"
        ? new Error("Some server resources could not be loaded")
        : null,
    )
  }, [])

  function insertCard(kind: "server" | "project" | "database", link: string, resourceId?: number) {
    const api = apiRef.current
    if (!api || !canWrite) return
    const state = api.getAppState()
    const offset = (cardCountRef.current++ % 5) * 34
    // The middle of what is on screen, in scene units: scroll alone ignored
    // zoom, so a card inserted while zoomed out landed off to one side.
    const center = viewportCoordsToSceneCoords(
      { clientX: state.offsetLeft + state.width / 2, clientY: state.offsetTop + state.height / 2 },
      state,
    )
    // An embed, which `renderEmbeddable` draws as a live card. Excalidraw
    // builds no embed from a skeleton, but one is a rectangle by another type.
    // The colours are the cards' from before, for the list's thumbnail and an
    // exported image, which draw the shape and not the card.
    const [card] = convertToExcalidrawElements([
      {
        type: "rectangle",
        x: center.x - 140 + offset,
        y: center.y - 56 + offset,
        width: 280,
        height: 112,
        backgroundColor: "#1e293b",
        strokeColor: "#a5d8ff",
        fillStyle: "solid",
        roughness: 0,
        link,
        customData: { justDashboard: { kind, resourceId } },
      },
    ])
    api.updateScene({
      elements: [...api.getSceneElements(), { ...card, type: "embeddable" } as ExcalidrawElement],
      captureUpdate: CaptureUpdateAction.IMMEDIATELY,
    })
    setInsertOpen(false)
  }

  // A save that cannot go through used to make Back and card links do
  // nothing at all; now leaving is a choice the operator makes.
  async function go(href: string) {
    if ((await flush()) && !isDirty()) router.push(href)
    else setLeaving(href)
  }

  const onLinkOpen: NonNullable<ExcalidrawProps["onLinkOpen"]> = (element, event) => {
    const link = element.link
    const click = event.detail.nativeEvent
    if (
      link?.startsWith("/") &&
      !link.startsWith("//") &&
      !click.ctrlKey &&
      !click.metaKey &&
      !click.shiftKey
    ) {
      event.preventDefault()
      void go(link)
    }
  }

  function restoreName() {
    const next = nameRef.current.trim() || savedNameRef.current
    if (next === nameRef.current) return
    nameRef.current = next
    setName(next)
    changed()
  }

  async function remove() {
    // Best effort: a save that fails must not keep a board from being deleted,
    // and the confirmation names the saved board, which may differ from an unsaved edit.
    await flush()
    const title = savedNameRef.current
    confirm({
      title: "Delete drawing",
      description: `Delete “${title}” and everything drawn on it? This cannot be undone.`,
      confirmLabel: "Delete drawing",
      action: async () => {
        abandonedRef.current = true
        drawingDrafts.forget(String(board.id), owner)
        if (timerRef.current) clearTimeout(timerRef.current)
        try {
          await del(`/boards/${board.id}`)
        } catch (cause) {
          if (!(cause instanceof ApiError && cause.code === "board_not_found")) {
            abandonedRef.current = false
            changed()
            throw cause
          }
        }
        router.push("/excalidraw")
      },
    })
  }

  const stopped = saveState === "conflict" || saveState === "missing"
  const toggleInsert = () => {
    if (!insertOpen) void loadCatalog()
    setInsertOpen((open) => !open)
  }
  // Everything but the one verb. Saving is in here because the drawing saves
  // itself a moment after every change and on Ctrl+S; the item is for the
  // reader who wants to see it happen.
  const verbs: Verb[] = [
    ...(canWrite
      ? [
          {
            key: "save",
            label: "Save now",
            icon: FloppyDisk,
            disabled: stopped,
            run: () => void flush(),
          },
        ]
      : []),
    { key: "reload", label: "Reload", icon: RefreshClockwise, run: () => window.location.reload() },
    ...(can("destructive")
      ? [{ key: "delete", label: "Delete drawing", icon: Trash, danger: true, run: remove }]
      : []),
  ]
  const said = SAVE_WORDS[saveState]

  return (
    <Page fill className="max-w-none gap-0 p-0 lg:px-3 lg:py-3">
      {/* A workbench draws no page header, so its name is said, not drawn. */}
      <h1 className="sr-only">Drawing: {name.trim() || board.name}</h1>
      <div
        data-slot="drawing-workspace"
        className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-card lg:rounded-xl lg:border"
      >
        {/* Two rows on a phone, on purpose: back, the name and the verbs, then
            the save state under the name. Wrapped wherever the next control
            ran out of room, the status landed between the buttons. */}
        <PaneHeader className="grid min-h-12 grid-cols-[auto_minmax(0,1fr)_auto] gap-x-1.5 gap-y-0 px-2 py-1.5 sm:flex">
          <IconAction label="Back to Excalidraw" onClick={() => void go("/excalidraw")}>
            <ArrowLeft />
          </IconAction>
          <span className="flex min-w-0 items-center gap-1.5">
            <ProductLogo id="excalidraw" size="sm" className="max-sm:hidden" />
            <Input
              aria-label="Drawing name"
              value={name}
              maxLength={100}
              readOnly={!canWrite}
              onChange={(event) => {
                setName(event.target.value)
                nameRef.current = event.target.value
                changed()
              }}
              onBlur={restoreName}
              // As wide as the name, so the save state stands beside it
              // rather than a field's width away.
              className={cn(
                "[field-sizing:content] h-8 w-auto max-w-full min-w-24 border-transparent bg-transparent px-2 text-body font-semibold shadow-none sm:max-w-80",
                // A name that cannot be edited is a name, not a field.
                canWrite ? "hover:border-input" : "dark:bg-transparent",
              )}
            />
          </span>
          {/* As tall as the button a failed save puts in it, so the bar is
              one height whatever the state says. */}
          <span className="flex min-w-0 items-center gap-x-3 max-sm:order-last max-sm:col-span-3 max-sm:h-6 max-sm:pl-10 sm:mr-auto sm:pl-1">
            <span role="status" className="flex min-w-0">
              <Status tone={said.tone} label={said.label} className="min-w-0 font-normal" />
            </span>
            {saveState === "error" && canWrite && (
              <Button variant="outline" size="xs" onClick={() => void flush()}>
                Try again
              </Button>
            )}
            {live.peers.length > 0 && (
              <Status
                tone="running"
                live
                label={`Live with ${listOf(live.peers.map((p) => p.name || p.user))}`}
                className="min-w-0 font-normal text-muted-foreground"
              />
            )}
          </span>
          <span className="flex shrink-0 items-center gap-1.5">
            {/* Nothing added to a drawing another tab changed could be saved,
                so until it is read again the one verb is to read it again. */}
            {saveState === "conflict" ? (
              <Button size="sm" onClick={() => window.location.reload()}>
                <RefreshClockwise />
                Reload
              </Button>
            ) : (
              canWrite && (
                <Button
                  size="sm"
                  aria-label="Add server item"
                  aria-expanded={insertOpen}
                  onClick={toggleInsert}
                >
                  <Plus />
                  <span className="max-sm:hidden">Add server item</span>
                  <span className="sm:hidden">Add</span>
                </Button>
              )
            )}
            <VerbMenu label="Drawing actions" verbs={verbs} />
          </span>
        </PaneHeader>
        <div className="relative flex min-h-0 flex-1 overflow-hidden">
          <div className="min-h-0 min-w-0 flex-1" aria-label={`${name} drawing canvas`}>
            <LiveCardProvider open={(href) => void go(href)}>
              <Excalidraw
                initialData={
                  (retained?.scene
                    ? JSON.parse(
                        serializeAsJSON(
                          retained.scene.elements,
                          retained.scene.appState,
                          retained.scene.files,
                          "local",
                        ),
                      )
                    : board.scene) as ExcalidrawInitialDataState
                }
                excalidrawAPI={(next) => {
                  apiRef.current = next
                  setApi(next)
                }}
                onChange={onChange}
                onLinkOpen={onLinkOpen}
                onLibraryChange={onLibraryChange}
                onPointerUpdate={live.onPointerUpdate}
                isCollaborating={live.peers.length > 0}
                validateEmbeddable={embeddable}
                renderEmbeddable={renderEmbeddable}
                name={name}
                theme="dark"
                UIOptions={{ canvasActions: { saveToActiveFile: false } }}
                viewModeEnabled={!canWrite}
              >
                <BoardMenus
                  canWrite={canWrite}
                  onSave={() => void flush()}
                  onAddServerItem={() => {
                    void loadCatalog()
                    setInsertOpen(true)
                  }}
                  onDescribe={() =>
                    apiRef.current?.updateScene({
                      appState: { openDialog: { name: "ttd", tab: "text-to-diagram" } },
                    })
                  }
                />
              </Excalidraw>
            </LiveCardProvider>
          </div>
          {insertOpen && canWrite && (
            // A column beside the canvas from `sm`; on a phone it takes the
            // canvas's place, since a 320px panel left a strip of canvas no
            // finger could draw on.
            <aside
              className="absolute inset-0 z-10 flex min-h-0 flex-col border-hairline bg-card sm:relative sm:inset-auto sm:w-80 sm:shrink-0 sm:border-l"
              aria-label="Server items"
            >
              <PaneHeader className="h-10 py-0 pl-3">
                <h2 className="min-w-0 flex-1 truncate text-body font-medium">
                  Add from this server
                </h2>
                <IconAction label="Close" onClick={() => setInsertOpen(false)}>
                  <Cross />
                </IconAction>
              </PaneHeader>
              <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-2">
                {catalogError && (
                  <p className="flex flex-wrap items-center gap-x-2 gap-y-1 px-2 pt-1 text-hint text-warning">
                    Some of this server could not be read.
                    <Button variant="outline" size="xs" onClick={() => void loadCatalog()}>
                      Try again
                    </Button>
                  </p>
                )}
                <section>
                  <h3 className="eyebrow px-2 pt-1 pb-1.5">Host</h3>
                  <ResourceButton
                    icon={Servers}
                    title="This server"
                    detail="Dashboard overview"
                    onClick={() => insertCard("server", "/")}
                  />
                </section>
                <section>
                  <h3 className="eyebrow px-2 pt-1 pb-1.5">Deployments</h3>
                  {projects.length === 0 && (
                    <p className="px-2 text-hint text-muted-foreground">No projects yet</p>
                  )}
                  {projects.map((project) => (
                    <ResourceButton
                      key={project.id}
                      icon={CloudUpload}
                      projectId={project.id}
                      title={project.name}
                      detail={project.enabled ? "Active project" : "Paused project"}
                      onClick={() => insertCard("project", `/deploy/${project.id}`, project.id)}
                    />
                  ))}
                </section>
                <section>
                  <h3 className="eyebrow px-2 pt-1 pb-1.5">Databases</h3>
                  {databases.length === 0 && (
                    <p className="px-2 text-hint text-muted-foreground">No saved connections yet</p>
                  )}
                  {databases.map((database) => (
                    <ResourceButton
                      key={database.id}
                      icon={Database}
                      title={database.name}
                      detail={`${database.driver} · ${database.ok ? "reachable" : "unreachable"}`}
                      onClick={() => insertCard("database", sectionHref(database.id), database.id)}
                    />
                  ))}
                </section>
              </div>
            </aside>
          )}
        </div>
      </div>
      {dialog}
      <Modal
        open={leaving !== null}
        onOpenChange={(open) => !open && setLeaving(null)}
        title="Leave without saving?"
        size="sm"
        footer={
          <>
            <Button variant="outline" onClick={() => setLeaving(null)}>
              Stay
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                abandonedRef.current = true
                drawingDrafts.forget(String(board.id), owner)
                if (leaving) router.push(leaving)
              }}
            >
              Leave without saving
            </Button>
          </>
        }
      >
        <p className="text-body leading-relaxed">
          Your latest changes to this drawing are not on the server. Leaving discards them.
        </p>
      </Modal>
    </Page>
  )
}

function ResourceButton({
  icon: Icon,
  projectId,
  title,
  detail,
  onClick,
}: {
  icon: React.ComponentType<{ className?: string }>
  /** A project, drawn by its website's icon where it has one. */
  projectId?: number
  title: string
  detail: string
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full items-center gap-3 rounded-md px-2 py-2 text-left focus-ring-inset transition-colors hover:bg-row-hover"
    >
      <ProjectFavicon
        projectId={projectId}
        className="size-4"
        fallback={<Icon className="size-4 shrink-0 text-muted-foreground" />}
      />
      <span className="min-w-0">
        <span className="block truncate text-body font-medium">{title}</span>
        <span className="block truncate text-hint text-muted-foreground">{detail}</span>
      </span>
    </button>
  )
}
