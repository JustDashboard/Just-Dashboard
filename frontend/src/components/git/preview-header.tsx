"use client"

import { ArrowLeft, Cross } from "@/components/icons"
import { cn } from "@/lib/utils"
import { PaneHeader } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

export function PreviewHeader({
  title,
  subtitle,
  leading,
  trailing,
  mono = true,
  wrap,
  onClose,
}: {
  title: string
  subtitle?: React.ReactNode
  /** A mark before the title: a pull request's state, in its colour. */
  leading?: React.ReactNode
  trailing?: React.ReactNode
  mono?: boolean
  /** A sentence rather than a name: two lines before it is cut. */
  wrap?: boolean
  onClose: () => void
}) {
  return (
    <PaneHeader className={cn("gap-2 px-3", wrap && "items-start")}>
      {/* On a phone the preview is a page of its own, and leaving it is going
          back to the list rather than closing a column. */}
      <Button
        size="icon-sm"
        variant="ghost"
        className="-ml-1 shrink-0 text-muted-foreground hover:text-foreground lg:hidden"
        aria-label="Back to the list"
        onClick={onClose}
      >
        <ArrowLeft className="size-4" />
      </Button>
      {leading}
      <div className="min-w-0 flex-1">
        <p
          className={cn(
            "text-body font-medium",
            wrap ? "line-clamp-2 leading-snug" : "truncate",
            mono && "font-mono",
          )}
          title={title}
        >
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
            className="size-7 shrink-0 p-0 text-muted-foreground hover:text-foreground max-lg:hidden"
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
