"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import {
  CaptureUpdateAction,
  convertToExcalidrawElements,
  Excalidraw,
  hashElementsVersion,
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
import type { OrderedExcalidrawElement } from "@excalidraw/excalidraw/element/types"
import "@excalidraw/excalidraw/index.css"
import {
  ArrowLeft,
  CloudUpload,
  Database,
  FloppyDisk,
  Plus,
  RefreshClockwise,
  Servers,
  Trash,
} from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { ErrorState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Modal } from "@/components/modal"
import { sectionHref } from "@/components/database/engine"
import { ApiError, del, get, put } from "@/lib/api"
import type { Board, BoardScene, BoardSummary } from "@/lib/boards"
import type { DbFleet, DeployProject } from "@/lib/types"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"

type SaveState = "saved" | "pending" | "saving" | "error" | "unnamed" | "conflict" | "missing"

type Snapshot = {
  elements: readonly OrderedExcalidrawElement[]
  appState: AppState
  files: BinaryFiles
  signature: string
}

// Excalidraw calls onChange for every pointer move and selection, and a scene
// can carry megabytes of image data. Element versions and the appState fields
// a saved file keeps say whether anything worth saving changed; the whole
// scene is serialized only when it is saved.
function signature(elements: Snapshot["elements"], appState: AppState) {
  return `${hashElementsVersion(elements)}:${serializeAsJSON([], appState, {}, "local")}`
}

declare global {
  interface Window {
    EXCALIDRAW_ASSET_PATH?: string
  }
}

if (typeof window !== "undefined") window.EXCALIDRAW_ASSET_PATH = "/excalidraw/"

export default function BoardWorkspace({ id }: { id: number }) {
  const [board, setBoard] = useState<Board | null>(null)
  const [error, setError] = useState<Error | null>(null)

  useEffect(() => {
    if (!Number.isSafeInteger(id) || id < 1) return
    let cancelled = false
    get<Board>(`/boards/${id}`)
      .then((data) => {
        if (!cancelled) setBoard(data)
      })
      .catch((cause) => {
        if (!cancelled) setError(cause instanceof Error ? cause : new Error(String(cause)))
      })
    return () => {
      cancelled = true
    }
  }, [id])

  if (!Number.isSafeInteger(id) || id < 1)
    return (
      <div className="p-6">
        <ErrorState error={new Error("Invalid board address")} />
      </div>
    )
  if (error)
    return (
      <div className="p-6">
        <ErrorState error={error} />
      </div>
    )
  if (!board)
    return (
      <div className="p-6">
        <LoadingPanel />
      </div>
    )
  return <LoadedBoard key={board.id} board={board} />
}

function LoadedBoard({ board }: { board: Board }) {
  const router = useRouter()
  const { can } = useAuth()
  const canWrite = can("service.control")
  const { confirm, dialog } = useConfirm()
  const [name, setName] = useState(board.name)
  const [saveState, setSaveState] = useState<SaveState>("saved")
  const [leaving, setLeaving] = useState<string | null>(null)
  const [insertOpen, setInsertOpen] = useState(false)
  const [projects, setProjects] = useState<DeployProject[]>([])
  const [databases, setDatabases] = useState<DbFleet["connections"]>([])
  const [catalogError, setCatalogError] = useState<Error | null>(null)
  const apiRef = useRef<ExcalidrawImperativeAPI | null>(null)
  const nameRef = useRef(board.name)
  const savedNameRef = useRef(board.name)
  const sceneRef = useRef<Snapshot | null>(null)
  const savedSignatureRef = useRef<string | null>(null)
  const revisionRef = useRef(board.revision)
  const stoppedRef = useRef<"conflict" | "missing" | null>(null)
  const abandonedRef = useRef(false)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const savingRef = useRef<Promise<boolean> | null>(null)
  const errorToastRef = useRef<string | number | undefined>(undefined)
  const cardCountRef = useRef(0)

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

  const flush = useCallback(async (): Promise<boolean> => {
    if (timerRef.current) clearTimeout(timerRef.current)
    // A second caller waits out the save in flight and any save queued behind
    // it; returning early reported success while the latest change was unsent.
    while (savingRef.current) await savingRef.current
    if (stoppedRef.current || abandonedRef.current) return false
    if (!isDirty()) return true
    const title = nameRef.current.trim()
    if (!title) {
      setSaveState("unnamed")
      return false
    }
    const snapshot = sceneRef.current
    setSaveState("saving")
    const task = put<BoardSummary>(`/boards/${board.id}`, {
      name: title,
      // "local" is Excalidraw's file format, and the one that keeps `files`:
      // "database" drops them for a separate image store this server does not
      // have, so every save was refused and images would have been lost.
      scene: snapshot
        ? (JSON.parse(
            serializeAsJSON(snapshot.elements, snapshot.appState, snapshot.files, "local"),
          ) as BoardScene)
        : board.scene,
      revision: revisionRef.current,
    })
      .then((saved) => {
        revisionRef.current = saved.revision
        savedNameRef.current = saved.name
        if (snapshot) savedSignatureRef.current = snapshot.signature
        clearSaveError()
        setSaveState(!isDirty() ? "saved" : nameRef.current.trim() ? "pending" : "unnamed")
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
          errorToastRef.current = notify.error("Board was not saved", cause)
        }
        return false
      })
    savingRef.current = task
    const saved = await task
    if (savingRef.current === task) savingRef.current = null
    return saved
  }, [board.id, board.scene, clearSaveError, isDirty])

  const changed = useCallback(() => {
    if (timerRef.current) clearTimeout(timerRef.current)
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
  }, [flush, isDirty])

  const onChange: NonNullable<ExcalidrawProps["onChange"]> = useCallback(
    (elements, appState, files) => {
      const next = signature(elements, appState)
      // The first call is the scene as Excalidraw restored it. Comparing with
      // the stored JSON instead made every visit a save, which bumped the
      // revision and put anyone else looking at the board into a conflict.
      if (savedSignatureRef.current === null) savedSignatureRef.current = next
      const previous = sceneRef.current?.signature ?? savedSignatureRef.current
      sceneRef.current = { elements, appState, files, signature: next }
      if (canWrite && next !== previous) changed()
    },
    [canWrite, changed],
  )

  useEffect(() => {
    const warn = (event: BeforeUnloadEvent) => {
      if (abandonedRef.current || (!isDirty() && !savingRef.current)) return
      event.preventDefault()
    }
    window.addEventListener("beforeunload", warn)
    return () => {
      window.removeEventListener("beforeunload", warn)
      if (timerRef.current) clearTimeout(timerRef.current)
      if (isDirty()) void flush()
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

  function insertCard(
    kind: "server" | "project" | "database",
    label: string,
    detail: string,
    link: string,
    resourceId?: number,
  ) {
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
    const elements = convertToExcalidrawElements([
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
        label: { text: `${label}\n${detail}`, fontSize: 18 },
      },
    ])
    api.updateScene({
      elements: [...api.getSceneElements(), ...elements],
      captureUpdate: CaptureUpdateAction.IMMEDIATELY,
    })
    setInsertOpen(false)
  }

  // A save that cannot go through used to make Back and card links do
  // nothing at all; now leaving is a choice the operator makes.
  async function go(href: string) {
    if (await flush()) router.push(href)
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
      title: "Delete board",
      description: `Delete “${title}” and all its drawings? This cannot be undone.`,
      confirmLabel: "Delete board",
      action: async () => {
        abandonedRef.current = true
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
        router.push("/boards")
      },
    })
  }

  return (
    <div className="flex h-full min-h-0 min-w-0 flex-col">
      <div className="flex min-h-16 flex-wrap items-center gap-2 border-b border-hairline bg-background px-4 py-2 md:px-6">
        <Button
          variant="ghost"
          size="icon"
          aria-label="Back to boards"
          onClick={() => void go("/boards")}
        >
          <ArrowLeft />
        </Button>
        <Input
          aria-label="Board name"
          value={name}
          maxLength={100}
          readOnly={!canWrite}
          onChange={(event) => {
            setName(event.target.value)
            nameRef.current = event.target.value
            changed()
          }}
          onBlur={restoreName}
          className="max-w-sm min-w-32 flex-1 border-transparent bg-transparent text-base font-semibold"
        />
        <span role="status" className="mr-auto text-xs text-muted-foreground">
          {saveState === "saved" && "Saved on this server"}
          {saveState === "pending" && "Unsaved changes"}
          {saveState === "saving" && "Saving…"}
          {saveState === "error" && "Save failed — try again"}
          {saveState === "unnamed" && "Name the board to save it"}
          {saveState === "conflict" && "Changed in another tab — reload to review"}
          {saveState === "missing" && "This board was deleted — changes can’t be saved"}
        </span>
        {canWrite && (
          <>
            <Button
              variant="outline"
              size="sm"
              disabled={saveState === "conflict" || saveState === "missing"}
              onClick={() => void flush()}
            >
              <FloppyDisk /> Save
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                if (!insertOpen) void loadCatalog()
                setInsertOpen((open) => !open)
              }}
            >
              <Plus /> Add server item
            </Button>
          </>
        )}
        {saveState === "conflict" && (
          <Button variant="outline" size="sm" onClick={() => window.location.reload()}>
            <RefreshClockwise /> Reload
          </Button>
        )}
        {can("destructive") && (
          <Button variant="ghost" size="icon" aria-label="Delete board" onClick={remove}>
            <Trash />
          </Button>
        )}
      </div>
      <div className="relative flex min-h-0 flex-1 overflow-hidden">
        <div className="min-h-0 min-w-0 flex-1" aria-label={`${name} drawing canvas`}>
          <Excalidraw
            initialData={board.scene as unknown as ExcalidrawInitialDataState}
            excalidrawAPI={(api) => {
              apiRef.current = api
            }}
            onChange={onChange}
            onLinkOpen={onLinkOpen}
            name={name}
            theme="dark"
            UIOptions={{ canvasActions: { saveToActiveFile: false } }}
            viewModeEnabled={!canWrite}
          />
        </div>
        {insertOpen && canWrite && (
          <aside
            className="absolute inset-y-0 right-0 z-10 w-full max-w-80 overflow-y-auto border-l border-hairline bg-card p-4 shadow-lg sm:relative sm:shadow-none"
            aria-label="Server items"
          >
            <div className="mb-4 flex items-center justify-between">
              <h2 className="font-semibold">Add from this server</h2>
              <Button variant="ghost" size="sm" onClick={() => setInsertOpen(false)}>
                Close
              </Button>
            </div>
            <p className="mb-4 text-sm text-muted-foreground">
              Add a linked card to the canvas. Open its link to visit the live resource.
            </p>
            {catalogError && (
              <p className="mb-3 text-sm text-warning">
                Some resources are unavailable.{" "}
                <button className="underline" onClick={() => void loadCatalog()}>
                  Retry
                </button>
              </p>
            )}
            <div className="space-y-5">
              <section>
                <h3 className="mb-2 text-xs font-semibold tracking-wide text-muted-foreground uppercase">
                  Host
                </h3>
                <ResourceButton
                  icon={Servers}
                  title="This server"
                  detail="Dashboard overview"
                  onClick={() => insertCard("server", "THIS SERVER", "Dashboard overview", "/")}
                />
              </section>
              <section>
                <h3 className="mb-2 text-xs font-semibold tracking-wide text-muted-foreground uppercase">
                  Deployments
                </h3>
                {projects.length === 0 && (
                  <p className="text-sm text-muted-foreground">No projects yet.</p>
                )}
                {projects.map((project) => (
                  <ResourceButton
                    key={project.id}
                    icon={CloudUpload}
                    title={project.name}
                    detail={project.enabled ? "Active project" : "Paused project"}
                    onClick={() =>
                      insertCard(
                        "project",
                        project.name,
                        project.enabled ? "ACTIVE PROJECT" : "PAUSED PROJECT",
                        `/deploy/${project.id}`,
                        project.id,
                      )
                    }
                  />
                ))}
              </section>
              <section>
                <h3 className="mb-2 text-xs font-semibold tracking-wide text-muted-foreground uppercase">
                  Databases
                </h3>
                {databases.length === 0 && (
                  <p className="text-sm text-muted-foreground">No saved connections yet.</p>
                )}
                {databases.map((database) => (
                  <ResourceButton
                    key={database.id}
                    icon={Database}
                    title={database.name}
                    detail={`${database.driver} · ${database.ok ? "reachable" : "unreachable"}`}
                    onClick={() =>
                      insertCard(
                        "database",
                        database.name,
                        `${database.driver.toUpperCase()} · ${database.ok ? "REACHABLE" : "UNREACHABLE"}`,
                        sectionHref(database.id),
                        database.id,
                      )
                    }
                  />
                ))}
              </section>
            </div>
          </aside>
        )}
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
                if (leaving) router.push(leaving)
              }}
            >
              Leave without saving
            </Button>
          </>
        }
      >
        <p className="text-body leading-relaxed">
          Your latest changes to this board are not on the server. Leaving discards them.
        </p>
      </Modal>
    </div>
  )
}

function ResourceButton({
  icon: Icon,
  title,
  detail,
  onClick,
}: {
  icon: React.ComponentType<{ className?: string }>
  title: string
  detail: string
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full items-start gap-3 rounded-lg px-2 py-2.5 text-left focus-ring transition-colors hover:bg-control-hover"
    >
      <Icon className="mt-0.5 size-4 shrink-0 text-brand" />
      <span className="min-w-0">
        <span className="block truncate text-sm font-medium">{title}</span>
        <span className="block text-xs text-muted-foreground">{detail}</span>
      </span>
    </button>
  )
}
