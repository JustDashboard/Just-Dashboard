"use client"

import { Fragment, useRef, useState } from "react"
import Link from "next/link"
import {
  ArrowMove,
  Check,
  Clipboard,
  Copy,
  CornerUpRight,
  Download,
  Eye,
  FileZip,
  FolderClosed,
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
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
  ContextMenuTrigger,
} from "@/components/ui/context-menu"
import { copyText } from "@/lib/clipboard"
import { isArchive, mediaKind } from "@/components/files/media"
import {
  FOLDER_COLOURS,
  FOLDER_COLOUR_NAMES,
  FolderSwatch,
  type FolderColour,
} from "@/components/files/file-icon"

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
  /** The colour a folder is drawn in, and the way to change it. */
  colour: FolderColour
  onColour: (colour: FolderColour) => void
}

/**
 * One verb, as data.
 *
 * Entry and background actions share one renderer, keeping each capability
 * and verb in the data rather than separate lists of menu controls.
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
  /** A drawing in place of the glyph — a colour's own swatch. */
  mark?: React.ReactNode
  /** The current one of a set of choices. */
  checked?: boolean
  /** Verbs one level down, behind this one. */
  submenu?: Verb[]
}

/**
 * A folder's colour, as a verb with the nine labels under it. The swatch in
 * front of each is the folder as it would be drawn, so the choice is made by
 * looking rather than by reading nine colour names.
 */
export function colourVerb(current: FolderColour, onPick: (colour: FolderColour) => void): Verb {
  return {
    id: "colour",
    label: "Colour",
    icon: FolderClosed,
    mark: <FolderSwatch colour={current} className="size-3.5" />,
    submenu: FOLDER_COLOURS.map((colour) => ({
      id: `colour-${colour}`,
      label: FOLDER_COLOUR_NAMES[colour],
      icon: FolderClosed,
      mark: <FolderSwatch colour={colour} className="size-3.5" />,
      checked: colour === current,
      onSelect: () => onPick(colour),
    })),
  }
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
      go.push(colourVerb(actions.colour, actions.onColour))
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

/** The three primitives a verb with a submenu needs, from whichever menu is open. */
type SubmenuComponents = {
  Sub: React.ComponentType<{ children?: React.ReactNode }>
  SubTrigger: React.ComponentType<{ className?: string; children?: React.ReactNode }>
  SubContent: React.ComponentType<{ className?: string; children?: React.ReactNode }>
}

const CONTEXT_SUBMENU: SubmenuComponents = {
  Sub: ContextMenuSub,
  SubTrigger: ContextMenuSubTrigger,
  SubContent: ContextMenuSubContent,
}

/** Draws verb groups into whichever menu primitive is open. */
export function VerbList({
  groups,
  Item,
  Separator,
  submenu,
}: {
  groups: Verb[][]
  Item: MenuItemComponent
  Separator: React.ComponentType<{ className?: string }>
  submenu: SubmenuComponents
}) {
  const visible = groups.filter((group) => group.length > 0)
  return (
    <>
      {visible.map((group, i) => (
        <Fragment key={i}>
          {i > 0 && <Separator />}
          {group.map((verb) => (
            <VerbItem key={verb.id} verb={verb} Item={Item} submenu={submenu} />
          ))}
        </Fragment>
      ))}
    </>
  )
}

function VerbItem({
  verb,
  Item,
  submenu,
}: {
  verb: Verb
  Item: MenuItemComponent
  submenu: SubmenuComponents
}) {
  const mark = verb.mark ?? <verb.icon className="size-3.5" />
  if (verb.submenu) {
    const { Sub, SubTrigger, SubContent } = submenu
    return (
      <Sub>
        <SubTrigger className="text-body">
          {mark}
          <span className="min-w-0 flex-1 truncate">{verb.label}</span>
        </SubTrigger>
        <SubContent className="w-44">
          {verb.submenu.map((inner) => (
            <VerbItem key={inner.id} verb={inner} Item={Item} submenu={submenu} />
          ))}
        </SubContent>
      </Sub>
    )
  }
  const body = (
    <>
      {mark}
      <span className="min-w-0 flex-1 truncate">{verb.label}</span>
      {verb.checked && <Check className="size-3.5" />}
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
  const returnFocus = useRef<HTMLElement | null>(null)
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
          onPointerDownCapture={(event) => {
            if (event.pointerType !== "touch" && event.pointerType !== "pen") return
            // Radix's long-press opens directly from pointerdown, without
            // dispatching the contextmenu event used by a mouse.
            const el = (event.target as HTMLElement).closest<HTMLElement>("[data-entry-path]")
            const entry = el?.dataset.entryPath ? resolve(el.dataset.entryPath) : undefined
            setTarget(entry ?? null)
            onTarget?.(entry ?? null)
          }}
          onKeyDownCapture={(event) => {
            if (event.key !== "ContextMenu" && !(event.shiftKey && event.key === "F10")) return
            const el = (event.target as HTMLElement).closest<HTMLElement>("[data-entry-path]")
            if (!el) return
            event.preventDefault()
            returnFocus.current = event.target as HTMLElement
            const rect = el.getBoundingClientRect()
            el.dispatchEvent(
              new MouseEvent("contextmenu", {
                bubbles: true,
                cancelable: true,
                clientX: rect.left + Math.min(rect.width / 2, 80),
                clientY: rect.top + rect.height / 2,
              }),
            )
          }}
          onContextMenuCapture={(event) => {
            if (event.nativeEvent.isTrusted) returnFocus.current = null
            const el = (event.target as HTMLElement).closest<HTMLElement>("[data-entry-path]")
            const entry = el?.dataset.entryPath ? resolve(el.dataset.entryPath) : undefined
            setTarget(entry ?? null)
            onTarget?.(entry ?? null)
          }}
        >
          {children}
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent
        className="w-56"
        onCloseAutoFocus={(event) => {
          const target = returnFocus.current
          returnFocus.current = null
          if (!target?.isConnected) return
          event.preventDefault()
          target.focus({ preventScroll: true })
        }}
      >
        <VerbList
          groups={groups}
          Item={ContextMenuItem}
          Separator={ContextMenuSeparator}
          submenu={CONTEXT_SUBMENU}
        />
      </ContextMenuContent>
    </ContextMenu>
  )
}
