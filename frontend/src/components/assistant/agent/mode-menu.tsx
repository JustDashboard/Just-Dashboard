"use client"

import {
  Check,
  ChevronDown,
  LockClosed,
  LockOpen,
  ShieldCheck,
  type Icon,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { MODE_DETAIL, MODE_LABEL, type AssistantMode } from "@/components/assistant/settings"

const MODE_ICON: Record<AssistantMode, Icon> = {
  ask: LockClosed,
  changes: ShieldCheck,
  full: LockOpen,
}

/**
 * How much it may do before it asks, chosen beside the press that asks. Full access
 * is amber on the toolbar for as long as it is on: it is the one setting here a
 * person should not lose sight of.
 */
export function ModeMenu({
  mode,
  onChange,
}: {
  mode: AssistantMode
  onChange: (mode: AssistantMode) => void
}) {
  const Mark = MODE_ICON[mode]
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          data-slot="mode-menu"
          data-mode={mode}
          aria-label={`Permissions: ${MODE_LABEL[mode]}`}
          className={cn(
            "inline-flex h-7 shrink-0 items-center gap-1.5 rounded-md px-2 text-hint focus-ring transition-colors hover:bg-row-hover",
            mode === "full" ? "text-warning" : "text-muted-foreground hover:text-foreground",
          )}
        >
          <Mark aria-hidden className="size-3.5" />
          <span className="whitespace-nowrap max-sm:hidden">{MODE_LABEL[mode]}</span>
          <ChevronDown aria-hidden className="size-3" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-80">
        {(Object.keys(MODE_LABEL) as AssistantMode[]).map((option) => {
          const OptionMark = MODE_ICON[option]
          return (
            <DropdownMenuItem
              key={option}
              onSelect={() => onChange(option)}
              className="items-start gap-2.5 py-2"
            >
              <OptionMark
                aria-hidden
                className={cn(
                  "mt-0.5 size-4 shrink-0",
                  option === "full" ? "text-warning" : "text-muted-foreground",
                )}
              />
              <span className="min-w-0 flex-1">
                <span className="block text-body font-medium">{MODE_LABEL[option]}</span>
                <span className="block text-hint leading-relaxed text-muted-foreground">
                  {MODE_DETAIL[option]}
                </span>
              </span>
              {option === mode && <Check aria-hidden className="mt-0.5 size-3.5 shrink-0" />}
            </DropdownMenuItem>
          )
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
