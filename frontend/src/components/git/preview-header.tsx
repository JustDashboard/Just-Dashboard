"use client"

import { Cross } from "@/components/icons"
import { cn } from "@/lib/utils"
import { PaneHeader } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

export function PreviewHeader({
  title,
  subtitle,
  trailing,
  mono = true,
  onClose,
}: {
  title: string
  subtitle?: React.ReactNode
  trailing?: React.ReactNode
  mono?: boolean
  onClose: () => void
}) {
  return (
    <PaneHeader className="gap-2 px-3">
      <div className="min-w-0 flex-1">
        <p className={cn("truncate text-body font-medium", mono && "font-mono")} title={title}>
          {title}
        </p>
        {subtitle && <p className="truncate text-hint text-muted-foreground">{subtitle}</p>}
      </div>
      {trailing}
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            size="sm"
            variant="ghost"
            className="size-7 shrink-0 p-0 text-muted-foreground hover:text-foreground"
            aria-label="Close"
            onClick={onClose}
          >
            <Cross className="size-4" />
          </Button>
        </TooltipTrigger>
        <TooltipContent>Close the preview</TooltipContent>
      </Tooltip>
    </PaneHeader>
  )
}
