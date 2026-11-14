"use client"

import { MoreHorizontal } from "@/components/icons"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { VerbList, type Verb } from "@/components/files/file-actions"

const SUBMENU: React.ComponentProps<typeof VerbList>["submenu"] = {
  Sub: DropdownMenuSub,
  SubTrigger: DropdownMenuSubTrigger,
  SubContent: DropdownMenuSubContent,
}

/**
 * What a strip has no room to draw, behind one button.
 *
 * A phone's strip holds four controls; the workbench has four times that. The
 * rest are not dropped and not wrapped onto a second and a third row: they are
 * the same verbs the listing's right-click menu is made of, as words, one to a
 * line, drawn by the same renderer so a verb reads the same in both.
 */
export function MoreMenu({
  groups,
  label = "More",
  className,
}: {
  groups: Verb[][]
  label?: string
  className?: string
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="icon-sm" variant="outline" aria-label={label} className={className}>
          <MoreHorizontal className="size-3.5" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <VerbList
          groups={groups}
          Item={DropdownMenuItem}
          Separator={DropdownMenuSeparator}
          submenu={SUBMENU}
        />
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
