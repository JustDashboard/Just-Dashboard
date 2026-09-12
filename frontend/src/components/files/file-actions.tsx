"use client"

import Link from "next/link"
import {
  ArrowMove,
  Clipboard,
  Copy,
  CornerUpRight,
  Download,
  FileZip,
  FolderOpen,
  GitBranch,
  Image as ImageIcon,
  Pencil,
  Shield,
  Star,
  TerminalWindow,
  Trash,
} from "@/components/icons"
import { downloadUrl } from "@/lib/api"
import type { FileEntry } from "@/lib/types"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu"
import { copyText } from "@/lib/clipboard"

/** Archives the backend can extract in place. */
const ARCHIVE_RE = /\.(zip|tar|tar\.gz|tgz|tar\.bz2)$/i
export const isArchive = (name: string) => ARCHIVE_RE.test(name)

const IMAGE_RE = /\.(png|jpe?g|gif|webp|avif|bmp|ico|svg)$/i
export const isImage = (name: string) => IMAGE_RE.test(name)

export type RowCaps = { write: boolean; destruct: boolean; admin: boolean }

export type FileActions = {
  onOpen: () => void
  onRename: () => void
  onDuplicate: () => void
  onCopy: () => void
  onCut: () => void
  onExtract: () => void
  onPermissions: () => void
  onDelete: () => void
  onEditImage: () => void
  onBookmark: () => void
}

/**
 * Every operation the backend supports, reachable from one entry.
 *
 * It lives on its own rather than inside the table row because the grid needs
 * exactly the same menu, and a second copy of it is how a feature ends up
 * available in one view and not the other — which reads as the view being
 * broken rather than as an oversight. The trigger is passed in as `children`
 * so each view can style its own button and the menu itself stays identical.
 *
 * An action that only makes sense sometimes appears only then: extract on an
 * archive, the image editor on an image, permissions for an admin, a shell and
 * the repository view on a folder.
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
  const copyPath = () => void copyText(entry.path, "Path copied")

  const archive = !entry.isDir && isArchive(entry.name)
  const image = !entry.isDir && isImage(entry.name)

  return (
    <DropdownMenu>
      {children}
      <DropdownMenuContent align="end" className="w-52">
        <DropdownMenuItem onSelect={actions.onOpen}>
          {entry.isDir ? <FolderOpen className="size-3.5" /> : <Pencil className="size-3.5" />}
          {entry.isDir ? "Open" : "Open / edit"}
        </DropdownMenuItem>
        {image && caps.write && (
          <DropdownMenuItem onSelect={actions.onEditImage}>
            <ImageIcon className="size-3.5" />
            Edit image…
          </DropdownMenuItem>
        )}
        {!entry.isDir && (
          <DropdownMenuItem asChild>
            <a href={downloadUrl("/files/download", { path: entry.path })} download>
              <Download className="size-3.5" />
              Download
            </a>
          </DropdownMenuItem>
        )}
        {entry.isDir && (
          <DropdownMenuItem asChild>
            <a
              href={
                downloadUrl("/files/archive", { base: entry.path, format: "tar.gz" }) +
                `&path=${encodeURIComponent(entry.path)}`
              }
              download
            >
              <FileZip className="size-3.5" />
              Download as .tar.gz
            </a>
          </DropdownMenuItem>
        )}
        <DropdownMenuItem onSelect={copyPath}>
          <Clipboard className="size-3.5" />
          Copy path
        </DropdownMenuItem>

        {entry.isDir && (
          <>
            <DropdownMenuSeparator />
            {/* The two places an operator goes next from a folder. Both are
                deep links the other pages already accept. */}
            <DropdownMenuItem asChild>
              <Link href={`/terminal?cwd=${encodeURIComponent(entry.path)}`}>
                <TerminalWindow className="size-3.5" />
                Open a shell here
              </Link>
            </DropdownMenuItem>
            <DropdownMenuItem asChild>
              <Link href={`/git?repo=${encodeURIComponent(entry.path)}`}>
                <GitBranch className="size-3.5" />
                Open in Git
              </Link>
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={actions.onBookmark}>
              <Star className="size-3.5" />
              Add to places
            </DropdownMenuItem>
          </>
        )}

        {caps.write && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={actions.onRename}>
              <Pencil className="size-3.5" />
              Rename
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={actions.onDuplicate}>
              <Copy className="size-3.5" />
              Duplicate
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={actions.onCopy}>
              <Copy className="size-3.5" />
              Copy
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={actions.onCut}>
              <ArrowMove className="size-3.5" />
              Cut
            </DropdownMenuItem>
            {archive && (
              <DropdownMenuItem onSelect={actions.onExtract}>
                <CornerUpRight className="size-3.5" />
                Extract here
              </DropdownMenuItem>
            )}
          </>
        )}

        {caps.admin && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={actions.onPermissions}>
              <Shield className="size-3.5" />
              Permissions…
            </DropdownMenuItem>
          </>
        )}

        {caps.destruct && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              className="text-destructive focus:text-destructive"
              onSelect={actions.onDelete}
            >
              <Trash className="size-3.5" />
              Delete
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
