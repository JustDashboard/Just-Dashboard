"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import {
  CaptureUpdateAction,
  convertToExcalidrawElements,
  Excalidraw,
  serializeAsJSON,
} from "@excalidraw/excalidraw"
import type {
  ExcalidrawImperativeAPI,
  ExcalidrawInitialDataState,
  ExcalidrawProps,
} from "@excalidraw/excalidraw/types"
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
import { ApiError, del, get, put } from "@/lib/api"
import type { Board, BoardScene, BoardSummary } from "@/lib/boards"
import type { DbFleet, DeployProject } from "@/lib/types"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"

type SaveState = "saved" | "pending" | "saving" | "error" | "conflict"

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
  const [insertOpen, setInsertOpen] = useState(false)
  const [projects, setProjects] = useState<DeployProject[]>([])
  const [databases, setDatabases] = useState<DbFleet["connections"]>([])
  const [catalogError, setCatalogError] = useState<Error | null>(null)
  const apiRef = useRef<ExcalidrawImperativeAPI | null>(null)
  const nameRef = useRef(board.name)
  const sceneRef = useRef<BoardScene>(board.scene)
  const serializedRef = useRef(JSON.stringify(board.scene))
  const revisionRef = useRef(board.revision)
  const dirtyRef = useRef(false)
  const conflictRef = useRef(false)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const savingRef = useRef<Promise<boolean> | null>(null)
  const deletedRef = useRef(false)
  const cardCountRef = useRef(0)

  const flush = useCallback(async (): Promise<boolean> => {
    if (timerRef.current) clearTimeout(timerRef.current)
    if (savingRef.current) await savingRef.current
    if (conflictRef.current) return false
    if (!dirtyRef.current) return true
    dirtyRef.current = false
    setSaveState("saving")
    const scene = sceneRef.current
    const title = nameRef.current
    const task = put<BoardSummary>(`/boards/${board.id}`, {
      name: title,
      scene,
      revision: revisionRef.current,
    })
      .then((saved) => {
        revisionRef.current = saved.revision
        if (!dirtyRef.current) setSaveState("saved")
        return true
      })
      .catch((cause) => {
        dirtyRef.current = true
        if (cause instanceof ApiError && cause.code === "board_conflict") {
          conflictRef.current = true
          setSaveState("conflict")
        } else {
          setSaveState("error")
          notify.error("Board was not saved", cause)
        }
        return false
      })
    savingRef.current = task
    const saved = await task
    savingRef.current = null
    return saved
  }, [board.id])

  const changed = useCallback(() => {
    dirtyRef.current = true
    setSaveState("pending")
    if (timerRef.current) clearTimeout(timerRef.current)
    if (!conflictRef.current) timerRef.current = setTimeout(() => void flush(), 900)
  }, [flush])

  const onChange: NonNullable<ExcalidrawProps["onChange"]> = useCallback(
    (elements, appState, files) => {
      if (!canWrite) return
      const serialized = serializeAsJSON(elements, appState, files, "database")
      if (serialized === serializedRef.current) return
      serializedRef.current = serialized
      sceneRef.current = JSON.parse(serialized) as BoardScene
      changed()
    },
    [canWrite, changed],
  )

  useEffect(() => {
    const warn = (event: BeforeUnloadEvent) => {
      if (!dirtyRef.current && !savingRef.current) return
      event.preventDefault()
    }
    window.addEventListener("beforeunload", warn)
    return () => {
      window.removeEventListener("beforeunload", warn)
      if (timerRef.current) clearTimeout(timerRef.current)
      if (!deletedRef.current && dirtyRef.current && !conflictRef.current) void flush()
    }
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
    const elements = convertToExcalidrawElements([
      {
        type: "rectangle",
        x: -state.scrollX + 120 + offset,
        y: -state.scrollY + 110 + offset,
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
      void flush().then((saved) => {
        if (saved) router.push(link)
      })
    }
  }

  async function leave() {
    if (await flush()) router.push("/boards")
  }

  function remove() {
    confirm({
      title: "Delete board",
      description: `Delete “${name}” and all its drawings? This cannot be undone.`,
      phrase: name,
      confirmLabel: "Delete board",
      action: async (confirm) => {
        if (!(await flush())) throw new Error("Save the latest board changes before deleting it")
        await del(`/boards/${board.id}`, { confirm })
        deletedRef.current = true
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
          onClick={() => void leave()}
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
          className="max-w-sm min-w-32 flex-1 border-transparent bg-transparent text-base font-semibold"
        />
        <span role="status" className="mr-auto text-xs text-muted-foreground">
          {saveState === "saved" && "Saved on this server"}
          {saveState === "pending" && "Unsaved changes"}
          {saveState === "saving" && "Saving…"}
          {saveState === "error" && "Save failed — try again"}
          {saveState === "conflict" && "Changed in another tab — reload to review"}
        </span>
        {canWrite && (
          <>
            <Button variant="outline" size="sm" onClick={() => void flush()}>
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
                        `/databases/overview?conn=${database.id}`,
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
