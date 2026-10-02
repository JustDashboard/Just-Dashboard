"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useSearchParams } from "next/navigation"
import { AnimatePresence, motion, useReducedMotion } from "motion/react"
import { useMarquee } from "@/components/files/use-marquee"
import { rangePaths } from "@/components/files/selection"
import {
  ArrowMove,
  ArrowUp,
  Check,
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
  Location,
  MagnifyingGlass,
  Plus,
  PlusSquareSmall,
  PreviewDocument,
  RefreshClockwise,
  SettingsSliders,
  SidebarLeftClose,
  SidebarLeftOpen,
  SidebarRightClose,
  SidebarRightOpen,
  Star,
  StarFill,
  Trash,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { ApiError, del, get, post, put } from "@/lib/api"
import { bytes, plural, truncateMiddle } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileBookmark, FileEntry, FileListing, FilePlaces } from "@/lib/types"
import { useSessionState, useViewState } from "@/lib/view-state"
import { usePanelSize } from "@/lib/panel-size"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useMetrics } from "@/hooks/use-metrics"
import { useConfirm } from "@/components/confirm-dialog"
import { Page } from "@/components/page"
import { PaneFooter, PaneHeader } from "@/components/panel"
import { GitHubAccountControl } from "@/components/git/github-account"
import { FileEditorSheet } from "@/components/files/file-editor"
import {
  defaultFolderColour,
  folderColourOf,
  FolderColourProvider,
  FolderIcon,
  FolderSwatch,
  validFolderColour,
  type FolderColour,
} from "@/components/files/file-icon"
import { FolderColourMenu } from "@/components/files/folder-colour"
import { COLUMN, FileRow } from "@/components/files/file-row"
import { FilesSidebar } from "@/components/files/files-sidebar"
import { GridView, type TileSize } from "@/components/files/grid-view"
import { ImageEditorSheet } from "@/components/files/image-editor"
import { MediaViewer } from "@/components/files/media-viewer"
import { PathBar } from "@/components/files/path-bar"
import { PlacesMenu } from "@/components/files/places-menu"
import { Modal } from "@/components/modal"
import { PermissionsDialog } from "@/components/files/permissions-dialog"
import { PreviewPanel } from "@/components/files/preview-panel"
import { QuickOpen, type FileSearchMode } from "@/components/files/quick-open"
import {
  archiveHref,
  colourVerb,
  ListingContextMenu,
  type FileActions,
  type Verb,
} from "@/components/files/file-actions"
import { usePrompt } from "@/components/files/prompt-dialog"
import { useConflicts, type ConflictPolicy } from "@/components/files/conflict-dialog"
import {
  collectInput,
  collectTransfer,
  UploadStrip,
  useUploads,
  type Incoming,
  type UploadJob,
} from "@/components/files/uploads"
import {
  baseOf,
  cleanPath,
  isWithin,
  joinPath,
  mediaKind,
  numberedName,
  parentOf,
  uniqueName,
} from "@/components/files/media"
import { startPathDrag, useDropTarget, type DropMode } from "@/components/files/dnd"
import { ResizeHandle } from "@/components/resize-handle"
import { Meter, utilisationTone } from "@/components/meter"
import { IconAction } from "@/components/icon-action"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Input } from "@/components/ui/input"
import { Checkbox } from "@/components/ui/checkbox"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
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
type Sort = { key: SortKey; dir: "asc" | "desc" }
type ViewMode = "list" | "grid"
type Clip = { mode: "cut" | "copy"; paths: string[] }

const RAIL = { base: 264, min: 200, max: 440 }
const INSPECTOR = { base: 320, min: 260, max: 560 }
const EMPTY = new Set<string>()
const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v))

// Reading register: the workbench owns its scroll boundaries; directory and
// selection counts stay in the listing footer, where they describe the work.
export default function FilesPage() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const { prompt, dialog: promptDialog } = usePrompt()
  const conflicts = useConflicts()
  const canWrite = can("file.write")
  const canDestruct = can("destructive")
  const canAdmin = can("system.admin")
  const reducedMotion = useReducedMotion()
  const [dragging, setDragging] = useState<Set<string>>(EMPTY)
  const caps = useMemo(
    () => ({ write: canWrite, destruct: canDestruct, admin: canAdmin }),
    [canWrite, canDestruct, canAdmin],
  )

  // Where the machine says to start. The page opens at home rather than at
  // "/", which is the one directory on a Linux server where nothing an
  // operator owns lives. A ?path= from a deep link still wins.
  const initialPath = useSearchParams().get("path")
  const initialEntry = useSearchParams().get("entry")
  const places = usePoll<FilePlaces>((signal) => get("/files/places", undefined, signal), 0, [])
  // Derived rather than copied into state by an effect: until either the URL
  // or a navigation has said otherwise, the answer *is* whatever the server
  // reports as home. The chosen directory is kept for the tab, so the rail's
  // bare link comes back to the folder being worked in rather than to home.
  const [chosenPath, setChosenPath] = useSessionState<string | null>(
    "files.path",
    null,
    initialPath ? cleanPath(initialPath) : undefined,
  )
  const path = chosenPath ?? places.data?.home ?? null
  const pathRef = useRef(path)
  useEffect(() => {
    pathRef.current = path
  }, [path])

  // The furniture, remembered. A stored "tree" from the version that had a
  // third view falls back to the list.
  const [storedView, setView] = useViewState<string>("files.view", "list")
  const view: ViewMode = storedView === "grid" ? "grid" : "list"
  const [tile, setTile] = useViewState<TileSize>("files.tile", "md")
  const [showHidden, setShowHidden] = useViewState("files.hidden", false)
  const [showSidebar, setShowSidebar] = useViewState("files.sidebar", true)
  const [showInspector, setShowInspector] = useViewState("files.inspector", true)
  const [sort, setSort] = useViewState<Sort>("files.sort", { key: "name", dir: "asc" })
  // Where you have just been. Kept in the browser rather than on the server:
  // unlike a starred folder, which is a fact about the machine, this is a
  // property of the last ten minutes at this screen.
  const [recent, setRecent] = useViewState<string[]>("files.recent", [])
  const [railWidth, setRailWidth, resetRailWidth] = usePanelSize("files.rail", RAIL.base)
  const [inspectorWidth, setInspectorWidth, resetInspectorWidth] = usePanelSize(
    "files.inspector",
    INSPECTOR.base,
  )
  const railPx = clamp(railWidth, RAIL.min, RAIL.max)
  const inspectorPx = clamp(inspectorWidth, INSPECTOR.min, INSPECTOR.max)

  const [editing, setEditing] = useState<string | null>(null)
  const [editingImage, setEditingImage] = useState<string | null>(null)
  const [viewing, setViewing] = useState<number | null>(null)
  const [permsEntry, setPermsEntry] = useState<FileEntry | null>(null)
  const [symlinkOpen, setSymlinkOpen] = useState(false)
  const [quickOpen, setQuickOpen] = useState(false)
  const [quickMode, setQuickMode] = useState<FileSearchMode>("names")
  const [searchLocation, setSearchLocation] = useState<{ path: string; line?: number }>()
  const [clip, setClip] = useState<Clip | null>(null)
  // The selection and the active row are scoped to the directory they were
  // made in, so navigating away discards both without a reset effect.
  const [selection, setSelection] = useState<{ dir: string; paths: Set<string> }>({
    dir: path ?? "/",
    paths: new Set(),
  })
  const [active, setActive] = useState<{ dir: string; entry: FileEntry } | null | undefined>()
  // Where a Shift-click range starts: the last row clicked or arrowed to.
  const anchor = useRef<string | null>(null)
  const fileInput = useRef<HTMLInputElement>(null)
  const folderInput = useRef<HTMLInputElement>(null)
  const selected = selection.dir === path ? selection.paths : EMPTY

  // Polled gently: a file that arrived by scp, a deploy that wrote a release,
  // a log that rotated — the listing used to show none of it until the
  // operator navigated away and back. The hidden switch is a parameter of the
  // same resource, refetched in place, rather than a new resource that would
  // blank the rows for a skeleton on every flip.
  const listing = usePoll(
    (signal) =>
      path === null
        ? Promise.resolve(null as unknown as FileListing)
        : get<FileListing>("/files/list", { path, hidden: showHidden }, signal),
    20_000,
    [path],
  )
  const refreshListing = listing.refresh
  // A storage investigation names an existing entry in a validated listing.
  // An explicit click or deselection wins over the initial URL selection.
  const activeEntry =
    active === undefined && initialEntry && path === initialPath
      ? (listing.data?.entries.find((entry) => entry.path === initialEntry) ?? null)
      : active && active.dir === path
        ? active.entry
        : null
  const firstHidden = useRef(true)
  useEffect(() => {
    if (firstHidden.current) {
      firstHidden.current = false
      return
    }
    refreshListing()
  }, [showHidden, refreshListing])
  const reload = useCallback(() => {
    refreshListing()
  }, [refreshListing])

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
  const byPath = useMemo(() => new Map(entries.map((e) => [e.path, e])), [entries])
  const names = useMemo(() => new Set(entries.map((e) => e.name)), [entries])
  // The viewer walks the folder's files in the order the listing shows them.
  const viewable = useMemo(() => entries.filter((e) => !e.isDir), [entries])
  const dimmed = useMemo(() => new Set(clip?.mode === "cut" ? clip.paths : []), [clip])
  const bookmarks = useMemo(() => places.data?.bookmarks ?? [], [places.data])
  const starred = path !== null && bookmarks.some((b) => b.path === path)
  // A colour picked here is drawn at once rather than after the round trip;
  // the edits are kept against the places they were made over, so the
  // server's answer replaces them the moment it is read back.
  const [colourEdits, setColourEdits] = useState<{
    over?: FilePlaces
    edits: Record<string, string>
    defaultColour?: FolderColour
    reset?: boolean
  }>({ edits: {} })
  const defaultColour =
    (colourEdits.over === places.data && colourEdits.defaultColour) ||
    validFolderColour(places.data?.defaultColour)
  const colours = useMemo(() => {
    const merged = {
      ...(colourEdits.over === places.data && colourEdits.reset
        ? {}
        : (places.data?.colours ?? {})),
    }
    if (colourEdits.over === places.data) {
      for (const [p, colour] of Object.entries(colourEdits.edits)) {
        if (colour) merged[p] = colour
        else delete merged[p]
      }
    }
    return merged
  }, [places.data, colourEdits])
  // What is being browsed, as an entry: the inspector describes it while
  // nothing in it is chosen, and the strip draws it in its colour.
  const here = useMemo<FileEntry | null>(
    () =>
      path === null
        ? null
        : {
            name: baseOf(path),
            path,
            isDir: true,
            isSymlink: false,
            size: 0,
            mode: "",
            modeOctal: "",
            modified: "",
            owner: "",
            group: "",
            uid: 0,
            gid: 0,
          },
    [path],
  )
  // The row above the listing that goes up a level — only where up is
  // somewhere the server will list. An install that narrowed JD_FILE_ROOTS
  // used to offer a parent that answered 403.
  const parent =
    listing.data && listing.data.parent !== listing.data.path ? listing.data.parent : null
  const parentReachable =
    parent !== null && (listing.data?.roots ?? []).some((root) => isWithin(parent, root))

  // The disk this folder is on, from the metrics stream the shell already
  // keeps open: a cloud says how much room is left, and so does this.
  const { snapshot } = useMetrics()
  const mount = useMemo(() => {
    // A frame that has not carried mounts yet — the first one after the
    // socket opens, or a seeded server snapshot — is simply no reading.
    if (!path || !Array.isArray(snapshot?.mounts)) return undefined
    let best: (typeof snapshot.mounts)[number] | undefined
    for (const m of snapshot.mounts) {
      if (isWithin(path, m.mountpoint) && (!best || m.mountpoint.length > best.mountpoint.length)) {
        best = m
      }
    }
    return best
  }, [path, snapshot])

  // An upload into the browsed folder is a change to the listing; one into
  // another folder shows up when that folder is opened.
  const uploads = useUploads(
    useCallback(
      (dir: string) => {
        if (dir === pathRef.current) reload()
      },
      [reload],
    ),
  )

  // --- selection ---

  const clearSelection = useCallback(() => {
    anchor.current = null
    setSelection({ dir: path ?? "/", paths: new Set() })
  }, [path])
  const setSelected = useCallback(
    (paths: Set<string>) =>
      setSelection((previous) => {
        if (
          previous.dir === path &&
          previous.paths.size === paths.size &&
          [...paths].every((p) => previous.paths.has(p))
        )
          return previous
        return { dir: path ?? "/", paths }
      }),
    [path],
  )
  const toggleSelected = (entry: FileEntry, checked: boolean) => {
    anchor.current = entry.path
    setActive({ dir: path ?? "/", entry })
    setSelection((prev) => {
      const paths = new Set(prev.dir === path ? prev.paths : [])
      if (checked) paths.add(entry.path)
      else paths.delete(entry.path)
      return { dir: path ?? "/", paths }
    })
  }

  /** Once selection begins, the whole entry toggles; double-click and Enter still open. */
  const selectRow = (
    entry: FileEntry,
    event: { shiftKey: boolean; ctrlKey: boolean; metaKey: boolean; detail?: number },
  ) => {
    if ((event.detail ?? 1) > 1) return
    const mod = event.ctrlKey || event.metaKey
    if (event.shiftKey && anchor.current) {
      setSelected(
        new Set([
          ...(mod ? selected : []),
          ...rangePaths(
            entries.map((e) => e.path),
            anchor.current,
            entry.path,
          ),
        ]),
      )
    } else if (mod || selected.size > 0) {
      toggleSelected(entry, !selected.has(entry.path))
    } else {
      anchor.current = entry.path
    }
    setActive({ dir: path ?? "/", entry })
  }

  const marquee = useMarquee({
    scope: `${path}:${view}:${tile}`,
    selected,
    onSelect: setSelected,
    onClear: () => {
      clearSelection()
      setActive(null)
      anchor.current = null
    },
  })

  const navigate = useCallback(
    (next: string) => {
      setChosenPath(cleanPath(next))
      setActive(null)
      setViewing(null)
    },
    [setChosenPath],
  )

  const viewEntry = useCallback(
    (entry: FileEntry) => {
      const index = viewable.findIndex((e) => e.path === entry.path)
      if (index >= 0) setViewing(index)
    },
    [viewable],
  )

  // What a double-click means: a folder is entered, media is looked at, and
  // everything else goes to the editor — which says so if the bytes turn out
  // not to be text.
  const openEntry = useCallback(
    (entry: FileEntry) => {
      if (entry.isDir) navigate(entry.path)
      else if (mediaKind(entry.name)) viewEntry(entry)
      else setEditing(entry.path)
    },
    [navigate, viewEntry],
  )

  // --- operations ---

  /** Every name in a folder, dotfiles included: what a new name must not be. */
  const namesIn = async (dir: string): Promise<Set<string>> => {
    const full = await get<FileListing>("/files/list", { path: dir, hidden: true })
    return new Set(full.entries.map((e) => e.name))
  }

  /**
   * Files from the desktop, into a folder. Folders inside the drop are made
   * first; names that are already taken are asked about once, for the lot.
   */
  const handleIncoming = async (incoming: Incoming[], dir: string) => {
    if (incoming.length === 0) return
    try {
      const taken = await namesIn(dir)
      const top = incoming.filter((i) => !i.rel.includes("/")).map((i) => i.rel)
      const clashes = top.filter((name) => taken.has(name))
      let policy: ConflictPolicy = "keep"
      if (clashes.length > 0) {
        const answer = await conflicts.ask(clashes, dir, "uploaded")
        if (!answer) return
        policy = answer
      }
      const dirs = new Set<string>()
      for (const item of incoming) {
        const parts = item.rel.split("/")
        for (let k = 1; k < parts.length; k++) dirs.add(parts.slice(0, k).join("/"))
      }
      for (const d of [...dirs].sort((a, b) => a.split("/").length - b.split("/").length)) {
        try {
          await post("/files/mkdir", { path: joinPath(dir, d) })
        } catch (err) {
          if (!(err instanceof ApiError && err.status === 409)) throw err
        }
      }
      const jobs: UploadJob[] = []
      for (const item of incoming) {
        const parts = item.rel.split("/")
        const name = parts.pop() ?? item.file.name
        const sub = parts.join("/")
        let finalName = name
        let overwrite = false
        if (!sub && taken.has(name)) {
          if (policy === "skip") continue
          if (policy === "replace") overwrite = true
          else finalName = numberedName(name, taken)
        } else if (sub) {
          overwrite = policy === "replace"
        }
        taken.add(finalName)
        jobs.push({
          file: item.file,
          dir: sub ? joinPath(dir, sub) : dir,
          name: finalName,
          overwrite,
        })
      }
      uploads.enqueue(jobs)
      if (dirs.size > 0) reload()
    } catch (err) {
      notify.error("Upload failed", err)
    }
  }

  const dropFiles = async (transfer: DataTransfer, dir: string) => {
    await handleIncoming(await collectTransfer(transfer), dir)
  }

  /**
   * Paths from the listing into a folder — the drop, the paste, the drag
   * onto a crumb. The server refuses to clobber, so a taken name is asked
   * about before anything moves rather than found out about per file.
   */
  const movePaths = async (paths: string[], dir: string, mode: DropMode) => {
    const dest = cleanPath(dir)
    const targets = paths.filter((p) => !(mode === "move" && parentOf(p) === dest))
    if (targets.some((p) => isWithin(dest, p))) {
      notify.error("A folder cannot be moved into itself")
      return
    }
    if (targets.length === 0) return
    try {
      const taken = await namesIn(dest)
      const clashes = targets.map(baseOf).filter((name) => taken.has(name))
      let policy: ConflictPolicy = "keep"
      if (clashes.length > 0) {
        const answer = await conflicts.ask(clashes, dest, mode === "move" ? "moved" : "copied")
        if (!answer) return
        policy = answer
      }
      let ok = 0
      const moved = new Set<string>()
      for (const src of targets) {
        const base = baseOf(src)
        let name = base
        let overwrite = false
        if (taken.has(base)) {
          if (policy === "skip") continue
          if (policy === "replace") overwrite = true
          else if (mode === "copy" && parentOf(src) === dest) name = uniqueName(base, taken)
          else name = numberedName(base, taken)
        }
        try {
          await post(`/files/${mode}`, { from: src, to: joinPath(dest, name), overwrite })
          taken.add(name)
          if (mode === "move") moved.add(src)
          ok++
        } catch (err) {
          notify.error(`Could not ${mode} ${base}`, err)
        }
      }
      if (ok > 0) {
        const where = dest === path ? "" : ` to ${truncateMiddle(dest, 40)}`
        notify.success(`${mode === "move" ? "Moved" : "Copied"} ${plural(ok, "item")}${where}`)
        if (mode === "move") {
          afterLabelledChange([...moved])
          setActive((previous) =>
            previous &&
            previous.dir === path &&
            [...moved].some((src) => isWithin(previous.entry.path, src))
              ? null
              : previous,
          )
        }
      }
      clearSelection()
      reload()
    } catch (err) {
      notify.error(`Could not ${mode}`, err)
    }
  }

  const paste = async () => {
    if (!clip || !path) return
    const held = clip
    await movePaths(held.paths, path, held.mode === "cut" ? "move" : "copy")
    if (held.mode === "cut") setClip(null)
  }

  const rename = (entry: FileEntry) =>
    prompt({
      title: `Rename ${entry.name}`,
      label: "New name",
      initial: entry.name,
      confirmLabel: "Rename",
      selectBasename: true,
      validate: (v) =>
        v.includes("/")
          ? "A name cannot contain a slash."
          : v !== entry.name && names.has(v)
            ? "Something here already has that name."
            : undefined,
      hint: (v) => joinPath(path ?? "/", v || "…"),
      submit: async (name) => {
        if (name === entry.name) return
        await post("/files/move", { from: entry.path, to: joinPath(path ?? "/", name) })
        notify.success(`Renamed to ${name}`)
        setActive(null)
        reload()
        afterLabelledChange([entry.path])
      },
    })

  const duplicate = async (entry: FileEntry) => {
    const name = uniqueName(entry.name, names)
    try {
      await post("/files/copy", { from: entry.path, to: joinPath(path ?? "/", name) })
      notify.success(`Duplicated as ${name}`)
      reload()
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
      notify.success(`Extracted ${plural(res.extracted, "item")}`)
      reload()
    } catch (err) {
      notify.error("Could not extract", err)
    }
  }

  const deleteEntry = (entry: FileEntry) =>
    confirm({
      title: entry.isDir ? "Delete folder" : "Delete file",
      confirmLabel: "Delete",
      description: (
        <p className="text-destructive">
          <b>{entry.path}</b> is deleted permanently
          {entry.isDir ? ", along with everything inside it." : "."}
        </p>
      ),
      action: async () => {
        await del("/files/delete", {
          query: { path: entry.path, recursive: entry.isDir },
        })
        setActive(null)
        reload()
        afterLabelledChange([entry.path])
      },
    })

  const bulkDelete = () => {
    const targets = entries.filter((e) => selected.has(e.path))
    if (targets.length === 0) return
    const dirs = targets.filter((e) => e.isDir)
    confirm({
      title: `Delete ${plural(targets.length, "item")}`,
      confirmLabel: "Delete all",
      description: (
        <p className="text-destructive">
          {plural(targets.length, "selected item")} {targets.length === 1 ? "is" : "are"} deleted
          permanently
          {dirs.length > 0 ? ", folders and everything inside them included." : "."}
        </p>
      ),
      action: async () => {
        let failed = 0
        for (const entry of targets) {
          try {
            await del("/files/delete", {
              query: { path: entry.path, recursive: entry.isDir },
            })
          } catch {
            failed++
          }
        }
        clearSelection()
        setActive(null)
        reload()
        afterLabelledChange(targets.map((e) => e.path))
        if (failed) notify.error(`${plural(failed, "item")} could not be deleted`)
      },
    })
  }

  const newFolder = () =>
    prompt({
      title: "New folder",
      placeholder: "Folder name",
      confirmLabel: "Create",
      validate: (v) =>
        v.includes("/")
          ? "A name cannot contain a slash."
          : names.has(v)
            ? "Something here already has that name."
            : undefined,
      hint: (v) => joinPath(path ?? "/", v || "…"),
      submit: async (name) => {
        await post("/files/mkdir", { path: joinPath(path ?? "/", name) })
        notify.success(`Created ${name}`)
        reload()
      },
    })

  const newFile = () =>
    prompt({
      title: "New file",
      placeholder: "File name",
      confirmLabel: "Create",
      validate: (v) =>
        v.includes("/")
          ? "A name cannot contain a slash."
          : names.has(v)
            ? "Something here already has that name."
            : undefined,
      hint: (v) => joinPath(path ?? "/", v || "…"),
      submit: async (name) => {
        await post("/files/touch", { path: joinPath(path ?? "/", name) })
        notify.success(`Created ${name}`)
        reload()
        setEditing(joinPath(path ?? "/", name))
      },
    })

  const cutCopy = (mode: "cut" | "copy", paths: string[]) => {
    if (paths.length === 0) return
    setClip({ mode, paths })
    notify.success(
      `${mode === "cut" ? "Cut" : "Copied"} ${plural(paths.length, "item")} — paste in any folder`,
    )
  }

  const setColour = async (target: Pick<FileEntry, "path" | "name">, colour: FolderColour) => {
    // A folder's default is no label at all, so choosing it clears the one it
    // had rather than storing the default against a folder that already uses it.
    const value =
      colour === defaultFolderColour(target.name, target.path, defaultColour) ? "" : colour
    setColourEdits((prev) => ({
      ...(prev.over === places.data ? prev : {}),
      over: places.data,
      edits: { ...(prev.over === places.data ? prev.edits : {}), [target.path]: value },
    }))
    try {
      await put("/files/colours", { path: target.path, colour: value })
    } catch (err) {
      notify.error("Could not colour that folder", err)
    }
    places.refresh()
  }
  const setDefaultColour = async (colour: FolderColour) => {
    setColourEdits({ over: places.data, edits: {}, defaultColour: colour, reset: true })
    try {
      await put("/files/colours/default", { colour })
    } catch (err) {
      notify.error("Could not colour all folders", err)
    }
    places.refresh()
  }
  /** The server carries a label along with a move and drops it with a delete; this reads that back. */
  const afterLabelledChange = (paths: string[]) => {
    const labelled = Object.keys(colours)
    if (paths.some((p) => labelled.some((k) => isWithin(k, p)))) places.refresh()
  }

  const saveBookmarks = async (next: FileBookmark[]) => {
    try {
      await put("/files/bookmarks", { bookmarks: next })
      places.refresh()
    } catch (err) {
      notify.error("Could not save your places", err)
    }
  }
  const toggleStar = (dir: string, name: string) => {
    const has = bookmarks.some((b) => b.path === dir)
    void saveBookmarks(
      has ? bookmarks.filter((b) => b.path !== dir) : [...bookmarks, { path: dir, name }],
    )
  }

  const actionsFor = (entry: FileEntry): FileActions => ({
    onOpen: () => openEntry(entry),
    onView: () => viewEntry(entry),
    onEdit: () => setEditing(entry.path),
    onRename: () => rename(entry),
    onDuplicate: () => void duplicate(entry),
    onCopy: () => cutCopy("copy", selected.has(entry.path) ? [...selected] : [entry.path]),
    onCut: () => cutCopy("cut", selected.has(entry.path) ? [...selected] : [entry.path]),
    onExtract: () => void extract(entry),
    onPermissions: () => setPermsEntry(entry),
    onDelete: () =>
      selected.has(entry.path) && selected.size > 1 ? bulkDelete() : deleteEntry(entry),
    onEditImage: () => setEditingImage(entry.path),
    onToggleStar: () => toggleStar(entry.path, entry.name),
    starred: bookmarks.some((b) => b.path === entry.path),
    colour: folderColourOf(colours, entry.path, entry.name, defaultColour),
    onColour: (colour) => void setColour(entry, colour),
  })

  /** What a right-click on the space between rows offers: the folder's verbs. */
  const backgroundVerbs: Verb[][] = [
    canWrite
      ? [
          { id: "new-folder", label: "New folder", icon: FolderPlus, onSelect: newFolder },
          { id: "new-file", label: "New file", icon: PlusSquareSmall, onSelect: newFile },
          {
            id: "upload",
            label: "Upload files…",
            icon: CloudUpload,
            onSelect: () => fileInput.current?.click(),
          },
        ]
      : [],
    canWrite && clip
      ? [
          {
            id: "paste",
            label: `Paste ${plural(clip.paths.length, "item")}`,
            icon: Clipboard,
            onSelect: () => void paste(),
            shortcut: "⌃V",
          },
        ]
      : [],
    [
      {
        id: "select-all",
        label: "Select all",
        icon: Check,
        onSelect: () => setSelected(new Set(entries.map((e) => e.path))),
        shortcut: "⌃A",
      },
      { id: "refresh", label: "Refresh", icon: RefreshClockwise, onSelect: reload },
    ],
    canWrite && here
      ? [
          {
            id: "star",
            label: starred ? "Unstar this folder" : "Star this folder",
            icon: starred ? StarFill : Star,
            onSelect: () => toggleStar(here.path, here.name),
          },
          colourVerb(folderColourOf(colours, here.path, here.name, defaultColour), (colour) => {
            void setColour(here, colour)
          }),
        ]
      : [],
  ]

  const dragStart = (entry: FileEntry, event: React.DragEvent) => {
    const paths = selected.has(entry.path) ? [...selected] : [entry.path]
    setDragging(new Set(paths))
    if (!selected.has(entry.path)) setSelected(new Set(paths))
    anchor.current = entry.path
    setActive({ dir: path ?? "/", entry })
    startPathDrag(event, paths)
  }

  // Files from the desktop dropped anywhere on the listing land in the folder
  // being browsed; a folder row under the pointer takes them instead.
  const dropZone = useDropTarget({
    dir: canWrite ? path : null,
    paths: false,
    onDropFiles: (transfer, dir) => void dropFiles(transfer, dir),
  })

  /** Arrow keys walk the listing; Shift extends the selection as they go. */
  const moveActive = (key: "ArrowDown" | "ArrowUp" | "Home" | "End", extend: boolean) => {
    if (entries.length === 0) return
    const index = activeEntry ? entries.findIndex((e) => e.path === activeEntry.path) : -1
    const next =
      key === "Home"
        ? 0
        : key === "End"
          ? entries.length - 1
          : key === "ArrowDown"
            ? Math.min(entries.length - 1, index + 1)
            : Math.max(0, index - 1)
    const entry = entries[next]
    setActive({ dir: path ?? "/", entry })
    if (extend && anchor.current) {
      const a = entries.findIndex((e) => e.path === anchor.current)
      const [lo, hi] = a < next ? [a, next] : [next, a]
      const paths = new Set<string>()
      for (let i = Math.max(0, lo); i <= hi; i++) paths.add(entries[i].path)
      setSelected(paths)
    } else {
      anchor.current = entry.path
    }
    requestAnimationFrame(() => {
      const el = document.querySelector<HTMLElement>(
        `[data-entry-path="${CSS.escape(entry.path)}"]`,
      )
      el?.scrollIntoView({ block: "nearest" })
      if (el?.tagName === "TR") el.focus({ preventScroll: true })
    })
  }

  // The page's own shortcuts. They all match something people already have
  // in their hands: Ctrl+P is every editor's "go to file", F2 renames as it
  // has since Norton Commander, Space is a quick look, Backspace goes up,
  // and the clipboard chords do what the menu's Copy, Cut and Paste do.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.defaultPrevented) return
      const target = event.target as HTMLElement | null
      const typing =
        target?.tagName === "INPUT" ||
        target?.tagName === "TEXTAREA" ||
        target?.isContentEditable === true
      // A dialog or a menu open anywhere owns the keyboard; a shortcut firing
      // behind one acts on a page the operator cannot see. One that is
      // closing — still in the DOM for its exit animation — no longer does.
      if (
        document.querySelector(
          "[role='dialog']:not([data-state='closed']), [role='menu']:not([data-state='closed'])",
        )
      ) {
        return
      }
      const mod = event.metaKey || event.ctrlKey
      const key = event.key.toLowerCase()

      if (mod && event.shiftKey && key === "f") {
        event.preventDefault()
        setQuickMode("content")
        setQuickOpen(true)
        return
      }
      if (mod && key === "p") {
        event.preventDefault()
        setQuickMode("names")
        setQuickOpen(true)
        return
      }
      if (typing) return
      if (mod && key === "a") {
        event.preventDefault()
        setSelected(new Set(entries.map((e) => e.path)))
        return
      }
      if (mod && (key === "c" || key === "x") && canWrite) {
        const paths = selected.size > 0 ? [...selected] : activeEntry ? [activeEntry.path] : []
        if (paths.length > 0) {
          event.preventDefault()
          cutCopy(key === "c" ? "copy" : "cut", paths)
        }
        return
      }
      if (mod && key === "v" && canWrite && clip) {
        event.preventDefault()
        void paste()
        return
      }
      if (mod) return

      // A control with the focus keeps its own keys: Space on a checkbox is
      // the checkbox's, Enter on a button is the button's.
      const onControl = target?.closest(
        "button, a, input, select, textarea, [role='checkbox'], [role='menuitem']",
      )
      switch (event.key) {
        case "Escape":
          clearSelection()
          setActive(null)
          break
        case "F2":
          if (activeEntry && canWrite) {
            event.preventDefault()
            rename(activeEntry)
          }
          break
        case "Delete":
          if (!canDestruct) break
          event.preventDefault()
          if (selected.size > 0) bulkDelete()
          else if (activeEntry) deleteEntry(activeEntry)
          break
        case "Backspace":
          if (parentReachable && parent) {
            event.preventDefault()
            navigate(parent)
          }
          break
        case "Enter":
          if (activeEntry && !onControl) {
            event.preventDefault()
            openEntry(activeEntry)
          }
          break
        case " ":
          if (activeEntry && !activeEntry.isDir && !onControl) {
            event.preventDefault()
            viewEntry(activeEntry)
          }
          break
        case "ArrowDown":
        case "ArrowUp":
        case "Home":
        case "End":
          if (onControl) break
          event.preventDefault()
          moveActive(event.key, event.shiftKey)
          break
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeEntry, canWrite, canDestruct, entries, path, selected, clip, parent, parentReachable])

  const openFileInput = () => fileInput.current?.click()
  const uploadFromInput = (input: HTMLInputElement) => {
    const incoming = collectInput(input.files)
    input.value = ""
    if (path) void handleIncoming(incoming, path)
  }

  const folderCount = entries.filter((e) => e.isDir).length
  const fileCount = entries.length - folderCount
  const selectedBytes = entries.reduce(
    (sum, e) => (selected.has(e.path) && !e.isDir ? sum + e.size : sum),
    0,
  )
  const dropInto = canWrite
    ? (paths: string[], dir: string, mode: DropMode) => void movePaths(paths, dir, mode)
    : undefined
  const dropFilesInto = canWrite
    ? (transfer: DataTransfer, dir: string) => void dropFiles(transfer, dir)
    : undefined

  return (
    <Page fill className="gap-2 px-2 py-2 md:px-3 md:py-3">
      <FolderColourProvider colours={colours} defaultColour={defaultColour}>
        {/* One frame around the whole workbench: a strip across the top, then
            the sidebar, the listing and the inspector separated by a hairline
            each rather than by a gutter and three borders — three framed
            panels with gaps between them read as three boxes floating on the
            page, and the screen is one working surface.

            There is no page header above it, the way the terminal and a Git
            working copy have none. "Workspace / Files" and a row of buttons
            took a band of the screen to say what the sidebar already says,
            and put the page's commands a hand's width from the folder they
            act on. They are in the strip now, beside where you are. */}
        <div
          style={
            {
              "--jd-files-rail": `${railPx}px`,
              "--jd-files-inspector": `${inspectorPx}px`,
            } as React.CSSProperties
          }
          className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-xl border bg-card"
        >
          <PaneHeader className="flex-wrap gap-x-1 gap-y-1.5 px-2 py-1.5">
            <IconAction
              label={showSidebar ? "Hide the sidebar" : "Show the sidebar"}
              aria-pressed={showSidebar}
              className="hidden size-8 lg:inline-flex"
              onClick={() => setShowSidebar(!showSidebar)}
            >
              {showSidebar ? <SidebarLeftClose /> : <SidebarLeftOpen />}
            </IconAction>
            {/* The sidebar lists the places; where it is not drawn, the same
                list sits behind this button so a phone can still jump. */}
            <div className={cn(showSidebar && "lg:hidden")}>
              <PlacesMenu
                places={places.data}
                recent={recent}
                current={path ?? "/"}
                onPick={navigate}
              >
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label="Places"
                  className="size-8 text-muted-foreground"
                >
                  <Location className="size-3.5" />
                </Button>
              </PlacesMenu>
            </div>

            {/* The strip's folder controls the colour of every folder; the
                inspector and folder menus still label one folder at a time. */}
            <div className="flex min-w-0 flex-1 basis-56 items-center gap-1">
              {here &&
                (canWrite ? (
                  <FolderColourMenu
                    value={defaultColour ?? "blue"}
                    onPick={(c) => void setDefaultColour(c)}
                    label="Colour all folders"
                  >
                    <button
                      type="button"
                      aria-label="Colour all folders"
                      className="inline-flex size-8 shrink-0 items-center justify-center rounded-md focus-ring transition-colors hover:bg-row-hover"
                    >
                      <FolderSwatch colour={defaultColour ?? "blue"} className="size-4" />
                    </button>
                  </FolderColourMenu>
                ) : (
                  <FolderIcon name={here.name} path={here.path} className="size-4" />
                ))}
              <PathBar
                path={path ?? "/"}
                home={places.data?.home}
                onNavigate={navigate}
                onDropPaths={dropInto}
                onDropFiles={dropFilesInto}
                className="min-w-0 flex-1"
              />
              {canWrite && here && (
                <IconAction
                  label={starred ? "Unstar this folder" : "Star this folder"}
                  className="size-8"
                  onClick={() => toggleStar(here.path, here.name)}
                >
                  {starred ? <StarFill className="text-warning" /> : <Star />}
                </IconAction>
              )}
            </div>

            {/* What you can do here: find, look, arrange, then make. */}
            <div className="ml-auto flex shrink-0 items-center gap-1">
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant="outline"
                    size="sm"
                    aria-label="Find"
                    className="text-muted-foreground hover:text-foreground md:w-40 md:justify-start"
                    onClick={() => {
                      setQuickMode("names")
                      setQuickOpen(true)
                    }}
                  >
                    <MagnifyingGlass className="size-4" />
                    <span className="hidden md:inline">Find</span>
                    <kbd className="ml-auto hidden rounded-sm border border-hairline px-1 text-micro md:inline">
                      ⌃P
                    </kbd>
                  </Button>
                </TooltipTrigger>
                <TooltipContent>Find files · Ctrl+P / ⌘P</TooltipContent>
              </Tooltip>
              <IconAction
                label="Search inside files"
                className="size-8"
                onClick={() => {
                  setQuickMode("content")
                  setQuickOpen(true)
                }}
              >
                <PreviewDocument />
              </IconAction>
              <IconAction label="Refresh" className="size-8" onClick={reload}>
                <RefreshClockwise />
              </IconAction>
              <ToggleGroup
                type="single"
                size="sm"
                variant="outline"
                value={view}
                onValueChange={(v) => v && setView(v)}
                aria-label="View"
                className="h-8"
              >
                <ToggleGroupItem value="list" aria-label="Details" className="h-8 min-w-8 px-1.5">
                  <ListUnordered className="size-3.5" />
                </ToggleGroupItem>
                <ToggleGroupItem value="grid" aria-label="Tiles" className="h-8 min-w-8 px-1.5">
                  <GridSquare className="size-3.5" />
                </ToggleGroupItem>
              </ToggleGroup>
              <ArrangeMenu
                sort={sort}
                setSort={setSort}
                showHidden={showHidden}
                setShowHidden={setShowHidden}
                tile={tile}
                setTile={setTile}
                grid={view === "grid"}
              />
              <span aria-hidden className="mx-1 hidden h-5 w-px bg-hairline md:block" />
              {/* Edits made here are committed somewhere else, so the account
                  those commits will carry belongs on this page too. */}
              <div className="hidden md:contents">
                <GitHubAccountControl compact="avatar" />
              </div>
              {canWrite && (
                <>
                  <NewMenu
                    onFolder={newFolder}
                    onFile={newFile}
                    onSymlink={() => setSymlinkOpen(true)}
                  />
                  <UploadMenu
                    onFiles={openFileInput}
                    onFolder={() => folderInput.current?.click()}
                  />
                  <input
                    ref={fileInput}
                    type="file"
                    multiple
                    hidden
                    onChange={(e) => uploadFromInput(e.currentTarget)}
                  />
                  <input
                    ref={folderInput}
                    type="file"
                    multiple
                    hidden
                    {...({ webkitdirectory: "" } as Record<string, string>)}
                    onChange={(e) => uploadFromInput(e.currentTarget)}
                  />
                </>
              )}
              <IconAction
                label={showInspector ? "Hide the details" : "Show the details"}
                aria-pressed={showInspector}
                className="hidden size-8 xl:inline-flex"
                onClick={() => setShowInspector(!showInspector)}
              >
                {showInspector ? <SidebarRightClose /> : <SidebarRightOpen />}
              </IconAction>
            </div>
          </PaneHeader>

          <div className="flex min-h-0 min-w-0 flex-1 flex-col lg:flex-row">
            {showSidebar && (
              <div className="relative hidden shrink-0 border-r border-hairline lg:flex lg:w-(--jd-files-rail)">
                <FilesSidebar
                  places={places.data}
                  path={path ?? "/"}
                  recent={recent}
                  canWrite={canWrite}
                  onNavigate={navigate}
                  onBookmarksChange={(next) => void saveBookmarks(next)}
                  onDropPaths={(paths, dir, mode) => void movePaths(paths, dir, mode)}
                  onDropFiles={(transfer, dir) => void dropFiles(transfer, dir)}
                />
                <ResizeHandle
                  side="left"
                  label="Sidebar width"
                  value={railPx}
                  min={RAIL.min}
                  max={RAIL.max}
                  onChange={(px, commit) => setRailWidth(clamp(px, RAIL.min, RAIL.max), commit)}
                  onReset={resetRailWidth}
                  className="absolute inset-y-0 -right-1 z-20"
                />
              </div>
            )}

            <div className="relative flex min-h-0 min-w-0 flex-1 flex-col">
              <div
                data-file-actions
                className="pointer-events-none absolute inset-x-2 bottom-12 z-20 flex justify-center"
              >
                <AnimatePresence initial={false} mode="wait">
                  {selected.size > 0 ? (
                    <motion.div
                      key="selection"
                      initial={{ opacity: 0, y: reducedMotion ? 0 : 4 }}
                      animate={{ opacity: 1, y: 0 }}
                      exit={{ opacity: 0, y: reducedMotion ? 0 : 4 }}
                      transition={{ duration: reducedMotion ? 0 : 0.16 }}
                      className="pointer-events-auto max-w-full"
                    >
                      <SelectionBar
                        count={selected.size}
                        size={selectedBytes}
                        canWrite={canWrite}
                        canDestruct={canDestruct}
                        archiveHref={archiveHref(path ?? "/", [...selected], "zip")}
                        onCopy={() => cutCopy("copy", [...selected])}
                        onCut={() => cutCopy("cut", [...selected])}
                        onDelete={bulkDelete}
                        onClear={clearSelection}
                      />
                    </motion.div>
                  ) : (
                    clip && (
                      <motion.div
                        key="clipboard"
                        initial={{ opacity: 0, y: reducedMotion ? 0 : 4 }}
                        animate={{ opacity: 1, y: 0 }}
                        exit={{ opacity: 0, y: reducedMotion ? 0 : 4 }}
                        transition={{ duration: reducedMotion ? 0 : 0.16 }}
                        className="pointer-events-auto flex max-w-full flex-wrap items-center gap-2 rounded-md border border-hairline bg-popover px-3 py-2 text-xs"
                      >
                        {clip.mode === "cut" ? (
                          <ArrowMove className="size-3.5 text-muted-foreground" />
                        ) : (
                          <Clipboard className="size-3.5 text-muted-foreground" />
                        )}
                        <span className="text-muted-foreground">
                          {plural(clip.paths.length, "item")} ready to{" "}
                          {clip.mode === "cut" ? "move" : "copy"}
                        </span>
                        <span className="flex-1" />
                        {canWrite && (
                          <Button size="xs" onClick={() => void paste()}>
                            <Clipboard className="size-3.5" />
                            Paste here
                          </Button>
                        )}
                        <IconAction
                          label="Forget the clipboard"
                          className="size-6"
                          onClick={() => setClip(null)}
                        >
                          <Cross />
                        </IconAction>
                      </motion.div>
                    )
                  )}
                </AnimatePresence>
              </div>

              {/* The body does not scroll; whatever is inside it does. That is
                  what keeps the table's header stuck to the top of the list: a
                  sticky header sticks to its nearest scrolling ancestor. The
                  right-click menu is scoped to the body too, so the path field
                  in the strip above keeps the browser's own. */}
              <ListingContextMenu
                caps={caps}
                resolve={(p) => byPath.get(p)}
                actionsFor={actionsFor}
                background={backgroundVerbs}
                onTarget={(entry) => entry && setActive({ dir: path ?? "/", entry })}
                className="relative flex min-h-0 flex-1 flex-col overflow-hidden"
              >
                <div
                  data-file-listing
                  className="@container relative flex min-h-0 flex-1 flex-col overflow-hidden"
                  {...marquee.handlers}
                  onDragEnd={() => setDragging(EMPTY)}
                  onDropCapture={() => setDragging(EMPTY)}
                  {...dropZone.handlers}
                >
                  {dropZone.over && (
                    <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center bg-wash-brand">
                      <span className="rounded-lg border border-dashed border-rule-brand bg-card px-4 py-2 text-body font-medium">
                        Drop to upload to {truncateMiddle(path ?? "/", 40)}
                      </span>
                    </div>
                  )}
                  {(listing.loading || path === null) && <LoadingRows className="p-4" />}
                  {listing.error && <ErrorState error={listing.error} className="m-4" />}

                  {listing.data && view === "grid" && (
                    <div
                      data-file-scroll
                      key={path}
                      className="min-h-0 flex-1 animate-rise overflow-auto"
                    >
                      {entries.length === 0 ? (
                        <EmptyFolder canWrite={canWrite} onUpload={openFileInput} />
                      ) : (
                        <GridView
                          entries={entries}
                          selected={selected}
                          activePath={activeEntry?.path ?? null}
                          dimmed={dimmed}
                          dragging={dragging}
                          caps={caps}
                          size={tile}
                          onToggle={toggleSelected}
                          onSelect={selectRow}
                          onOpen={openEntry}
                          onDragStart={canWrite ? dragStart : undefined}
                          onDropPaths={dropInto}
                          onDropFiles={dropFilesInto}
                        />
                      )}
                    </div>
                  )}

                  {listing.data && view === "list" && (
                    <div
                      key={path}
                      className="relative min-h-0 flex-1 animate-rise overflow-hidden"
                    >
                      <Table className="mb-24" containerClassName="h-full">
                        <TableHeader className={stickyTableHeader}>
                          <TableRow>
                            <TableHead className="w-8">
                              <Checkbox
                                aria-label="Select all"
                                checked={
                                  entries.length > 0 && selected.size === entries.length
                                    ? true
                                    : selected.size > 0
                                      ? "indeterminate"
                                      : false
                                }
                                onCheckedChange={(v) =>
                                  setSelected(
                                    v === true ? new Set(entries.map((e) => e.path)) : new Set(),
                                  )
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
                              align="right"
                            />
                            <SortHead
                              label="Modified"
                              k="modified"
                              sort={sort}
                              setSort={setSort}
                              className={COLUMN.modified}
                            />
                            <SortHead
                              label="Owner"
                              k="owner"
                              sort={sort}
                              setSort={setSort}
                              className={COLUMN.owner}
                            />
                            <SortHead
                              label="Mode"
                              k="mode"
                              sort={sort}
                              setSort={setSort}
                              className={cn("w-20", COLUMN.mode)}
                            />
                            <TableHead className="w-px" />
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {parentReachable && parent && (
                            <TableRow className="select-none" onActivate={() => navigate(parent)}>
                              <TableCell />
                              <TableCell colSpan={6}>
                                <span className="flex items-center gap-2 text-body text-muted-foreground">
                                  <ArrowUp className="size-3.5" />
                                  Parent folder
                                </span>
                              </TableCell>
                            </TableRow>
                          )}
                          {entries.map((entry) => (
                            <FileRow
                              key={entry.path}
                              entry={entry}
                              selected={selected.has(entry.path)}
                              active={activeEntry?.path === entry.path}
                              dimmed={dimmed.has(entry.path)}
                              dragging={dragging.has(entry.path)}
                              caps={caps}
                              onToggle={(checked) => toggleSelected(entry, checked)}
                              onSelect={(event) => selectRow(entry, event)}
                              onOpen={() => openEntry(entry)}
                              onDragStart={
                                canWrite ? (event) => dragStart(entry, event) : undefined
                              }
                              onDropPaths={dropInto}
                              onDropFiles={dropFilesInto}
                              actions={actionsFor(entry)}
                            />
                          ))}
                          {entries.length === 0 && (
                            <TableRow>
                              <TableCell colSpan={7} className="p-0">
                                <EmptyFolder canWrite={canWrite} onUpload={openFileInput} />
                              </TableCell>
                            </TableRow>
                          )}
                        </TableBody>
                      </Table>
                    </div>
                  )}
                  {marquee.box && (
                    <div
                      data-file-marquee
                      aria-hidden
                      className="pointer-events-none absolute z-10 rounded-sm border border-rule-brand bg-wash-brand"
                      style={{
                        left: marquee.box.left,
                        top: marquee.box.top,
                        width: Math.max(0, marquee.box.right - marquee.box.left),
                        height: Math.max(0, marquee.box.bottom - marquee.box.top),
                      }}
                    />
                  )}
                </div>
              </ListingContextMenu>

              <UploadStrip
                items={uploads.items}
                currentDir={path ?? "/"}
                onCancel={uploads.cancel}
                onDismiss={uploads.dismiss}
                onClear={uploads.clear}
              />

              <PaneFooter className="justify-between gap-3 px-3 text-hint text-muted-foreground">
                <span className="numeric min-w-0 truncate">
                  {listing.data
                    ? selected.size > 0
                      ? `${plural(selected.size, "item")} selected${
                          selectedBytes > 0 ? ` · ${bytes(selectedBytes)}` : ""
                        }`
                      : entries.length === 0
                        ? "Empty folder"
                        : [
                            folderCount > 0 && plural(folderCount, "folder"),
                            fileCount > 0 && plural(fileCount, "file"),
                          ]
                            .filter(Boolean)
                            .join(", ")
                    : ""}
                </span>
                {mount && (
                  <span className="flex min-w-0 shrink-0 items-center gap-2">
                    <Meter
                      value={mount.usedPercent}
                      tone={utilisationTone(mount.usedPercent)}
                      size="thin"
                      label={`Disk used on ${mount.mountpoint}`}
                      className="w-20"
                    />
                    <span className="numeric truncate">
                      {bytes(mount.free)} free on {mount.mountpoint}
                    </span>
                  </span>
                )}
              </PaneFooter>
            </div>

            {showInspector && (
              <div className="relative hidden shrink-0 flex-col border-l border-hairline xl:flex xl:w-(--jd-files-inspector)">
                <ResizeHandle
                  side="right"
                  label="Details width"
                  value={inspectorPx}
                  min={INSPECTOR.min}
                  max={INSPECTOR.max}
                  onChange={(px, commit) =>
                    setInspectorWidth(clamp(px, INSPECTOR.min, INSPECTOR.max), commit)
                  }
                  onReset={resetInspectorWidth}
                  className="absolute inset-y-0 -left-1 z-20"
                />
                <PreviewPanel
                  entry={activeEntry}
                  folder={here}
                  canWrite={canWrite}
                  onColour={(entry, colour) => void setColour(entry, colour)}
                  onOpen={(p) => setEditing(p)}
                  onView={viewEntry}
                  onEditImage={(p) => setEditingImage(p)}
                  onNavigate={navigate}
                  className="flex-1"
                />
              </div>
            )}
          </div>
        </div>
      </FolderColourProvider>

      <QuickOpen
        open={quickOpen}
        onOpenChange={setQuickOpen}
        root={path ?? "/"}
        home={places.data?.home}
        entries={listing.data?.entries}
        initialMode={quickMode}
        onOpenPath={(p, isDir, line) => {
          if (isDir) navigate(p)
          else {
            setSearchLocation({ path: p, line })
            setEditing(p)
          }
        }}
      />
      <MediaViewer
        items={viewable}
        index={viewing}
        canWrite={canWrite}
        onClose={() => setViewing(null)}
        onIndexChange={(i) => {
          setViewing(i)
          const entry = viewable[i]
          if (entry) setActive({ dir: path ?? "/", entry })
        }}
        onEditImage={(p) => {
          setViewing(null)
          setEditingImage(p)
        }}
        onOpenEditor={(p) => {
          setViewing(null)
          setEditing(p)
        }}
      />
      <FileEditorSheet
        path={editing}
        root={path ?? undefined}
        revealLine={searchLocation?.path === editing ? searchLocation.line : undefined}
        onOpenChange={(open) => !open && setEditing(null)}
        onSaved={reload}
      />
      <ImageEditorSheet
        path={editingImage}
        root={path ?? undefined}
        modified={activeEntry?.path === editingImage ? activeEntry?.modified : undefined}
        onOpenChange={(open) => !open && setEditingImage(null)}
        onSaved={() => {
          setActive(null)
          reload()
        }}
      />
      <PermissionsDialog
        entry={permsEntry}
        onOpenChange={(open) => !open && setPermsEntry(null)}
        onDone={reload}
      />
      <SymlinkDialog
        open={symlinkOpen}
        dir={path ?? "/"}
        onOpenChange={setSymlinkOpen}
        onDone={reload}
      />
      {dialog}
      {promptDialog}
      {conflicts.dialog}
    </Page>
  )
}

function EmptyFolder({ canWrite, onUpload }: { canWrite: boolean; onUpload: () => void }) {
  return (
    <EmptyState
      className="m-4"
      icon={FolderOpen}
      title="This folder is empty"
      description={canWrite ? "Drop files or folders here to upload them, or use New." : undefined}
      action={
        canWrite && (
          <Button size="sm" variant="outline" onClick={onUpload}>
            <CloudUpload className="size-4" />
            Upload files
          </Button>
        )
      }
    />
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
  sort: Sort
  setSort: (s: Sort) => void
  showHidden: boolean
  setShowHidden: (v: boolean) => void
  tile: TileSize
  setTile: (v: TileSize) => void
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
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon-sm"
              className="size-8 text-muted-foreground"
              aria-label="Arrange"
            >
              <SettingsSliders className="size-3.5" />
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent>Sort, hidden files, tile size</TooltipContent>
      </Tooltip>
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
  align,
  className,
}: {
  label: string
  k: SortKey
  sort: Sort
  setSort: (s: Sort) => void
  align?: "right"
  className?: string
}) {
  const active = sort.key === k
  return (
    <TableHead className={cn(align === "right" && "text-right", className)}>
      <button
        className={cn(
          "inline-flex items-center gap-1 hover:text-foreground",
          active && "text-foreground",
          align === "right" && "flex-row-reverse",
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

/** Selection commands float within the workbench, so the entries keep their positions. */
function SelectionBar({
  count,
  size,
  canWrite,
  canDestruct,
  archiveHref,
  onCopy,
  onCut,
  onDelete,
  onClear,
}: {
  count: number
  size: number
  canWrite: boolean
  canDestruct: boolean
  archiveHref: string
  onCopy: () => void
  onCut: () => void
  onDelete: () => void
  onClear: () => void
}) {
  return (
    <div
      role="toolbar"
      aria-label="Selection actions"
      className="flex flex-wrap items-center gap-1.5 rounded-md border border-hairline bg-popover px-2.5 py-2"
    >
      <span className="numeric mr-1 text-body font-medium">
        {plural(count, "item")} selected
        {size > 0 && (
          <span className="ml-1.5 font-normal text-muted-foreground">{bytes(size)}</span>
        )}
      </span>
      {canWrite && (
        <>
          <Button variant="outline" size="xs" onClick={onCopy}>
            <Clipboard className="size-3.5" />
            Copy
          </Button>
          <Button variant="outline" size="xs" onClick={onCut}>
            <ArrowMove className="size-3.5" />
            Cut
          </Button>
        </>
      )}
      <Button variant="outline" size="xs" asChild>
        <a href={archiveHref} download>
          <FileZip className="size-3.5" />
          Download .zip
        </a>
      </Button>
      {canDestruct && (
        <Button variant="outline" size="xs" className="text-destructive" onClick={onDelete}>
          <Trash className="size-3.5" />
          Delete
        </Button>
      )}
      <span className="flex-1" />
      <IconAction label="Clear the selection" className="size-6" onClick={onClear}>
        <Cross />
      </IconAction>
    </div>
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
        <Button variant="outline" size="sm" aria-label="New">
          <Plus className="size-4" />
          <span className="hidden sm:inline">New</span>
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

/**
 * The page's one command. Files, or a whole folder with its tree intact —
 * the folder picker is what makes "put this site here" a single gesture
 * rather than a directory made by hand and forty files dragged into it.
 */
function UploadMenu({ onFiles, onFolder }: { onFiles: () => void; onFolder: () => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="sm" aria-label="Upload">
          <CloudUpload className="size-4" />
          <span className="hidden sm:inline">Upload</span>
          <ChevronDown className="size-3 opacity-70" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-44">
        <DropdownMenuItem onSelect={onFiles}>
          <CloudUpload className="size-3.5" />
          Files…
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={onFolder}>
          <FolderOpen className="size-3.5" />
          Folder…
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
      await post("/files/symlink", { target: target.trim(), link: joinPath(dir, name.trim()) })
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
            {joinPath(dir, name || "…")} → {target || "…"}
          </p>
        </div>
      </div>
    </Modal>
  )
}
