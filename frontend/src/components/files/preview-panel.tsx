"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import {
  ArrowUpRight,
  Box,
  Calculator,
  Check,
  ChevronRight,
  Clock,
  CodeBracket,
  Copy,
  Crop,
  Download,
  Eye,
  File,
  Fingerprint,
  FolderClosed,
  FolderOpen,
  Linked,
  ListOrdered,
  Minus,
  Pencil,
  Users,
  type Icon,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { downloadUrl, get } from "@/lib/api"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileChecksum, FileEntry, FilePreview, FileUsage } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Well } from "@/components/panel"
import { ErrorState, LoadingRows } from "@/components/state"
import { IconAction } from "@/components/icon-action"
import { BarList } from "@/components/bar-list"
import {
  FileIcon,
  fileKind,
  kindOfEntry,
  useFolderColour,
  type FolderColour,
} from "@/components/files/file-icon"
import { FolderColourSwatches } from "@/components/files/folder-colour"
import { copyText } from "@/lib/clipboard"
import { baseOf, parentOf, rawUrl } from "@/components/files/media"
import { accessRows, accessSummary, type Access } from "@/components/files/access"
import { ArchiveListing, PdfPreview, TextHead } from "@/components/files/preview-bits"

export { rawUrl } from "@/components/files/media"

/**
 * The inspector beside the listing: what one click gets you.
 *
 * Opening a file used to mean loading it into the editor — the right answer
 * for a config file and the wrong one for a 200 MB log, a JPEG, a tarball and
 * a binary, three of which arrived at the same sheet only to be refused. A
 * single click now asks the server what the thing *is* and shows that: the
 * first hundred lines, the picture, the contents of the archive, the size of
 * the directory. Opening it — the viewer, the editor, the image editor, the
 * download — is the deliberate second action.
 *
 * It opens on the thing itself, large, on a stage a step darker than the
 * column — the picture, the video, or the folder or page it is drawn as — with
 * its name, its format in the format's own colour, and the verbs that apply to
 * it under that: the one most people want, named, and the rest as a row of
 * glyphs. Below it the facts are grouped and read as sentences rather than as
 * a column of bare values: when it changed and the date, who can do what with
 * it (the mode as a grid, and in words), and where it lives as a path you can
 * walk up. With nothing chosen it describes the folder being browsed rather
 * than asking to be clicked.
 *
 * Nothing here loads a whole file. The text is a head the server trimmed, the
 * image is a URL the browser fetches itself, and the recursive size of a
 * directory is asked for rather than computed on hover.
 */
export function PreviewPanel({
  entry,
  folder,
  canWrite,
  onColour,
  onOpen,
  onView,
  onEditImage,
  onNavigate,
  className,
}: {
  entry: FileEntry | null
  /** The folder being browsed, shown while nothing in it is chosen. */
  folder: FileEntry | null
  canWrite: boolean
  onColour: (entry: FileEntry, colour: FolderColour) => void
  /** The editor. */
  onOpen: (path: string) => void
  /** The full-screen viewer. */
  onView: (entry: FileEntry) => void
  onEditImage: (path: string) => void
  onNavigate: (path: string) => void
  className?: string
}) {
  const shown = entry ?? folder
  if (!shown) {
    return (
      <div className={cn("flex min-h-0 flex-col", className)}>
        <LoadingRows rows={3} className="p-4" />
      </div>
    )
  }
  // Keyed on the path so the fetch, the checksum and everything else about
  // one entry is thrown away by React when the selection moves, rather than
  // by an effect that clears four pieces of state on the way past.
  return (
    <Preview
      key={shown.path}
      entry={shown}
      current={!entry}
      canWrite={canWrite}
      onColour={onColour}
      onOpen={onOpen}
      onView={onView}
      onEditImage={onEditImage}
      onNavigate={onNavigate}
      className={className}
    />
  )
}

type PreviewProps = {
  entry: FileEntry
  /** The folder being browsed, rather than a row in it. */
  current: boolean
  canWrite: boolean
  onColour: (entry: FileEntry, colour: FolderColour) => void
  onOpen: (path: string) => void
  onView: (entry: FileEntry) => void
  onEditImage: (path: string) => void
  onNavigate: (path: string) => void
  className?: string
}

function Preview({ className, ...props }: PreviewProps) {
  const { entry } = props
  const [preview, setPreview] = useState<FilePreview>()
  const [error, setError] = useState<Error>()
  const path = entry.path
  const modified = entry.modified

  // Re-read when the entry's own modification time moves — a saved edit, a
  // replaced picture — so the head and the facts are the file as it is now.
  useEffect(() => {
    const controller = new AbortController()
    get<FilePreview>("/files/preview", { path }, controller.signal)
      .then(setPreview)
      .catch((err) => !controller.signal.aborted && setError(err))
    return () => controller.abort()
  }, [path, modified])

  return (
    <div data-files-inspector className={cn("flex min-h-0 flex-col overflow-auto", className)}>
      <Hero {...props} preview={preview} />
      {error && <ErrorState error={error} className="m-4" />}
      {!preview && !error && <LoadingRows rows={4} className="p-4" />}
      {preview && (
        <>
          <Contents {...props} preview={preview} />
          <Details entry={entry} preview={preview} />
          <AccessSection preview={preview} isDir={entry.isDir} />
          <Location {...props} />
        </>
      )}
    </div>
  )
}

/**
 * The thing, large, what it is called and what it is, and what can be done
 * with it. A picture or a video is itself; everything else is the folder or
 * the page the listing draws it as, at a size where the page can carry its
 * extension.
 */
function Hero({
  entry,
  preview,
  current,
  canWrite,
  onColour,
  onOpen,
  onView,
  onEditImage,
  onNavigate,
}: PreviewProps & { preview: FilePreview | undefined }) {
  const colour = useFolderColour(entry.path, entry.name)
  const kind = entry.isDir ? null : fileKind(entry.name)
  const facts = [
    current ? "This folder" : kindOfEntry(entry).label,
    preview?.kind === "dir"
      ? plural(preview.childCount ?? 0, "item")
      : preview && !entry.isDir
        ? bytes(preview.size)
        : null,
  ].filter(Boolean)

  return (
    <div className="flex flex-col gap-3 border-b border-hairline p-4">
      {preview?.kind === "image" ? (
        <button
          type="button"
          className="flex h-44 items-center justify-center overflow-hidden rounded-lg border border-hairline checkerboard p-2 focus-ring"
          onClick={() => onView(entry)}
          title="View full screen"
        >
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            src={rawUrl(entry.path, preview.modified)}
            alt={entry.name}
            className="max-h-full max-w-full object-contain"
          />
        </button>
      ) : preview?.kind === "video" ? (
        <video
          src={rawUrl(entry.path, preview.modified)}
          controls
          preload="metadata"
          className="h-44 w-full rounded-lg border border-hairline bg-black object-contain"
        />
      ) : (
        <FileIcon entry={entry} detail className="mx-auto mt-2 size-24" />
      )}
      <div className="min-w-0 space-y-1 text-center">
        <h2
          className="line-clamp-2 text-title leading-snug font-semibold break-all"
          title={entry.name}
        >
          {entry.name}
        </h2>
        <p className="numeric flex items-center justify-center gap-1.5 truncate text-hint text-muted-foreground">
          {kind?.ext && (
            <span className="font-mono font-semibold uppercase" style={{ color: kind.tone }}>
              {kind.ext}
            </span>
          )}
          <span className="truncate">{facts.join(" · ")}</span>
          {preview?.width ? (
            <span>
              · {preview.width}×{preview.height}
            </span>
          ) : null}
        </p>
      </div>
      {entry.isDir && canWrite && (
        <FolderColourSwatches
          value={colour}
          onPick={(next) => onColour(entry, next)}
          className="justify-center"
        />
      )}
      {preview && (
        <Verbs
          entry={entry}
          preview={preview}
          current={current}
          canWrite={canWrite}
          onOpen={onOpen}
          onView={onView}
          onEditImage={onEditImage}
          onNavigate={onNavigate}
        />
      )}
    </div>
  )
}

/**
 * What can be done with it: the verb most people want, named and wide, and
 * the rest as glyphs beside it, each with its name in a tooltip. They had been
 * five outlined words wrapping onto two lines, all the same weight, so the
 * one that mattered had to be read for.
 */
function Verbs({
  entry,
  preview,
  current,
  canWrite,
  onOpen,
  onView,
  onEditImage,
  onNavigate,
}: Omit<PreviewProps, "onColour" | "className"> & { preview: FilePreview }) {
  const [sum, setSum] = useState<FileChecksum>()
  const [hashing, setHashing] = useState(false)
  const abort = useRef<AbortController>(null)

  // Nothing resets `sum` on a new selection because nothing has to: the whole
  // panel is keyed on the path, so a different entry is a different component.
  // This only abandons a hash still running when the panel goes away.
  useEffect(() => () => abort.current?.abort(), [])

  if (preview.kind === "dir") {
    if (current) return null
    return (
      <Button size="sm" variant="outline" onClick={() => onNavigate(entry.path)}>
        <FolderOpen className="size-3.5" />
        Open folder
      </Button>
    )
  }

  const checksum = async () => {
    setHashing(true)
    abort.current = new AbortController()
    try {
      setSum(await get<FileChecksum>("/files/checksum", { path: entry.path }, abort.current.signal))
    } catch (err) {
      if (!abort.current.signal.aborted) notify.error("Could not hash this file", err)
    } finally {
      setHashing(false)
    }
  }

  const download = downloadUrl("/files/download", { path: entry.path })
  const opaque = preview.kind === "binary" || preview.kind === "archive"
  const main = preview.editable
    ? { label: canWrite ? "Edit" : "Open", icon: Pencil, run: () => onOpen(entry.path) }
    : opaque
      ? null
      : { label: "View", icon: Eye, run: () => onView(entry) }

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-1.5">
        {main ? (
          <Button size="sm" variant="outline" className="flex-1" onClick={main.run}>
            <main.icon className="size-3.5" />
            {main.label}
          </Button>
        ) : (
          <Button size="sm" variant="outline" className="flex-1" asChild>
            <a href={download} download>
              <Download className="size-3.5" />
              Download
            </a>
          </Button>
        )}
        {preview.editable && (
          <IconAction variant="outline" label="View" onClick={() => onView(entry)}>
            <Eye />
          </IconAction>
        )}
        {preview.kind === "image" && canWrite && (
          <IconAction
            variant="outline"
            label="Crop, rotate, resize"
            onClick={() => onEditImage(entry.path)}
          >
            <Crop />
          </IconAction>
        )}
        {main && (
          <IconAction variant="outline" label="Download" asChild>
            <a href={download} download>
              <Download />
            </a>
          </IconAction>
        )}
        <IconAction variant="outline" label="Checksum" onClick={checksum} pending={hashing}>
          <Fingerprint />
        </IconAction>
      </div>
      {sum && (
        <button
          className="w-full rounded-md border border-hairline bg-surface-sunken p-2 text-left font-mono text-micro break-all transition-colors hover:border-border-strong"
          onClick={() => void copyText(sum.sum, "Checksum copied")}
          title="Copy the checksum"
        >
          <span className="mr-1 text-muted-foreground uppercase">{sum.algo}</span>
          {sum.sum}
        </button>
      )}
    </div>
  )
}

/** One group of the inspector: an eyebrow, an optional control at its end, and its body. */
function Section({
  title,
  aside,
  children,
}: {
  title: string
  aside?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <section className="space-y-2.5 border-b border-hairline px-4 py-3.5 last:border-b-0">
      <div className="flex min-h-6 items-center justify-between gap-2">
        <h3 className="eyebrow">{title}</h3>
        {aside}
      </div>
      {children}
    </section>
  )
}

/** What is inside: the head of a text, the pages of a PDF, an archive's entries, a folder's. */
function Contents({
  entry,
  preview,
  current,
  canWrite,
  onOpen,
  onNavigate,
}: PreviewProps & { preview: FilePreview }) {
  switch (preview.kind) {
    case "audio":
      return (
        <Section title="Listen">
          <audio src={rawUrl(entry.path, preview.modified)} controls className="w-full" />
        </Section>
      )
    case "pdf":
      return (
        <Section title="Pages">
          <PdfPreview path={entry.path} modified={preview.modified} />
        </Section>
      )
    case "text":
      return (
        <Section
          title={
            preview.truncated
              ? `First ${plural(preview.lines ?? 0, "line")}`
              : plural(preview.lines ?? 0, "line")
          }
          aside={
            <Button size="xs" variant="ghost" onClick={() => onOpen(entry.path)}>
              {preview.editable && canWrite ? "Open in editor" : "Open"}
              <ArrowUpRight className="size-3" />
            </Button>
          }
        >
          <Well className="max-h-72 p-0 whitespace-pre">
            <TextHead text={preview.text ?? ""} />
          </Well>
        </Section>
      )
    case "archive":
      return (
        <Section title="Inside the archive">
          <ArchiveListing preview={preview} />
        </Section>
      )
    case "dir":
      return (
        <DirectoryContents
          entry={entry}
          preview={preview}
          current={current}
          onNavigate={onNavigate}
        />
      )
    case "binary":
      return (
        <Section title="Contents">
          <p className="text-hint text-muted-foreground">
            Binary data — download it to open it on your computer, and compare its checksum with the
            one you were given rather than trusting the size.
          </p>
        </Section>
      )
    default:
      return null
  }
}

/** A folder's own row says "—" for size. This is the button that answers it. */
function DirectoryContents({
  entry,
  preview,
  current,
  onNavigate,
}: {
  entry: FileEntry
  preview: FilePreview
  current: boolean
  onNavigate: (path: string) => void
}) {
  const [usage, setUsage] = useState<FileUsage>()
  const [busy, setBusy] = useState(false)

  const measure = useCallback(async () => {
    setBusy(true)
    try {
      setUsage(await get<FileUsage>("/files/usage", { path: entry.path }))
    } catch (err) {
      notify.error("Could not measure this folder", err)
    } finally {
      setBusy(false)
    }
  }, [entry.path])

  const largest = usage?.largest ?? []
  const top = Math.max(1, ...largest.map((item) => item.bytes))

  return (
    <Section
      title={current ? "In this folder" : "Inside"}
      aside={
        !usage && (
          <Button size="xs" variant="ghost" onClick={measure} pending={busy}>
            <Calculator className="size-3" />
            Measure size
          </Button>
        )
      }
    >
      <div className="grid grid-cols-2 gap-2">
        <Count icon={FolderClosed} value={preview.dirCount ?? 0} label="folder" />
        <Count icon={File} value={preview.fileCount ?? 0} label="file" />
      </div>
      {usage && (
        <div className="space-y-2">
          <p className="text-xs">
            <b className="numeric text-body">{bytes(usage.bytes)}</b>{" "}
            <span className="text-muted-foreground">
              in {plural(usage.files, "file")} and {plural(usage.dirs, "folder")}
            </span>
          </p>
          {usage.truncated && (
            <p className="text-hint text-warning">
              The walk stopped at its budget, so this is a floor rather than the total.
            </p>
          )}
          <BarList
            className="-mx-2"
            items={largest.map((item) => ({
              key: item.path,
              label: item.name,
              mark: (
                <FileIcon
                  entry={{ name: item.name, isDir: item.isDir, isSymlink: false, path: item.path }}
                  className="size-3.5"
                />
              ),
              value: bytes(item.bytes),
              share: item.bytes / top,
              title: item.isDir ? `Open ${item.name}` : item.name,
              onClick: item.isDir ? () => onNavigate(item.path) : undefined,
            }))}
          />
        </div>
      )}
    </Section>
  )
}

function Count({ icon: Glyph, value, label }: { icon: Icon; value: number; label: string }) {
  return (
    <div className="flex items-center gap-2.5 rounded-md bg-background px-2.5 py-2">
      <Glyph aria-hidden className="size-4 text-muted-foreground" />
      <span className="min-w-0">
        <span className="numeric block text-body leading-tight font-semibold">
          {value.toLocaleString()}
        </span>
        <span className="block text-hint text-muted-foreground">
          {value === 1 ? label : label + "s"}
        </span>
      </span>
    </div>
  )
}

/** The facts, each beside a glyph for what kind of fact it is. */
function Details({ entry, preview }: { entry: FileEntry; preview: FilePreview }) {
  return (
    <Section title="Details">
      <dl className="space-y-2.5">
        <Fact icon={Clock} label="Modified">
          {relativeTime(preview.modified)}
          <span className="block text-hint text-muted-foreground">
            {timestamp(preview.modified)}
          </span>
        </Fact>
        {preview.kind !== "dir" && (
          <Fact icon={Box} label="Size">
            {bytes(preview.size)}
            {preview.size >= 1024 && (
              <span className="block text-hint text-muted-foreground">
                {preview.size.toLocaleString()} bytes
              </span>
            )}
          </Fact>
        )}
        {preview.kind === "text" && preview.lines !== undefined && !preview.truncated && (
          <Fact icon={ListOrdered} label="Lines">
            {preview.lines.toLocaleString()}
          </Fact>
        )}
        {preview.language && preview.language !== "plaintext" && (
          <Fact icon={CodeBracket} label="Language">
            <span className="capitalize">{preview.language}</span>
          </Fact>
        )}
        <Fact icon={Users} label="Owner">
          {preview.owner ?? entry.owner}
          <span className="block text-hint text-muted-foreground">
            group {preview.group ?? entry.group}
          </span>
        </Fact>
        {preview.isSymlink && (
          <Fact icon={Linked} label="Links to">
            <span className="font-mono text-xs break-all">{preview.symlinkTarget}</span>
            {preview.linkBroken && <span className="ml-1 text-destructive">broken</span>}
          </Fact>
        )}
      </dl>
    </Section>
  )
}

function Fact({
  icon: Glyph,
  label,
  children,
}: {
  icon: Icon
  label: string
  children: React.ReactNode
}) {
  return (
    <div className="grid grid-cols-[1rem_5.5rem_minmax(0,1fr)] items-start gap-x-2 text-body">
      <Glyph aria-hidden className="mt-0.5 size-3.5 text-muted-foreground" />
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="numeric min-w-0">{children}</dd>
    </div>
  )
}

/**
 * Who can do what with it: the mode as the grid it encodes, and the same in
 * one sentence for the reader who does not think in octal.
 */
function AccessSection({ preview, isDir }: { preview: FilePreview; isDir: boolean }) {
  const rows = accessRows(preview.modeOctal, preview.owner, preview.group)
  const columns: { key: keyof Access; label: string }[] = [
    { key: "read", label: isDir ? "List" : "Read" },
    { key: "write", label: isDir ? "Change" : "Write" },
    { key: "run", label: isDir ? "Enter" : "Run" },
  ]
  return (
    <Section
      title="Access"
      aside={<span className="font-mono text-hint text-muted-foreground">{preview.modeOctal}</span>}
    >
      <p className="text-body">{accessSummary(rows, isDir)}</p>
      <table className="w-full text-hint">
        <thead>
          <tr className="text-muted-foreground">
            <th className="pb-1 text-left font-normal">
              <span className="sr-only">Who</span>
            </th>
            {columns.map((column) => (
              <th key={column.key} className="w-14 pb-1 text-center font-normal">
                {column.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.who} className="border-t border-hairline">
              <th scope="row" className="py-1.5 text-left font-normal">
                <span className="text-foreground">{row.who}</span>
                {row.name && <span className="ml-1.5 text-muted-foreground">{row.name}</span>}
              </th>
              {columns.map((column) => (
                <td key={column.key} className="py-1.5 text-center">
                  {row.access[column.key] ? (
                    <Check aria-label="Yes" className="mx-auto size-3.5 text-foreground" />
                  ) : (
                    <Minus aria-label="No" className="mx-auto size-3.5 text-muted-foreground/50" />
                  )}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </Section>
  )
}

/** Where it lives, as the folders above it — each one a click away — and its path to copy. */
function Location({ entry, current, onNavigate }: PreviewProps) {
  const dir = current ? entry.path : parentOf(entry.path)
  const parts = dir.split("/").filter(Boolean)
  const crumbs = [
    { label: "/", path: "/" },
    ...parts.map((part, i) => ({ label: part, path: "/" + parts.slice(0, i + 1).join("/") })),
  ]
  return (
    <Section
      title={current ? "Path" : "Location"}
      aside={
        <IconAction
          label="Copy path"
          className="size-6"
          onClick={() => void copyText(entry.path, "Path copied")}
        >
          <Copy />
        </IconAction>
      }
    >
      <nav aria-label="Location" className="flex flex-wrap items-center gap-0.5 text-body">
        {crumbs.map((crumb, i) => (
          <span key={crumb.path} className="flex min-w-0 items-center gap-0.5">
            {i > 1 && <ChevronRight aria-hidden className="size-3 text-muted-foreground/60" />}
            <button
              type="button"
              disabled={current && crumb.path === dir}
              onClick={() => onNavigate(crumb.path)}
              className={cn(
                "max-w-40 truncate rounded-sm px-1 py-0.5 font-mono text-xs focus-ring transition-colors hover:bg-accent disabled:pointer-events-none",
                crumb.path === dir ? "text-foreground" : "text-muted-foreground",
              )}
            >
              {crumb.label}
            </button>
          </span>
        ))}
        {!current && (
          <span className="flex min-w-0 items-center gap-0.5">
            {parts.length > 0 && (
              <ChevronRight aria-hidden className="size-3 text-muted-foreground/60" />
            )}
            <span className="truncate px-1 font-mono text-xs font-medium">
              {baseOf(entry.path)}
            </span>
          </span>
        )}
      </nav>
    </Section>
  )
}
