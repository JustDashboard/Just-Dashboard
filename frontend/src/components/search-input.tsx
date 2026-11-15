"use client"

import { useCallback, useRef } from "react"
import { cn } from "@/lib/utils"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Cross, MagnifyingGlass } from "@/components/icons"
import { useDeferredSearch } from "@/hooks/use-deferred-search"

/*
 * The box's own sizes, shared with its skeleton so the bone is the box: a
 * hand copy of the proxy's bone stood 8px short of the phone field it held
 * the place of, and the page moved when the field arrived.
 *
 * On a phone the page's filter is a field like any other, 44px at 16px, and
 * takes the first line of a wrapping toolbar to itself. From `sm` under a
 * mouse it is the 32px box it always was. The dense one is a pane's own
 * chrome at 28px, and under a finger it is the full box too: 28px with 12px
 * text was too small to press, and iOS zoomed the page when it took focus.
 */
const BOX = "w-full min-w-0 sm:w-72"
const BOX_PAGE = "max-sm:basis-full"
const FIELD_PAGE = "h-11 sm:h-8 pointer-coarse:h-11"
const FIELD_DENSE = "h-7 pointer-coarse:h-11"

/**
 * The filter box, which appeared in six pages as the same three elements
 * assembled slightly differently each time — a different width, a different
 * icon offset, sometimes no icon at all.
 *
 * A box that runs a scan per value takes `onApply`: its `value` is then the
 * value in force, the box keeps what is being typed, and applies it after a
 * pause (`debounceMs`), at once on Enter, and empty on Escape or the clear
 * button (`useDeferredSearch`). Without `onApply` it is a plain controlled
 * field, and `clearable` empties it through its own `onChange`.
 */
export function SearchInput({
  dense,
  trailing,
  onApply,
  debounceMs = 400,
  clearable,
  stopEscape,
  clearLabel = "Clear the search",
  className,
  containerClassName,
  ref,
  ...props
}: React.ComponentProps<typeof Input> & {
  /** The filter box inside a pane's own chrome, where 32px is too tall. */
  dense?: boolean
  /** Controls pinned inside the box — a regex toggle. */
  trailing?: React.ReactNode
  /** Applies the typed value after a pause; `value` is then the value in force. */
  onApply?: (value: string) => void
  debounceMs?: number
  /** A clear button inside the box while it holds text. */
  clearable?: boolean
  /**
   * Escape empties the box and goes no further, so the sheet or dialog
   * around it stays open. Escape on an empty box still closes it.
   */
  stopEscape?: boolean
  clearLabel?: string
  containerClassName?: string
}) {
  if (onApply) {
    return (
      <DeferredSearchInput
        {...props}
        ref={ref}
        dense={dense}
        trailing={trailing}
        onApply={onApply}
        debounceMs={debounceMs}
        clearable={clearable ?? true}
        stopEscape={stopEscape}
        clearLabel={clearLabel}
        className={className}
        containerClassName={containerClassName}
      />
    )
  }
  return (
    <SearchBox
      {...props}
      ref={ref}
      dense={dense}
      trailing={trailing}
      clearable={clearable}
      stopEscape={stopEscape}
      clearLabel={clearLabel}
      className={className}
      containerClassName={containerClassName}
    />
  )
}

type BoxProps = Omit<React.ComponentProps<typeof Input>, "onApply"> & {
  dense?: boolean
  trailing?: React.ReactNode
  clearable?: boolean
  stopEscape?: boolean
  clearLabel: string
  containerClassName?: string
  /** Empties the box; left out, the box empties itself through `onChange`. */
  onClear?: () => void
}

function DeferredSearchInput({
  value,
  onApply,
  debounceMs,
  onChange,
  onKeyDown,
  ...props
}: BoxProps & { onApply: (value: string) => void; debounceMs: number }) {
  const search = useDeferredSearch(String(value ?? ""), onApply, debounceMs)
  return (
    <SearchBox
      {...props}
      value={search.draft}
      onChange={(event) => {
        onChange?.(event)
        search.setDraft(event.target.value)
      }}
      onKeyDown={(event) => {
        onKeyDown?.(event)
        if (!event.defaultPrevented && event.key === "Enter") search.apply()
      }}
      onClear={search.clear}
    />
  )
}

function SearchBox({
  dense,
  trailing,
  clearable,
  stopEscape,
  clearLabel,
  onClear,
  className,
  containerClassName,
  ref,
  onKeyDown,
  ...props
}: BoxProps) {
  const own = useRef<HTMLInputElement | null>(null)
  // One callback for the life of the caller's ref, so a callback ref is not
  // called with null and the node again on every render.
  const attach = useCallback(
    (node: HTMLInputElement | null) => {
      own.current = node
      if (typeof ref === "function") return ref(node)
      if (ref) ref.current = node
    },
    [ref],
  )
  const filled = String(props.value ?? "") !== ""
  const clear = () => {
    if (onClear) onClear()
    else if (own.current) emptyInput(own.current)
    own.current?.focus()
  }
  const showClear = clearable && filled && !props.disabled && !props.readOnly
  return (
    <div className={cn("relative flex items-center", BOX, !dense && BOX_PAGE, containerClassName)}>
      <MagnifyingGlass
        className={cn(
          "pointer-events-none absolute top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground",
          dense ? "left-2 pointer-coarse:left-2.5" : "left-2.5",
        )}
      />
      <Input
        ref={attach}
        // A page's own filter, not one inside a pane's chrome, is what `/` focuses.
        data-page-search={dense ? undefined : ""}
        className={cn(
          dense ? `${FIELD_DENSE} pl-7 text-xs pointer-coarse:pl-8` : `${FIELD_PAGE} pl-8`,
          showClear && "pr-8",
          className,
        )}
        onKeyDown={(event) => {
          onKeyDown?.(event)
          if (event.defaultPrevented || event.key !== "Escape") return
          if ((clearable || onClear) && filled) {
            if (stopEscape) event.stopPropagation()
            clear()
          }
        }}
        {...props}
      />
      {(showClear || trailing) && (
        <div className="absolute right-1 flex items-center gap-0.5">
          {showClear && (
            <button
              type="button"
              aria-label={clearLabel}
              onClick={clear}
              className="hit-area flex size-6 items-center justify-center rounded-sm text-muted-foreground focus-ring hover:text-foreground"
            >
              <Cross className="size-3" />
            </button>
          )}
          {trailing}
        </div>
      )}
    </div>
  )
}

/**
 * Empties a controlled field the way typing would, so the caller's own
 * `onChange` hears it: React listens for the native input event, and a value
 * set through the element's prototype setter is one it has not seen yet.
 */
function emptyInput(input: HTMLInputElement) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set
  setter?.call(input, "")
  input.dispatchEvent(new Event("input", { bubbles: true }))
}

/** The search box's place while its page loads, at the box's own size. */
export function SearchInputSkeleton({ dense, className }: { dense?: boolean; className?: string }) {
  return (
    <Skeleton
      aria-hidden
      className={cn("rounded-md", BOX, dense ? FIELD_DENSE : [BOX_PAGE, FIELD_PAGE], className)}
    />
  )
}
