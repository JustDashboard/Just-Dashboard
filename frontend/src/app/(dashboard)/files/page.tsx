"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useSearchParams } from "next/navigation"
import {
  ArrowMove,
  ArrowUp,
  ChevronDown,
  ChevronUp,
  Clipboard,
  CloudUpload,
  Cross,
  FileZip,
  FolderOpen,
  FolderPlus,
  GridSquare,
  Linked,
  ListUnordered,
  MagnifyingGlass,
  Monorepo,
  Plus,
  PlusSquareSmall,
  PreviewDocument,
  SettingsSliders,
  SidebarLeft,
  SidebarRight,
  Trash,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { API_BASE, del, downloadUrl, get, mutationHeaders, post, put } from "@/lib/api"
import { truncateMiddle } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileBookmark, FileEntry, FileListing, FilePlaces } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Page, PageHeader } from "@/components/page"
import { Panel, PanelBody, PanelToolbar } from "@/components/panel"
import { GitHubAccountControl } from "@/components/git/github-account"
import { FileEditorSheet } from "@/components/files/file-editor"
import { FileRow } from "@/components/files/file-row"
import { FileTree } from "@/components/files/file-tree"
import { GridView } from "@/components/files/grid-view"
import { ImageEditorSheet } from "@/components/files/image-editor"
import { PathBar } from "@/components/files/path-bar"
import { Modal } from "@/components/modal"
import { PermissionsDialog } from "@/components/files/permissions-dialog"
import { PlacesRail } from "@/components/files/places-rail"
import { PreviewPanel } from "@/components/files/preview-panel"
import { QuickOpen } from "@/components/files/quick-open"
import { isImage, type FileActions } from "@/components/files/file-actions"
import { usePrompt } from "@/components/files/prompt-dialog"
import { EmptyNote, EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Input } from "@/components/ui/input"
import { Checkbox } from "@/components/ui/checkbox"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

type SortKey = "name" | "size" | "modified" | "owner" | "mode"
type ViewMode = "list" | "grid" | "tree"
type Clip = { mode: "cut" | "copy"; paths: string[] }

const clean = (p: string) => p.replace(/\/+$/, "") || "/"
const join = (dir: string, name: string) => `${clean(dir)}/${name}`.replace(/\/{2,}/g, "/")
const parentOf = (p: string) => {
  const c = clean(p)
  const i = c.lastIndexOf("/")
  return i <= 0 ? "/" : c.slice(0, i)
}
const baseOf = (p: string) => clean(p).split("/").pop() ?? p

/** A non-colliding "<name> copy" for duplicating into a directory. */
function uniqueName(name: string, taken: Set<string>): string {
  const dot = name.lastIndexOf(".")
  const stem = dot > 0 ? name.slice(0, dot) : name
  const ext = dot > 0 ? name.slice(dot) : ""
  let candidate = `${stem} copy${ext}`
  let n = 2
  while (taken.has(candidate)) candidate = `${stem} copy ${n++}${ext}`
  return candidate
}

export default function FilesPage() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const { prompt, dialog: promptDialog } = usePrompt()
  const canWrite = can("file.write")
  const canDestruct = can("destructive")
  const canAdmin = can("system.admin")
  const caps = useMemo(
    () => ({ write: canWrite, destruct: canDestruct, admin: canAdmin }),
    [canWrite, canDestruct, canAdmin],
  )

  // Where the machine says to start. The page opens at home rather than at
  // "/", which is the one directory on a Linux server where nothing an
  // operator owns lives — every visit used to begin with the same two clicks
  // past bin, boot, dev and proc. A ?path= from a deep link still wins.
  const initialPath = useSearchParams().get("path")
  const places = usePoll<FilePlaces>((signal) => get("/files/places", undefined, signal), 0, [])
  // Derived rather than copied into state by an effect: until either the URL
  // or a navigation has said otherwise, the answer *is* whatever the server
  // reports as home, and syncing that into state would render one frame of a
  // directory nobody asked for.
  const [chosenPath, setChosenPath] = useState<string | null>(
    initialPath ? clean(initialPath) : null,
  )
  const path = chosenPath ?? places.data?.home ?? null

  const [view, setView] = useViewState<ViewMode>("files.view", "list")
  const [tile, setTile] = useViewState<"sm" | "md" | "lg">("files.tile", "md")
  const [showHidden, setShowHidden] = useViewState("files.hidden", false)
  const [showRail, setShowRail] = useViewState("files.rail", true)
  const [showPreview, setShowPreview] = useViewState("files.preview", true)
  const [sort, setSort] = useViewState<{ key: SortKey; dir: "asc" | "desc" }>("files.sort", {
    key: "name",
    dir: "asc",
  })
  // Where you have just been. Kept in the browser rather than on the server:
  // unlike a starred folder, which is a fact about the machine, this is a
  // property of the last ten minutes at this screen.
  const [recent, setRecent] = useViewState<string[]>("files.recent", [])

  const [editing, setEditing] = useState<string | null>(null)
  const [editingImage, setEditingImage] = useState<string | null>(null)
  const [permsEntry, setPermsEntry] = useState<FileEntry | null>(null)
  const [symlinkOpen, setSymlinkOpen] = useState(false)
  const [quickOpen, setQuickOpen] = useState(false)
  const [clip, setClip] = useState<Clip | null>(null)
  const [dragOver, setDragOver] = useState(false)
  // The selection and the previewed row are scoped to the directory they were
  // made in, so navigating away discards both without a reset effect.
  const [selection, setSelection] = useState<{ dir: string; paths: Set<string> }>({
    dir: path ?? "/",
    paths: new Set(),
  })
  const [active, setActive] = useState<{ dir: string; entry: FileEntry } | null>(null)
  const selected = selection.dir === path ? [...selection.paths] : []
  const activeEntry = active && active.dir === path ? active.entry : null
  const uploadRef = useRef<HTMLInputElement>(null)

  const listing = usePoll(
    (signal) =>
      path === null
        ? Promise.resolve(null as unknown as FileListing)
        : get<FileListing>("/files/list", { path, hidden: showHidden }, signal),
    0,
    [path, showHidden],
  )
  const refresh = listing.refresh

  useEffect(() => {
    if (!path) return
    setRecent((prev) => [path, ...prev.filter((p) => p !== path)].slice(0, 8))
  }, [path, setRecent])

  // Directories first, then the chosen column. Keeping folders grouped is what
  // every file manager does and what makes a long listing navigable.
  const entries = useMemo(() => {
    const list = [...(listing.data?.entries ?? [])]
    const factor = sort.dir === "asc" ? 1 : -1
    list.sort((a, b) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
      let cmp = 0
      switch (sort.key) {
        case "size":
          cmp = a.size - b.size
          break
        case "modified":
          cmp = new Date(a.modified).getTime() - new Date(b.modified).getTime()
          break
        case "owner":
          cmp = `${a.owner}:${a.group}`.localeCompare(`${b.owner}:${b.group}`)
          break
        case "mode":
          cmp = a.modeOctal.localeCompare(b.modeOctal)
          break
        default:
          cmp = a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: "base" })
      }
      return cmp * factor
    })
    return list
  }, [listing.data, sort])

  const clearSelection = () => setSelection({ dir: path ?? "/", paths: new Set() })

  const navigate = useCallback((next: string) => {
    setChosenPath(clean(next))
    setActive(null)
  }, [])

  const upload = async (files: FileList | File[] | null) => {
    const arr = files ? Array.from(files) : []
    if (arr.length === 0 || !path) return
    const form = new FormData()
    for (const file of arr) form.append("file", file)
    try {
      const res = await fetch(
        `${API_BASE}/files/upload?path=${encodeURIComponent(path)}&overwrite=true`,
        { method: "POST", credentials: "include", headers: mutationHeaders(), body: form },
      )
      if (!res.ok) throw new Error((await res.json()).error?.message ?? res.statusText)
      notify.success(`Uploaded ${arr.length} file${arr.length === 1 ? "" : "s"}`)
      refresh()
    } catch (err) {
      notify.error("Upload failed", err)
    } finally {
      if (uploadRef.current) uploadRef.current.value = ""
    }
  }

  // --- operations ---

  const openEntry = useCallback(
    (entry: FileEntry) => {
      if (entry.isDir) {
        navigate(entry.path)
      } else if (canWrite && isImage(entry.name)) {
        // A picture opens in the picture editor rather than in a code editor
        // that would only refuse it.
        setEditingImage(entry.path)
      } else {
        setEditing(entry.path)
      }
    },
    [canWrite, navigate],
  )

  const rename = (entry: FileEntry) =>
    prompt({
      title: `Rename ${entry.name}`,
      label: "New name",
      initial: entry.name,
      confirmLabel: "Rename",
      selectBasename: true,
      validate: (v) => (v.includes("/") ? "A name cannot contain a slash." : undefined),
      hint: (v) => join(path ?? "/", v || "…"),
      submit: async (name) => {
        await post("/files/move", { from: entry.path, to: join(path ?? "/", name) })
        notify.success(`Renamed to ${name}`)
        refresh()
      },
    })

  const duplicate = async (entry: FileEntry) => {
    const taken = new Set((listing.data?.entries ?? []).map((e) => e.name))
    const name = uniqueName(entry.name, taken)
    try {
      await post("/files/copy", { from: entry.path, to: join(path ?? "/", name) })
      notify.success(`Duplicated as ${name}`)
      refresh()
    } catch (err) {
      notify.error("Could not duplicate", err)
    }
  }

  const extract = async (entry: FileEntry) => {
    try {
      const res = await post<{ extracted: number }>("/files/extract", {
        archive: entry.path,
        destination: path,
      })
      notify.success(`Extracted ${res.extracted} item${res.extracted === 1 ? "" : "s"}`)
      refresh()
    } catch (err) {
      notify.error("Could not extract", err)
    }
  }

  const deleteEntry = (entry: FileEntry) =>
    confirm({
      title: entry.isDir ? "Delete folder" : "Delete file",
      // Only a directory is typed for. Deleting one file is what a file
      // manager is, done constantly; a directory takes a tree the operator
      // cannot see the whole of from the row they clicked, and that is the one
      // worth reading the name back for.
      phrase: entry.isDir ? entry.name : undefined,
      confirmLabel: "Delete",
      description: (
        <p className="text-destructive">
          <b>{entry.path}</b> is deleted permanently
          {entry.isDir ? ", along with everything inside it." : "."}
        </p>
      ),
      action: async (c) => {
        await del("/files/delete", {
          confirm: c,
          query: { path: entry.path, recursive: entry.isDir },
        })
        setActive(null)
        refresh()
      },
    })

  const bulkDelete = () =>
    confirm({
      title: `Delete ${selected.length} item${selected.length === 1 ? "" : "s"}`,
      confirmLabel: "Delete all",
      description: (
        <p className="text-destructive">
          {selected.length} selected item{selected.length === 1 ? " is" : "s are"} deleted
          permanently, folders and everything inside them included.
        </p>
      ),
      action: async () => {
        let failed = 0
        for (const p of selected) {
          try {
            await del("/files/delete", { confirm: baseOf(p), query: { path: p, recursive: true } })
          } catch {
            failed++
          }
        }
        clearSelection()
        setActive(null)
        refresh()
        if (failed) notify.error(`${failed} item(s) could not be deleted`)
      },
    })

  const paste = async () => {
    if (!clip || !path) return
    const taken = new Set((listing.data?.entries ?? []).map((e) => e.name))
    let ok = 0
    let failed = 0
    for (const src of clip.paths) {
      const base = baseOf(src)
      const srcParent = parentOf(src)
      try {
        if (clip.mode === "cut") {
          if (srcParent === path) continue // already here
          await post("/files/move", { from: src, to: path })
        } else {
          // Copy keeps the name, unless that would collide (same folder, or an
          // existing entry) — then it becomes a "copy".
          const name = taken.has(base) ? uniqueName(base, taken) : base
          await post("/files/copy", { from: src, to: join(path, name) })
          taken.add(name)
        }
        ok++
      } catch {
        failed++
      }
    }
    if (clip.mode === "cut") setClip(null)
    clearSelection()
    refresh()
    if (failed) notify.error(`${failed} item(s) could not be pasted`)
    else if (ok) notify.success(`${clip.mode === "cut" ? "Moved" : "Copied"} ${ok} item(s) here`)
  }

  const newFolder = () =>
    prompt({
      title: "New folder",
      placeholder: "Folder name",
      confirmLabel: "Create",
      hint: (v) => join(path ?? "/", v || "…"),
      submit: async (name) => {
        await post("/files/mkdir", { path: join(path ?? "/", name) })
        notify.success(`Created ${name}`)
        refresh()
      },
    })

  const newFile = () =>
    prompt({
      title: "New file",
      placeholder: "File name",
      confirmLabel: "Create",
      hint: (v) => join(path ?? "/", v || "…"),
      submit: async (name) => {
        await post("/files/touch", { path: join(path ?? "/", name) })
        notify.success(`Created ${name}`)
        refresh()
        setEditing(join(path ?? "/", name))
      },
    })

  const cutCopy = (mode: "cut" | "copy", paths: string[]) => {
    if (paths.length === 0) return
    setClip({ mode, paths })
    notify.success(
      `${mode === "cut" ? "Cut" : "Copied"} ${paths.length} item(s) — paste in any folder`,
    )
  }

  const saveBookmarks = async (next: FileBookmark[]) => {
    try {
      await put("/files/bookmarks", { bookmarks: next })
      places.refresh()
    } catch (err) {
      notify.error("Could not save your places", err)
    }
  }

  const actionsFor = useCallback(
    (entry: FileEntry): FileActions => ({
      onOpen: () => openEntry(entry),
      onRename: () => rename(entry),
      onDuplicate: () => void duplicate(entry),
      onCopy: () => cutCopy("copy", [entry.path]),
      onCut: () => cutCopy("cut", [entry.path]),
      onExtract: () => void extract(entry),
      onPermissions: () => setPermsEntry(entry),
      onDelete: () => deleteEntry(entry),
      onEditImage: () => setEditingImage(entry.path),
      onBookmark: () =>
        void saveBookmarks([
          ...(places.data?.bookmarks ?? []),
          { path: entry.path, name: entry.name },
        ]),
    }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [openEntry, listing.data, places.data, path],
  )

  // The page's own shortcuts. They are deliberately few and they all match
  // something people already have in their hands: Ctrl+P is every editor's
  // "go to file", F2 renames as it does in every file manager since Norton
  // Commander, and Escape gets you out of a selection.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null
      const typing =
        target?.tagName === "INPUT" ||
        target?.tagName === "TEXTAREA" ||
        target?.isContentEditable === true
      // A dialog open anywhere owns the keyboard; a shortcut firing behind one
      // acts on a page the operator cannot see.
      if (document.querySelector("[role='dialog']")) return

      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "p") {
        event.preventDefault()
        setQuickOpen(true)
        return
      }
      if (typing) return
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "a") {
        event.preventDefault()
        setSelection({ dir: path ?? "/", paths: new Set(entries.map((e) => e.path)) })
        return
      }
      if (event.key === "Escape") {
        setSelection({ dir: path ?? "/", paths: new Set() })
        setActive(null)
      } else if (event.key === "F2" && activeEntry && canWrite) {
        event.preventDefault()
        rename(activeEntry)
      } else if (event.key === "Delete" && activeEntry && canDestruct) {
        event.preventDefault()
        deleteEntry(activeEntry)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeEntry, canWrite, canDestruct, entries, path])

  const clipPaths = useMemo(() => new Set(clip?.paths ?? []), [clip])
  // The tree starts at a permitted root rather than at "/": an install that
  // narrowed JD_FILE_ROOTS would otherwise open the rail on a directory the
  // server refuses to list — and the tree caches what it fetched, so that
  // first refusal is what it keeps showing. It waits for the answer instead.
  const treeRoot = places.data?.roots[0]
  // The rail shows the tree only when the tree is not the main view, and the
  // places list is sized against that.
  const railTree = view === "tree" ? undefined : treeRoot

  return (
    <Page fill>
      <PageHeader
        eyebrow="Access"
        title="Files"
        actions={
          <>
            {/* Edits made here are committed somewhere else, so the account
                those commits will carry belongs on this page too. */}
            <GitHubAccountControl />
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="outline" size="sm" onClick={() => setQuickOpen(true)}>
                  <MagnifyingGlass className="size-4" />
                  Find
                  <kbd className="ml-1 hidden rounded-sm border border-hairline px-1 text-micro text-muted-foreground sm:inline">
                    ⌃P
                  </kbd>
                </Button>
              </TooltipTrigger>
              <TooltipContent>Fuzzy-find a file or folder under here</TooltipContent>
            </Tooltip>
            <SearchDialog
              path={path ?? "/"}
              onOpen={(p, isDir) => (isDir ? navigate(p) : setEditing(p))}
            />
            <ViewSwitcher view={view} onChange={setView} />
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant={showRail ? "secondary" : "outline"}
                  size="icon-sm"
                  aria-label="Toggle the places and folder rail"
                  onClick={() => setShowRail((v) => !v)}
                >
                  <SidebarLeft className="size-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>
                {showRail ? "Hide the rail" : "Show places and folders"}
              </TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant={showPreview ? "secondary" : "outline"}
                  size="icon-sm"
                  aria-label="Toggle the preview pane"
                  onClick={() => setShowPreview((v) => !v)}
                >
                  <SidebarRight className="size-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>
                {showPreview ? "Hide the preview" : "Show the preview pane"}
              </TooltipContent>
            </Tooltip>
            {selected.length > 0 ? (
              <SelectionActions
                count={selected.length}
                canWrite={canWrite}
                canDestruct={canDestruct}
                archiveHref={
                  downloadUrl("/files/archive", { base: path ?? "/", format: "tar.gz" }) +
                  selected.map((p) => `&path=${encodeURIComponent(p)}`).join("")
                }
                onCopy={() => cutCopy("copy", selected)}
                onCut={() => cutCopy("cut", selected)}
                onDelete={bulkDelete}
                onClear={clearSelection}
              />
            ) : (
              canWrite && (
                <>
                  <NewMenu
                    onFolder={newFolder}
                    onFile={newFile}
                    onSymlink={() => setSymlinkOpen(true)}
                  />
                  <Button size="sm" onClick={() => uploadRef.current?.click()}>
                    <CloudUpload className="size-4" />
                    Upload
                  </Button>
                  <input
                    ref={uploadRef}
                    type="file"
                    multiple
                    hidden
                    onChange={(e) => upload(e.target.files)}
                  />
                </>
              )
            )}
          </>
        }
      />

      <div className="flex min-h-0 flex-1 gap-3">
        {showRail && (
          <Panel className="hidden w-64 shrink-0 lg:flex">
            {/* Both halves of the rail need a *bounded* height or neither can
                scroll: an overflow-y-auto box that is free to grow simply
                grows, and the panel's own overflow-hidden then clips whatever
                did not fit with no way to reach it. So places takes at most
                half the rail when the tree is under it, and all of it when it
                is not. */}
            <PlacesRail
              places={places.data}
              path={path ?? "/"}
              recent={recent}
              canWrite={canWrite}
              onNavigate={navigate}
              onBookmarksChange={(next) => void saveBookmarks(next)}
              className={cn(
                railTree ? "max-h-[50%] shrink-0 border-b border-hairline" : "min-h-0 flex-1",
              )}
            />
            {/* No tree in the rail when the tree *is* the view: the same
                control twice on one screen reads as a rendering bug. */}
            <div className="flex min-h-0 flex-1 flex-col">
              {railTree && (
                <FileTree
                  root={railTree}
                  statusMap={{}}
                  canWrite={canWrite}
                  canDelete={canDestruct}
                  activeDir={path ?? undefined}
                  onNavigate={navigate}
                  onOpenFile={(p) => setEditing(p)}
                  onConfirm={(req) =>
                    confirm({
                      title: req.title,
                      description: req.body,
                      confirmLabel: req.confirmLabel,
                      action: async () => {
                        await req.run()
                      },
                    })
                  }
                  onChanged={refresh}
                />
              )}
            </div>
          </Panel>
        )}

        <Panel
          className={cn("min-w-0 flex-1", dragOver && "ring-2 ring-primary")}
          onDragOver={(e) => {
            if (!canWrite) return
            e.preventDefault()
            setDragOver(true)
          }}
          onDragLeave={(e) => {
            // Only when leaving the panel itself, not a child.
            if (e.currentTarget === e.target) setDragOver(false)
          }}
          onDrop={(e) => {
            if (!canWrite) return
            e.preventDefault()
            setDragOver(false)
            if (e.dataTransfer.files.length) void upload(e.dataTransfer.files)
          }}
        >
          <PanelToolbar className="justify-between gap-3">
            <div className="flex min-w-0 flex-1 items-center gap-2">
              <PathBar
                path={path ?? "/"}
                home={places.data?.home}
                onNavigate={navigate}
                className="flex-1"
              />
            </div>
            <div className="flex shrink-0 items-center gap-3">
              {listing.data && (
                <span className="numeric text-hint text-muted-foreground">
                  {selected.length > 0
                    ? `${selected.length} selected`
                    : `${entries.length} item${entries.length === 1 ? "" : "s"}`}
                </span>
              )}
              <ArrangeMenu
                sort={sort}
                setSort={setSort}
                showHidden={showHidden}
                setShowHidden={setShowHidden}
                tile={tile}
                setTile={setTile}
                grid={view === "grid"}
              />
            </div>
          </PanelToolbar>

          {clip && (
            <div className="flex items-center gap-2 border-b border-hairline bg-wash-primary px-3 py-1.5 text-xs">
              {clip.mode === "cut" ? (
                <ArrowMove className="size-3.5 text-primary" />
              ) : (
                <Clipboard className="size-3.5 text-primary" />
              )}
              <span className="text-muted-foreground">
                {clip.paths.length} item{clip.paths.length === 1 ? "" : "s"} ready to{" "}
                {clip.mode === "cut" ? "move" : "copy"}
              </span>
              <span className="flex-1" />
              {canWrite && (
                <Button size="xs" onClick={paste}>
                  <Clipboard className="size-3.5" />
                  Paste here
                </Button>
              )}
              <Button size="xs" variant="ghost" onClick={() => setClip(null)}>
                <Cross className="size-3.5" />
              </Button>
            </div>
          )}

          {/* The body does not scroll; whatever is inside it does. That is
              what keeps the table's header stuck to the top of the list: a
              sticky header sticks to its nearest scrolling ancestor, and with
              the body scrolling instead the header rode away with the rows. */}
          <PanelBody flush className="relative min-h-0 flex-1 overflow-hidden">
            {dragOver && (
              <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center bg-wash-primary">
                <span className="rounded-lg border border-dashed border-primary bg-card px-4 py-2 text-body font-medium text-primary">
                  Drop to upload to {truncateMiddle(path ?? "/", 40)}
                </span>
              </div>
            )}
            {(listing.loading || path === null) && <LoadingRows className="p-4" />}
            {listing.error && <ErrorState error={listing.error} className="m-4" />}

            {view === "tree" && path !== null && treeRoot && (
              <div className="h-full overflow-auto">
                <FileTree
                  root={treeRoot}
                  statusMap={{}}
                  canWrite={canWrite}
                  canDelete={canDestruct}
                  activeDir={path}
                  onNavigate={navigate}
                  onOpenFile={(p) => setEditing(p)}
                  onConfirm={(req) =>
                    confirm({
                      title: req.title,
                      description: req.body,
                      confirmLabel: req.confirmLabel,
                      action: async () => {
                        await req.run()
                      },
                    })
                  }
                  onChanged={refresh}
                />
              </div>
            )}

            {view === "grid" && listing.data && (
              <div className="h-full overflow-auto">
                {entries.length === 0 ? (
                  <EmptyState
                    className="m-4"
                    icon={FolderOpen}
                    title="This directory is empty"
                    description={canWrite ? "Drop files here to upload, or use New." : undefined}
                  />
                ) : (
                  <GridView
                    entries={entries}
                    selected={selected}
                    activePath={activeEntry?.path ?? null}
                    caps={caps}
                    size={tile}
                    onToggle={(entry, checked) =>
                      setSelection((prev) => {
                        const paths = new Set(prev.dir === path ? prev.paths : [])
                        if (checked) paths.add(entry.path)
                        else paths.delete(entry.path)
                        return { dir: path ?? "/", paths }
                      })
                    }
                    onSelect={(entry) => setActive({ dir: path ?? "/", entry })}
                    onOpen={openEntry}
                    actions={actionsFor}
                  />
                )}
              </div>
            )}

            {view === "list" && listing.data && (
              <Table containerClassName="h-full">
                <TableHeader className={stickyTableHeader}>
                  <TableRow>
                    <TableHead className="w-8">
                      <Checkbox
                        aria-label="Select all"
                        checked={
                          entries.length > 0 && selected.length === entries.length
                            ? true
                            : selected.length > 0
                              ? "indeterminate"
                              : false
                        }
                        onCheckedChange={(v) =>
                          setSelection({
                            dir: path ?? "/",
                            paths: v === true ? new Set(entries.map((e) => e.path)) : new Set(),
                          })
                        }
                      />
                    </TableHead>
                    <SortHead
                      label="Name"
                      k="name"
                      sort={sort}
                      setSort={setSort}
                      className="w-full"
                    />
                    <SortHead
                      label="Size"
                      k="size"
                      sort={sort}
                      setSort={setSort}
                      className="text-right"
                    />
                    <SortHead label="Modified" k="modified" sort={sort} setSort={setSort} />
                    <SortHead label="Owner" k="owner" sort={sort} setSort={setSort} />
                    <SortHead
                      label="Mode"
                      k="mode"
                      sort={sort}
                      setSort={setSort}
                      className="w-20"
                    />
                    <TableHead className="w-px" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {path !== "/" && (
                    <TableRow
                      className="select-none"
                      onActivate={() => navigate(listing.data!.parent)}
                    >
                      <TableCell />
                      <TableCell colSpan={6}>
                        <span className="flex items-center gap-2 text-body text-muted-foreground">
                          <ArrowUp className="size-3.5" />
                          Parent directory
                        </span>
                      </TableCell>
                    </TableRow>
                  )}
                  {entries.map((entry) => (
                    <FileRow
                      key={entry.path}
                      entry={entry}
                      selected={selected.includes(entry.path)}
                      active={activeEntry?.path === entry.path}
                      dimmed={clip?.mode === "cut" && clipPaths.has(entry.path)}
                      caps={caps}
                      onToggle={(checked) =>
                        setSelection((prev) => {
                          const paths = new Set(prev.dir === path ? prev.paths : [])
                          if (checked) paths.add(entry.path)
                          else paths.delete(entry.path)
                          return { dir: path ?? "/", paths }
                        })
                      }
                      onSelect={() => setActive({ dir: path ?? "/", entry })}
                      onOpen={() => openEntry(entry)}
                      actions={actionsFor(entry)}
                    />
                  ))}
                  {entries.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={7} className="p-0">
                        <EmptyState
                          icon={FolderOpen}
                          title="This directory is empty"
                          description={
                            canWrite ? "Drop files here to upload, or use New." : undefined
                          }
                        />
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            )}
          </PanelBody>
        </Panel>

        {showPreview && (
          <Panel className="hidden w-80 shrink-0 xl:flex">
            <PreviewPanel
              entry={activeEntry}
              canWrite={canWrite}
              onOpen={(p) => setEditing(p)}
              onEditImage={(p) => setEditingImage(p)}
              onNavigate={navigate}
              onClose={() => setShowPreview(false)}
              className="flex-1"
            />
          </Panel>
        )}
      </div>

      <QuickOpen
        open={quickOpen}
        onOpenChange={setQuickOpen}
        root={path ?? "/"}
        home={places.data?.home}
        onOpenPath={(p, isDir) => (isDir ? navigate(p) : setEditing(p))}
      />
      <FileEditorSheet
        path={editing}
        onOpenChange={(open) => !open && setEditing(null)}
        onSaved={refresh}
      />
      <ImageEditorSheet
        path={editingImage}
        modified={activeEntry?.path === editingImage ? activeEntry?.modified : undefined}
        onOpenChange={(open) => !open && setEditingImage(null)}
        onSaved={() => {
          setActive(null)
          refresh()
        }}
      />
      <PermissionsDialog
        entry={permsEntry}
        onOpenChange={(open) => !open && setPermsEntry(null)}
        onDone={refresh}
      />
      <SymlinkDialog
        open={symlinkOpen}
        dir={path ?? "/"}
        onOpenChange={setSymlinkOpen}
        onDone={refresh}
      />
      {dialog}
      {promptDialog}
    </Page>
  )
}

/** List, grid or tree — one control, because they are one decision. */
function ViewSwitcher({ view, onChange }: { view: ViewMode; onChange: (v: ViewMode) => void }) {
  const options: {
    id: ViewMode
    label: string
    icon: React.ComponentType<{ className?: string }>
  }[] = [
    { id: "list", label: "Details", icon: ListUnordered },
    { id: "grid", label: "Tiles", icon: GridSquare },
    { id: "tree", label: "Tree", icon: Monorepo },
  ]
  return (
    <div className="flex items-center gap-0.5 rounded-md border bg-control p-0.5">
      {options.map((option) => (
        <Tooltip key={option.id}>
          <TooltipTrigger asChild>
            <button
              type="button"
              aria-label={option.label}
              aria-pressed={view === option.id}
              onClick={() => onChange(option.id)}
              className={cn(
                "flex size-7 items-center justify-center rounded-sm transition-colors",
                view === option.id
                  ? "bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
              )}
            >
              <option.icon className="size-3.5" />
            </button>
          </TooltipTrigger>
          <TooltipContent>{option.label}</TooltipContent>
        </Tooltip>
      ))}
    </div>
  )
}

/**
 * Sorting, hidden files and tile size in one menu.
 *
 * The table can sort by clicking a column header; the grid has no headers to
 * click, and a view that silently loses the ability to sort is a view people
 * conclude is broken.
 */
function ArrangeMenu({
  sort,
  setSort,
  showHidden,
  setShowHidden,
  tile,
  setTile,
  grid,
}: {
  sort: { key: SortKey; dir: "asc" | "desc" }
  setSort: (s: { key: SortKey; dir: "asc" | "desc" }) => void
  showHidden: boolean
  setShowHidden: (v: boolean) => void
  tile: "sm" | "md" | "lg"
  setTile: (v: "sm" | "md" | "lg") => void
  grid: boolean
}) {
  const keys: { id: SortKey; label: string }[] = [
    { id: "name", label: "Name" },
    { id: "size", label: "Size" },
    { id: "modified", label: "Modified" },
    { id: "owner", label: "Owner" },
    { id: "mode", label: "Mode" },
  ]
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="xs" className="text-muted-foreground">
          <SettingsSliders className="size-3.5" />
          Arrange
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-48">
        <DropdownMenuLabel>Sort by</DropdownMenuLabel>
        {keys.map((key) => (
          <DropdownMenuCheckboxItem
            key={key.id}
            checked={sort.key === key.id}
            onSelect={(e) => {
              e.preventDefault()
              setSort({
                key: key.id,
                dir: sort.key === key.id && sort.dir === "asc" ? "desc" : "asc",
              })
            }}
          >
            {key.label}
            {sort.key === key.id && (sort.dir === "asc" ? " ↑" : " ↓")}
          </DropdownMenuCheckboxItem>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuCheckboxItem
          checked={showHidden}
          onSelect={(e) => {
            e.preventDefault()
            setShowHidden(!showHidden)
          }}
        >
          Show hidden files
        </DropdownMenuCheckboxItem>
        {grid && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuLabel>Tile size</DropdownMenuLabel>
            {(["sm", "md", "lg"] as const).map((size) => (
              <DropdownMenuCheckboxItem
                key={size}
                checked={tile === size}
                onSelect={(e) => {
                  e.preventDefault()
                  setTile(size)
                }}
              >
                {size === "sm" ? "Small" : size === "lg" ? "Large" : "Medium"}
              </DropdownMenuCheckboxItem>
            ))}
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function SortHead({
  label,
  k,
  sort,
  setSort,
  className,
}: {
  label: string
  k: SortKey
  sort: { key: SortKey; dir: "asc" | "desc" }
  setSort: (s: { key: SortKey; dir: "asc" | "desc" }) => void
  className?: string
}) {
  const active = sort.key === k
  return (
    <TableHead className={className}>
      <button
        className={cn(
          "inline-flex items-center gap-1 hover:text-foreground",
          active && "text-foreground",
          className?.includes("text-right") && "flex-row-reverse",
        )}
        onClick={() => setSort({ key: k, dir: active && sort.dir === "asc" ? "desc" : "asc" })}
      >
        {label}
        {active &&
          (sort.dir === "asc" ? (
            <ChevronUp className="size-3" />
          ) : (
            <ChevronDown className="size-3" />
          ))}
      </button>
    </TableHead>
  )
}

function SelectionActions({
  count,
  canWrite,
  canDestruct,
  archiveHref,
  onCopy,
  onCut,
  onDelete,
  onClear,
}: {
  count: number
  canWrite: boolean
  canDestruct: boolean
  archiveHref: string
  onCopy: () => void
  onCut: () => void
  onDelete: () => void
  onClear: () => void
}) {
  return (
    <>
      <span className="numeric text-body text-muted-foreground">{count} selected</span>
      {canWrite && (
        <>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="outline" size="sm" onClick={onCopy}>
                <Clipboard className="size-4" />
                Copy
              </Button>
            </TooltipTrigger>
            <TooltipContent>Hold these to paste into another folder</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="outline" size="sm" onClick={onCut}>
                <ArrowMove className="size-4" />
                Cut
              </Button>
            </TooltipTrigger>
            <TooltipContent>Hold these to move into another folder</TooltipContent>
          </Tooltip>
        </>
      )}
      <Tooltip>
        <TooltipTrigger asChild>
          <Button variant="outline" size="sm" asChild>
            <a href={archiveHref} download>
              <FileZip className="size-4" />
              Archive
            </a>
          </Button>
        </TooltipTrigger>
        <TooltipContent>Download the selection as one .tar.gz</TooltipContent>
      </Tooltip>
      {canDestruct && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="outline" size="sm" className="text-destructive" onClick={onDelete}>
              <Trash className="size-4" />
              Delete
            </Button>
          </TooltipTrigger>
          <TooltipContent>Delete the selection permanently</TooltipContent>
        </Tooltip>
      )}
      <Tooltip>
        <TooltipTrigger asChild>
          <Button variant="ghost" size="sm" aria-label="Clear selection" onClick={onClear}>
            <Cross className="size-4" />
          </Button>
        </TooltipTrigger>
        <TooltipContent>Clear the selection</TooltipContent>
      </Tooltip>
    </>
  )
}

function NewMenu({
  onFolder,
  onFile,
  onSymlink,
}: {
  onFolder: () => void
  onFile: () => void
  onSymlink: () => void
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm">
          <Plus className="size-4" />
          New
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-44">
        <DropdownMenuItem onSelect={onFolder}>
          <FolderPlus className="size-3.5" />
          Folder
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={onFile}>
          <PlusSquareSmall className="size-3.5" />
          File
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={onSymlink}>
          <Linked className="size-3.5" />
          Symlink
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function SymlinkDialog({
  open,
  dir,
  onOpenChange,
  onDone,
}: {
  open: boolean
  dir: string
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  // The body is only mounted while open (Radix unmounts closed content), so its
  // fields start empty every time without a reset effect.
  return open ? (
    <SymlinkBody
      open={open}
      onOpenChange={onOpenChange}
      dir={dir}
      onClose={() => onOpenChange(false)}
      onDone={onDone}
    />
  ) : null
}

function SymlinkBody({
  open,
  onOpenChange,
  dir,
  onClose,
  onDone,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  dir: string
  onClose: () => void
  onDone: () => void
}) {
  const [target, setTarget] = useState("")
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)

  const create = async () => {
    if (!target.trim() || !name.trim()) return
    setBusy(true)
    try {
      await post("/files/symlink", { target: target.trim(), link: join(dir, name.trim()) })
      notify.success(`Created link ${name.trim()}`)
      onDone()
      onClose()
    } catch (err) {
      notify.error("Could not create symlink", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="sm"
      icon={Linked}
      title="New symlink"
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={create} disabled={busy || !target.trim() || !name.trim()}>
            Create link
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <div className="space-y-1.5">
          <label className="text-xs text-muted-foreground">Points at</label>
          <Input
            autoFocus
            value={target}
            onChange={(e) => setTarget(e.target.value)}
            placeholder="/path/it/points/to"
            className="font-mono text-xs"
          />
        </div>
        <div className="space-y-1.5">
          <label className="text-xs text-muted-foreground">Link name</label>
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && create()}
            placeholder="link-name"
            className="font-mono text-xs"
          />
          <p className="font-mono text-hint break-all text-muted-foreground">
            {join(dir, name || "…")} → {target || "…"}
          </p>
        </div>
      </div>
    </Modal>
  )
}

/**
 * The other search: a literal substring or a regular expression, optionally
 * inside file contents.
 *
 * It stays next to the fuzzy finder rather than being replaced by it because
 * the two answer different questions — this one is "which files mention this
 * connection string", and no amount of name matching answers that.
 */
function SearchDialog({
  path,
  onOpen,
}: {
  path: string
  onOpen: (path: string, isDir: boolean) => void
}) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState("")
  const [content, setContent] = useState(true)
  const [regex, setRegex] = useState(false)
  const [hits, setHits] = useState<
    { path: string; name: string; isDir: boolean; line?: number; snippet?: string }[]
  >([])
  const [busy, setBusy] = useState(false)

  const run = async () => {
    if (!query) return
    setBusy(true)
    try {
      setHits(await get("/files/search", { path, q: query, content, regex, limit: 200 }))
    } catch (err) {
      notify.error("Search failed", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            variant="outline"
            size="icon-sm"
            aria-label="Search inside files"
            onClick={() => setOpen(true)}
          >
            <PreviewDocument className="size-4" />
          </Button>
        </TooltipTrigger>
        <TooltipContent>Search inside file contents</TooltipContent>
      </Tooltip>
      <Modal
        open={open}
        onOpenChange={setOpen}
        size="lg"
        title={<>Search under {truncateMiddle(path, 40)}</>}
      >
        <div className="space-y-3">
          <div className="flex gap-2">
            <Input
              autoFocus
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && run()}
              placeholder={content ? "Text to find inside files" : "File or directory name"}
            />
            <Button onClick={run} disabled={busy || !query}>
              Search
            </Button>
          </div>
          <div className="flex flex-wrap gap-4">
            <label className="flex items-center gap-2 text-body">
              <Checkbox checked={content} onCheckedChange={(v) => setContent(v === true)} />
              Search inside file contents
            </label>
            <label className="flex items-center gap-2 text-body">
              <Checkbox checked={regex} onCheckedChange={(v) => setRegex(v === true)} />
              Regular expression
            </label>
          </div>
          <div className="max-h-80 space-y-0.5 overflow-auto">
            {hits.map((hit) => (
              <button
                key={`${hit.path}:${hit.line ?? 0}`}
                className="block w-full rounded-md px-2 py-1.5 text-left hover:bg-accent"
                onClick={() => {
                  onOpen(hit.path, hit.isDir)
                  setOpen(false)
                }}
              >
                <span className="block truncate font-mono text-xs">{hit.path}</span>
                {hit.snippet && (
                  <span className="block truncate text-hint text-muted-foreground">
                    line {hit.line}: {hit.snippet}
                  </span>
                )}
              </button>
            ))}
            {!busy && hits.length === 0 && query && <EmptyNote>No matches.</EmptyNote>}
          </div>
        </div>
      </Modal>
    </>
  )
}
