"use client"

import { useCallback, useEffect, useState } from "react"
import { useRouter } from "next/navigation"
import {
  ArrowLeftRight,
  CodeWrap,
  FloppyDisk,
  Fullscreen,
  FullscreenClose,
  Location,
  MagnifyingGlass,
  RotateCounterClockwise,
  ShieldOff,
  Sparkles,
  External,
  CornerUpLeft,
  CornerUpRight,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post, put } from "@/lib/api"
import { bytes } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileContent } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { CodeEditor, type EditorCommands } from "@/components/code-editor"
import { DiffView } from "@/components/files/diff-view"
import { unifiedDiff } from "@/components/files/diff"
import { isImage, rawUrl } from "@/components/files/media"
import { EditorSurface } from "./editor-surface"
import { FileIcon } from "./file-icon"
import { handoffDraft, takeDraft } from "./editor-handoff"
import { editorHref } from "./search"
import { useConfirm } from "@/components/confirm-dialog"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

/**
 * The languages the editor will highlight as, for the file whose extension
 * says nothing — a systemd unit written as `web`, a script called `deploy`.
 * The server guesses one; this is how you override it.
 */
const LANGUAGES = [
  "plaintext",
  "shell",
  "ini",
  "yaml",
  "json",
  "markdown",
  "nginx",
  "dockerfile",
  "javascript",
  "typescript",
  "go",
  "python",
  "sql",
  "html",
  "css",
  "xml",
  "toml",
  "rust",
  "ruby",
  "php",
  "java",
  "c",
  "cpp",
  "lua",
  "perl",
  "powershell",
  "diff",
]

export function FileEditorSheet({
  path,
  onOpenChange,
  onSaved,
  root,
  revealLine,
  destination,
  onDirtyChange,
}: {
  path: string | null
  onOpenChange: (open: boolean) => void
  onSaved?: (path: string) => void
  root?: string
  revealLine?: number
  destination?: boolean
  onDirtyChange?: (dirty: boolean) => void
}) {
  return (
    <FileEditorPanel
      // Keyed on the path: opening another file must not inherit the previous
      // file's unsaved draft.
      key={path ?? "none"}
      path={path}
      onOpenChange={onOpenChange}
      onSaved={onSaved}
      root={root}
      revealLine={revealLine}
      destination={destination}
      onDirtyChange={onDirtyChange}
    />
  )
}

function FileEditorPanel({
  path,
  onOpenChange,
  onSaved,
  root,
  revealLine,
  destination,
  onDirtyChange,
}: {
  path: string | null
  onOpenChange: (open: boolean) => void
  onSaved?: (path: string) => void
  root?: string
  revealLine?: number
  destination?: boolean
  onDirtyChange?: (dirty: boolean) => void
}) {
  const { can } = useAuth()
  const router = useRouter()
  const [handoff] = useState(() => (path && destination ? takeDraft(path) : undefined))
  const [commands, setCommands] = useState<EditorCommands>()
  const [indent, setIndent] = useViewState("files.editor.indent", "2")
  const { confirm, dialog } = useConfirm()
  const [file, setFile] = useState<FileContent>()
  const [draft, setDraft] = useState("")
  const [error, setError] = useState<Error>()
  const [saving, setSaving] = useState(false)
  const [mode, setMode] = useState("")
  const [language, setLanguage] = useState<string>()
  const [cursor, setCursor] = useState({ line: 1, column: 1, selected: 0 })
  const [format, setFormat] = useState<(() => void) | null>(null)
  // Reviewing swaps the editor for a diff of the draft against the disk. It
  // is the last look before Save on a file that keeps a server up, and it is
  // not remembered: it is a question about this edit, not about the editor.
  const [reviewing, setReviewing] = useState(false)

  // How the editor is set up is furniture — it belongs to the person, not to
  // the file — so it is remembered across files and across visits.
  const [wrap, setWrap] = useViewState("files.editor.wrap", false)
  const [minimap, setMinimap] = useViewState("files.editor.minimap", false)
  const [fontSize, setFontSize] = useViewState("files.editor.fontSize", 13)
  const [fullscreen, setFullscreen] = useViewState("files.editor.fullscreen", false)

  const load = useCallback(
    (signal?: AbortSignal) => {
      if (!path) return
      get<FileContent>("/files/read", { path }, signal)
        .then((f) => {
          if (signal?.aborted) return
          setFile(f)
          setDraft(handoff?.content ?? f.content)
          setMode(f.modeOctal)
          setLanguage(handoff?.language ?? (nameLanguage(path) || f.language))
        })
        .catch((err) => !signal?.aborted && setError(err))
    },
    [handoff, path],
  )

  useEffect(() => {
    const controller = new AbortController()
    load(controller.signal)
    return () => controller.abort()
  }, [load])

  const dirty = file !== undefined && draft !== file.content
  useEffect(() => {
    onDirtyChange?.(dirty)
  }, [dirty, onDirtyChange])
  const canEdit = can("file.write")
  const name = path?.split("/").pop() ?? "file"
  const review = reviewing && file ? unifiedDiff(file.content, draft, name) : undefined

  const save = useCallback(
    async (target?: string) => {
      if (!path || saving || !file || file.binary || !canEdit) return
      const destination = target ?? path
      setSaving(true)
      try {
        await put("/files/write", { path: destination, content: draft })
        notify.success("Saved", { description: destination })
        if (destination === path) setFile((f) => (f ? { ...f, content: draft } : f))
        onSaved?.(destination)
      } catch (err) {
        notify.error("Could not save", err)
      } finally {
        setSaving(false)
      }
    },
    [canEdit, draft, file, onSaved, path, saving],
  )

  const applyMode = async () => {
    if (!path) return
    try {
      await post("/files/chmod", { path, mode })
      notify.success(`Mode set to ${mode}`)
      setFile((f) => (f ? { ...f, modeOctal: mode } : f))
      onSaved?.(path)
    } catch (err) {
      notify.error("Could not change mode", err)
    }
  }

  const revert = () =>
    confirm({
      title: "Discard your changes",
      description: <p>The editor goes back to what is on disk. Nothing is written.</p>,
      confirmLabel: "Discard",
      action: async () => {
        setError(undefined)
        setFile(undefined)
        if (path)
          get<FileContent>("/files/read", { path })
            .then((f) => {
              setFile(f)
              setDraft(f.content)
              setMode(f.modeOctal)
            })
            .catch(setError)
      },
    })

  // Closing with unsaved work asks first. Every other way out of this panel —
  // Escape, the overlay, the X — comes through here, so one guard covers them
  // all rather than one per affordance.
  const requestClose = (open: boolean) => {
    if (open || !dirty) {
      onOpenChange(open)
      return
    }
    confirm({
      title: "Close without saving?",
      description: (
        <p>
          <b>{path?.split("/").pop()}</b> has changes that have not been written to disk.
        </p>
      ),
      confirmLabel: "Close and lose them",
      action: async () => onOpenChange(false),
    })
  }

  return (
    <EditorSurface
      destination={destination}
      open={path !== null}
      onOpenChange={requestClose}
      width="xl"
      // The whole window, for the file that is the afternoon's work: a
      // sheet that leaves a strip of the listing showing is right for a
      // glance at a config and wrong for editing one.
      className={cn(fullscreen && "sm:max-w-none")}
      title={
        <>
          {path && (
            <FileIcon
              entry={{ name, path, isDir: false, isSymlink: false }}
              className="size-5 shrink-0"
            />
          )}
          {path?.split("/").pop() ?? "File"}
          {dirty && <Tag tone="warning">unsaved</Tag>}
        </>
      }
      description={path ?? undefined}
      bodyClassName="flex min-h-0 flex-1 flex-col"
      actions={
        file &&
        !file.binary && (
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
            <Select value={language} onValueChange={setLanguage}>
              <SelectTrigger size="sm" aria-label="File language" className="h-8 w-32 text-body">
                <SelectValue />
              </SelectTrigger>
              <SelectContent className="max-h-72">
                {LANGUAGES.map((lang) => (
                  <SelectItem key={lang} value={lang} className="text-xs">
                    {lang}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button
              size="sm"
              variant="ghost"
              disabled={!commands}
              onClick={() => commands?.run("actions.find")}
            >
              <MagnifyingGlass className="size-3.5" />
              Find
            </Button>
            {canEdit && (
              <Button
                size="sm"
                variant="ghost"
                disabled={!commands}
                onClick={() => commands?.run("editor.action.startFindReplaceAction")}
              >
                Replace
              </Button>
            )}
            <Toggle label="Undo" disabled={!commands || !canEdit} onClick={() => commands?.undo()}>
              <CornerUpLeft className="size-3.5" />
            </Toggle>
            <Toggle label="Redo" disabled={!commands || !canEdit} onClick={() => commands?.redo()}>
              <CornerUpRight className="size-3.5" />
            </Toggle>
            <Toggle label="Wrap long lines" active={wrap} onClick={() => setWrap((v) => !v)}>
              <CodeWrap className="size-3.5" />
            </Toggle>
            <Toggle label="Minimap" active={minimap} onClick={() => setMinimap((v) => !v)}>
              <Location className="size-3.5" />
            </Toggle>
            {/* Two glyphs that differ only in size read as one button drawn
                twice, so each says which way it goes. */}
            <Toggle label="Smaller text" onClick={() => setFontSize((v) => Math.max(10, v - 1))}>
              <span className="text-hint leading-none font-semibold">A−</span>
            </Toggle>
            <Toggle label="Larger text" onClick={() => setFontSize((v) => Math.min(22, v + 1))}>
              <span className="text-body leading-none font-semibold">A+</span>
            </Toggle>
            {canEdit && (
              <Toggle label="Format this document" disabled={!commands} onClick={() => format?.()}>
                <Sparkles className="size-3.5" />
              </Toggle>
            )}
            {canEdit && dirty && (
              <Toggle label="Discard changes and reload from disk" onClick={revert}>
                <RotateCounterClockwise className="size-3.5" />
              </Toggle>
            )}
            {dirty && (
              <Toggle
                label={reviewing ? "Back to editing" : "Review the changes before saving"}
                active={reviewing}
                onClick={() => setReviewing((v) => !v)}
              >
                <ArrowLeftRight className="size-3.5" />
              </Toggle>
            )}
            {!destination && (
              <Toggle
                label={fullscreen ? "Leave full screen" : "Full screen"}
                active={fullscreen}
                onClick={() => setFullscreen((v) => !v)}
              >
                {fullscreen ? (
                  <FullscreenClose className="size-3.5" />
                ) : (
                  <Fullscreen className="size-3.5" />
                )}
              </Toggle>
            )}
            <Select value={indent} onValueChange={setIndent}>
              <SelectTrigger size="sm" aria-label="Indentation" className="h-8 w-24 text-body">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {["2", "4", "8"].map((size) => (
                  <SelectItem key={size} value={size}>
                    {size} spaces
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button
              size="sm"
              variant="ghost"
              disabled={!commands}
              onClick={() => commands?.run("editor.action.quickCommand")}
            >
              Commands
            </Button>
            {!destination && (
              <Button
                size="sm"
                variant="outline"
                className="ml-auto"
                onClick={() => {
                  if (!path) return
                  handoffDraft(path, draft, language)
                  router.push(editorHref(path, root, revealLine))
                }}
              >
                <External className="size-3.5" />
                Open full editor
              </Button>
            )}
          </div>
        )
      }
      footer={
        file ? (
          <>
            {/* chmod is a system.admin route, so the mode control only appears
                for an admin — showing it to a file.write user guaranteed a 403. */}
            {can("system.admin") && (
              <>
                <Label htmlFor="file-mode" className="text-xs text-muted-foreground">
                  Mode
                </Label>
                <Input
                  id="file-mode"
                  value={mode}
                  onChange={(e) => setMode(e.target.value)}
                  className="h-8 w-20 font-mono text-xs"
                />
                <Button
                  size="sm"
                  variant="outline"
                  onClick={applyMode}
                  disabled={mode === file.modeOctal}
                >
                  Apply
                </Button>
              </>
            )}
            <span className="flex-1" />
            {!file.binary && (
              <span className="numeric text-xs text-muted-foreground">
                Ln {cursor.line}, Col {cursor.column}
                {cursor.selected > 0 && ` · ${cursor.selected} selected`} ·{" "}
                {bytes(new TextEncoder().encode(draft).length)}
              </span>
            )}
            {canEdit && !file.binary && (
              <>
                <SaveAsButton
                  path={path ?? ""}
                  disabled={saving}
                  onSave={(target) => void save(target)}
                />
                <Button
                  size="sm"
                  onClick={() => void save()}
                  disabled={!dirty || saving}
                  pending={saving}
                >
                  <FloppyDisk className="size-4" />
                  Save
                </Button>
              </>
            )}
          </>
        ) : undefined
      }
    >
      <div className="flex shrink-0 items-center justify-between gap-3 border-b border-hairline px-3 py-1.5 text-hint text-muted-foreground">
        <span className="truncate font-mono" title={path ?? undefined}>
          {path}
        </span>
        <span className="shrink-0">
          {dirty ? "Unsaved changes" : canEdit ? "Saved to disk" : "Read only"}
        </span>
      </div>
      {error && <ErrorState error={error} className="m-4" />}
      {!file && !error && <LoadingRows className="p-4" />}

      {/* An image is shown rather than refused. It comes from the raw route,
          which serves it with a content type the browser will draw: the
          download route's octet-stream is refused by nosniff. */}
      {file?.binary && path && isImage(path) && (
        <div className="flex min-h-0 flex-1 items-center justify-center overflow-auto checkerboard p-4">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            src={rawUrl(path)}
            alt={path}
            className="max-h-full max-w-full rounded-md object-contain"
          />
        </div>
      )}

      {file?.binary && !(path && isImage(path)) && (
        <Notice className="m-4" tone="warning" title="Binary file" icon={ShieldOff}>
          This looks like a binary file ({bytes(file.size)}); it is not shown in the editor. Use
          Download to open it locally.
        </Notice>
      )}

      {file && !file.binary && reviewing && (
        <div className="flex min-h-0 flex-1 flex-col">
          {review === null ? (
            <Notice className="m-4" title="Too many changes to summarise">
              The draft differs from the file on disk in more places than can be aligned here. Save
              writes the whole draft; Discard goes back to the disk.
            </Notice>
          ) : review === "" ? (
            <Notice className="m-4" title="No changes">
              The draft is identical to the file on disk.
            </Notice>
          ) : (
            <DiffView body={review ?? ""} singleFile className="min-h-0 flex-1" />
          )}
        </div>
      )}

      {file && !file.binary && !reviewing && (
        <CodeEditor
          filePath={path ?? undefined}
          tabSize={Number(indent)}
          revealLine={revealLine}
          onReady={setCommands}
          className="flex-1"
          value={draft}
          onChange={setDraft}
          language={language ?? file.language}
          readOnly={!canEdit}
          wordWrap={wrap}
          minimap={minimap}
          fontSize={fontSize}
          onSave={() => dirty && void save()}
          onCursorChange={setCursor}
          onFormat={(fn) => setFormat(() => fn)}
        />
      )}
      {dialog}
    </EditorSurface>
  )
}

function nameLanguage(path: string) {
  const name = path.split("/").pop()?.toLowerCase() ?? ""
  if (name === "bun.lock" || name === "composer.lock") return "json"
  if (name === "dockerfile" || name.startsWith("dockerfile.")) return "dockerfile"
  if (name === ".env" || name.startsWith(".env.")) return "ini"
  const extension = name.split(".").pop() ?? ""
  return (
    { tsx: "typescript", jsx: "javascript", mdx: "markdown", yml: "yaml" } as Record<string, string>
  )[extension]
}

function Toggle({
  label,
  active,
  disabled,
  onClick,
  children,
}: {
  label: string
  active?: boolean
  disabled?: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          size="icon-sm"
          variant={active ? "secondary" : "ghost"}
          aria-label={label}
          aria-pressed={active}
          className={cn(!active && "text-muted-foreground")}
          onClick={onClick}
          disabled={disabled}
        >
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

/**
 * Save under another name, in the same folder.
 *
 * It is the cheapest possible backup before editing something that keeps a
 * server up, and it is why the pattern `cp nginx.conf nginx.conf.bak` exists
 * at all — done here it needs neither a shell nor a second trip through the
 * file list.
 */
function SaveAsButton({
  path,
  disabled,
  onSave,
}: {
  path: string
  disabled?: boolean
  onSave: (target: string) => void
}) {
  const [name, setName] = useState("")
  const [open, setOpen] = useState(false)
  const dir = path.slice(0, path.lastIndexOf("/")) || "/"
  const base = path.split("/").pop() ?? ""

  if (!open) {
    return (
      <Button
        size="sm"
        variant="outline"
        disabled={disabled}
        onClick={() => {
          setName(`${base}.bak`)
          setOpen(true)
        }}
      >
        Save as
      </Button>
    )
  }
  const target = `${dir}/${name}`.replace(/\/{2,}/g, "/")
  return (
    <span className="flex items-center gap-1.5">
      <Input
        autoFocus
        value={name}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Escape") setOpen(false)
          if (e.key === "Enter" && name.trim() && !name.includes("/")) {
            onSave(target)
            setOpen(false)
          }
        }}
        className="h-8 w-48 font-mono text-xs"
      />
      <Button
        size="sm"
        variant="outline"
        disabled={!name.trim() || name.includes("/")}
        onClick={() => {
          onSave(target)
          setOpen(false)
        }}
      >
        Write
      </Button>
    </span>
  )
}
