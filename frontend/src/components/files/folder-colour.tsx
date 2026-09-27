"use client"

import { useEffect, useRef } from "react"
import { usePathname } from "next/navigation"
import { Check } from "@/components/icons"
import { get } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { FilePlaces } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import {
  FOLDER_COLOURS,
  FOLDER_COLOUR_NAMES,
  FolderColourProvider,
  FolderSwatch,
  type FolderColour,
} from "@/components/files/file-icon"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

/**
 * A folder's colour, picked by looking: each choice is the folder as it would
 * be drawn. The chosen one is `bg-accent`, which is what a selection is
 * everywhere (§3), rather than a ring in the colour it already is.
 */
export function FolderColourSwatches({
  value,
  onPick,
  className,
}: {
  value: FolderColour
  onPick: (colour: FolderColour) => void
  className?: string
}) {
  return (
    <div
      role="radiogroup"
      aria-label="Folder colour"
      className={cn("flex flex-wrap items-center gap-0.5", className)}
    >
      {FOLDER_COLOURS.map((colour) => (
        <Tooltip key={colour}>
          <TooltipTrigger asChild>
            <button
              type="button"
              role="radio"
              aria-checked={value === colour}
              aria-label={FOLDER_COLOUR_NAMES[colour]}
              onClick={() => onPick(colour)}
              className={cn(
                "rounded-md p-1 focus-ring transition-colors hover:bg-row-hover",
                value === colour && "bg-accent hover:bg-accent",
              )}
            >
              <FolderSwatch colour={colour} className="size-5" />
            </button>
          </TooltipTrigger>
          <TooltipContent>{FOLDER_COLOUR_NAMES[colour]}</TooltipContent>
        </Tooltip>
      ))}
    </div>
  )
}

/** The same choice behind a trigger: the folder you are in, drawn in the strip. */
export function FolderColourMenu({
  value,
  onPick,
  children,
  label = "Colour this folder",
}: {
  value: FolderColour
  onPick: (colour: FolderColour) => void
  children: React.ReactNode
  label?: string
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>{children}</DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-44">
        <DropdownMenuLabel className="eyebrow py-1">{label}</DropdownMenuLabel>
        {FOLDER_COLOURS.map((colour) => (
          <DropdownMenuItem key={colour} onSelect={() => onPick(colour)}>
            <FolderSwatch colour={colour} className="size-3.5" />
            <span className="min-w-0 flex-1 truncate">{FOLDER_COLOUR_NAMES[colour]}</span>
            {value === colour && <Check className="size-3.5" />}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/**
 * The saved labels for every page in the shell, not just Files: the terminal's
 * and a working copy's tree, a volume's or a stack's browser and a deployment's
 * storage draw the same folders, and one that is red in Files and blue beside
 * the shell it was opened from is a label nobody can trust.
 *
 * Files is where a colour changes, and it draws its own edits under a provider
 * of its own before the round trip lands. So this reads again on each
 * navigation rather than on a timer: leaving Files is the moment the rest of
 * the dashboard needs the answer, and nothing else moves it.
 */
export function SavedFolderColours({ children }: { children: React.ReactNode }) {
  const pathname = usePathname()
  const places = usePoll((signal) => get<FilePlaces>("/files/places", undefined, signal), 0)
  const { refresh } = places
  const readOn = useRef(pathname)
  useEffect(() => {
    if (readOn.current === pathname) return
    readOn.current = pathname
    refresh()
  }, [pathname, refresh])
  return (
    <FolderColourProvider
      colours={places.data?.colours ?? {}}
      defaultColour={places.data?.defaultColour}
    >
      {children}
    </FolderColourProvider>
  )
}
