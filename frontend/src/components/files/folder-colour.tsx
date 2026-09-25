"use client"

import { Check } from "@/components/icons"
import { cn } from "@/lib/utils"
import {
  FOLDER_COLOURS,
  FOLDER_COLOUR_NAMES,
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
