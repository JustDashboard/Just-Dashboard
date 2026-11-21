"use client"

import Link from "next/link"
import { Trash } from "@/components/icons"
import { cn } from "@/lib/utils"
import { DropdownMenuItem } from "@/components/ui/dropdown-menu"
import { TRASH_ROUTE } from "@/components/files/trash"

/**
 * The Trash as a place: the last row of the Files sidebar (and so of the
 * phone's Places pane, which is the same list), and the last item of the
 * places menu the strip offers when the sidebar is put away. It is a page
 * rather than a folder, so it links there instead of browsing a path.
 */
export function TrashPlace({ active }: { active?: boolean }) {
  return (
    <div className="mt-auto border-t border-hairline pt-2">
      <Link
        href={TRASH_ROUTE}
        aria-current={active ? "page" : undefined}
        className={cn(
          "flex h-8 items-center gap-2.5 rounded-md px-2 text-body focus-ring-inset transition-colors hover:bg-row-hover",
          active && "bg-accent font-medium text-accent-foreground hover:bg-accent",
        )}
      >
        <Trash aria-hidden className="size-4.5 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate">Trash</span>
      </Link>
    </div>
  )
}

export function TrashMenuItem() {
  return (
    <DropdownMenuItem asChild className="gap-2.5">
      <Link href={TRASH_ROUTE}>
        <Trash aria-hidden className="size-4 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate text-body">Trash</span>
      </Link>
    </DropdownMenuItem>
  )
}
