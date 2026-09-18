"use client"

import { Fragment, useState } from "react"
import Link from "next/link"
import {
  ArrowMove,
  Clipboard,
  Copy,
  CornerUpRight,
  Download,
  Eye,
  FileZip,
  FolderOpen,
  GitHubMark,
  Image as ImageIcon,
  Pencil,
  Shield,
  Star,
  StarFill,
  TerminalWindow,
  Trash,
  type Icon,
} from "@/components/icons"
import { downloadUrl } from "@/lib/api"
import type { FileEntry } from "@/lib/types"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu"
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu"
import { copyText } from "@/lib/clipboard"
import { isArchive, mediaKind } from "@/components/files/media"

export { isArchive, isImage } from "@/components/files/media"

export type RowCaps = { write: boolean; destruct: boolean; admin: boolean }

export type FileActions = {
  /** What a double-click does: a folder opens, text edits, media views. */
  onOpen: () => void
  /** The full-screen viewer. Space, in the listing. */
  onView: () => void
  /** The code editor, whatever the file looks like. */
  onEdit: () => void
  onRename: () => void
  onDuplicate: () => void
  onCopy: () => void
  onCut: () => void
  onExtract: () => void
  onPermissions: () => void
  onDelete: () => void
  onEditImage: () => void
  onToggleStar: () => void
  starred: boolean
}

/**
 * One verb, as data.
 *
 * The listing offers its verbs in three places — the row's overflow button,
 * the tile's, and the right-click menu on either — and three lists is how a
 * verb ends up in one of them only. So the verbs are declared once, here, and
 * each surface only decides which primitive to draw them into.
 */
export type Verb = {
  id: string
  label: string
  icon: Icon
  onSelect?: () => void
  /** A link rather than a handler: a download, or another page. */
  href?: string
  download?: boolean
  /** The link is a route in this app and goes through the router. */
  internal?: boolean
  danger?: boolean
  /** The key that does the same thing from the keyboard. */
  shortcut?: string
}

export function archiveHref(base: string, paths: string[], format: "zip" | "tar.gz") {
  return (
    downloadUrl("/files/archive", { base, format }) +
    paths.map((p) => `&path=${encodeURIComponent(p)}`).join("")
  )
}

/**
 * Every operation the backend supports, reachable from one entry.
 *
 * An action that only makes sense sometimes appears only then: extract on an
 * archive, the image editor on an image, permissions for an admin, a shell
 * and the repository view on a folder.
 */
export function fileVerbs(entry: FileEntry, caps: RowCaps, actions: FileActions): Verb[][] {
  const kind = entry.isDir ? null : mediaKind(entry.name)
  const groups: Verb[][] = []

  const open: Verb[] = []
  if (entry.isDir) {
    open.push({
      id: "open",
      label: "Open",
      icon: FolderOpen,
      onSelect: actions.onOpen,
      shortcut: "↵",
    })
  } else {
    open.push({
      id: "view",
      label: kind ? "View" : "Quick look",
      icon: Eye,
      onSelect: actions.onView,
      shortcut: "Space",
    })
    open.push({ id: "edit", label: "Open in editor", icon: Pencil, onSelect: actions.onEdit })
    if (kind === "image" && caps.write) {
      open.push({
        id: "image",
        label: "Edit image…",
        icon: ImageIcon,
        onSelect: actions.onEditImage,
      })
    }
  }
  groups.push(open)

  const take: Verb[] = []
  if (entry.isDir) {
    take.push(
      {
        id: "zip",
        label: "Download as .zip",
        icon: FileZip,
        href: archiveHref(entry.path, [entry.path], "zip"),
        download: true,
      },
      {
        id: "tgz",
        label: "Download as .tar.gz",
        icon: FileZip,
        href: archiveHref(entry.path, [entry.path], "tar.gz"),
        download: true,
      },
    )
  } else {
    take.push({
      id: "download",
      label: "Download",
      icon: Download,
      href: downloadUrl("/files/download", { path: entry.path }),
      download: true,
    })
  }
  take.push({
    id: "path",
    label: "Copy path",
    icon: Clipboard,
    onSelect: () => void copyText(entry.path, "Path copied"),
  })
  groups.push(take)

  if (entry.isDir) {
    // The two places an operator goes next from a folder. Both are deep
    // links the other pages already accept.
    const go: Verb[] = [
      {
        id: "shell",
        label: "Open a shell here",
        icon: TerminalWindow,
        href: `/terminal?cwd=${encodeURIComponent(entry.path)}`,
        internal: true,
      },
      {
        id: "git",
        label: "Open in Git",
        icon: GitHubMark,
        href: `/git?repo=${encodeURIComponent(entry.path)}`,
        internal: true,
      },
    ]
    if (caps.write) {
      go.push({
        id: "star",
        label: actions.starred ? "Unstar folder" : "Star folder",
        icon: actions.starred ? StarFill : Star,
        onSelect: actions.onToggleStar,
      })
    }
    groups.push(go)
  }

  if (caps.write) {
    const change: Verb[] = [
      { id: "rename", label: "Rename", icon: Pencil, onSelect: actions.onRename, shortcut: "F2" },
      { id: "duplicate", label: "Duplicate", icon: Copy, onSelect: actions.onDuplicate },
      { id: "copy", label: "Copy", icon: Copy, onSelect: actions.onCopy, shortcut: "⌃C" },
      { id: "cut", label: "Cut", icon: ArrowMove, onSelect: actions.onCut, shortcut: "⌃X" },
    ]
    if (!entry.isDir && isArchive(entry.name)) {
      change.push({
        id: "extract",
        label: "Extract here",
        icon: CornerUpRight,
        onSelect: actions.onExtract,
      })
    }
    groups.push(change)
  }

  if (caps.admin) {
    groups.push([
      { id: "permissions", label: "Permissions…", icon: Shield, onSelect: actions.onPermissions },
    ])
  }

  if (caps.destruct) {
    groups.push([
      {
        id: "delete",
        label: "Delete",
        icon: Trash,
        onSelect: actions.onDelete,
        danger: true,
        shortcut: "Del",
      },
    ])
  }
  return groups
}

/** The props a menu item primitive has to accept to draw a verb. */
type MenuItemComponent = React.ComponentType<{
  onSelect?: (event: Event) => void
  asChild?: boolean
  variant?: "default" | "destructive"
  className?: string
  children?: React.ReactNode
}>

/** Draws verb groups into whichever menu primitive is open. */
export function VerbList({
  groups,
  Item,
  Separator,
}: {
  groups: Verb[][]
  Item: MenuItemComponent
  Separator: React.ComponentType<{ className?: string }>
}) {
  const visible = groups.filter((group) => group.length > 0)
  return (
    <>
      {visible.map((group, i) => (
        <Fragment key={i}>
          {i > 0 && <Separator />}
          {group.map((verb) => (
            <VerbItem key={verb.id} verb={verb} Item={Item} />
          ))}
        </Fragment>
      ))}
    </>
  )
}

function VerbItem({ verb, Item }: { verb: Verb; Item: MenuItemComponent }) {
  const body = (
    <>
      <verb.icon className="size-3.5" />
      <span className="min-w-0 flex-1 truncate">{verb.label}</span>
      {verb.shortcut && (
        <span className="ml-3 shrink-0 text-micro tracking-wide text-muted-foreground">
          {verb.shortcut}
        </span>
      )}
    </>
  )
  if (verb.href && verb.internal) {
    return (
      <Item asChild>
        <Link href={verb.href}>{body}</Link>
      </Item>
    )
  }
  if (verb.href) {
    return (
      <Item asChild>
        <a href={verb.href} download={verb.download}>
          {body}
        </a>
      </Item>
    )
  }
  return (
    <Item variant={verb.danger ? "destructive" : "default"} onSelect={verb.onSelect}>
      {body}
    </Item>
  )
}

/**
 * The overflow menu behind a row's or a tile's "…" button. The trigger is
 * passed in as `children` so each view can style its own button and the menu
 * itself stays identical.
 */
export function FileActionsMenu({
  entry,
  caps,
  actions,
  children,
}: {
  entry: FileEntry
  caps: RowCaps
  actions: FileActions
  children: React.ReactNode
}) {
  return (
    <DropdownMenu>
      {children}
      <DropdownMenuContent align="end" className="w-56">
        <VerbList
          groups={fileVerbs(entry, caps, actions)}
          Item={DropdownMenuItem}
          Separator={DropdownMenuSeparator}
        />
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/**
 * The right-click menu over the whole listing.
 *
 * One menu root for the listing rather than one per row: the row under the
 * pointer is read off the element the click landed on (`data-entry-path`),
 * so five hundred rows cost five hundred attributes rather than five hundred
 * menu instances. A right-click on the space between rows gets the folder's
 * own verbs — new folder, upload, paste — which is what that gesture means in
 * every file manager.
 */
export function ListingContextMenu({
  caps,
  resolve,
  actionsFor,
  background,
  onTarget,
  className,
  children,
}: {
  caps: RowCaps
  resolve: (path: string) => FileEntry | undefined
  actionsFor: (entry: FileEntry) => FileActions
  /** The verbs for the folder itself, when nothing in particular was clicked. */
  background: Verb[][]
  /** The entry the menu opened on, so the page can make it the active row. */
  onTarget?: (entry: FileEntry | null) => void
  className?: string
  children: React.ReactNode
}) {
  const [target, setTarget] = useState<FileEntry | null>(null)
  const groups = target ? fileVerbs(target, caps, actionsFor(target)) : background
  return (
    <ContextMenu
      onOpenChange={(open) => {
        if (!open) setTarget(null)
      }}
    >
      <ContextMenuTrigger asChild>
        <div
          className={className}
          onContextMenuCapture={(event) => {
            const el = (event.target as HTMLElement).closest<HTMLElement>("[data-entry-path]")
            const entry = el?.dataset.entryPath ? resolve(el.dataset.entryPath) : undefined
            setTarget(entry ?? null)
            onTarget?.(entry ?? null)
          }}
        >
          {children}
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent className="w-56">
        <VerbList groups={groups} Item={ContextMenuItem} Separator={ContextMenuSeparator} />
      </ContextMenuContent>
    </ContextMenu>
  )
}
