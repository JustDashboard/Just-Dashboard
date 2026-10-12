"use client"

import { useLayoutEffect, useRef, useState } from "react"
import { useStore } from "@xyflow/react"
import {
  Copy,
  LockClosed,
  LockOpen,
  MoreHorizontal,
  Pencil,
  Trash,
  ArrowLeftRight,
} from "@/components/icons"
import {
  NOTE_COLOR_NAMES,
  NOTE_COLORS,
  type LinkArrow,
  type LinkPath,
  type LinkStyle,
  type NoteAlign,
  type NoteColor,
  type NoteFont,
  type NoteSize,
  type NoteStyle,
  type Rect,
  type StickyLink,
  type StickyNote,
} from "@/lib/stickyboard"
import { useApple } from "@/lib/platform"
import { cn } from "@/lib/utils"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  AlignCenter,
  AlignLeft,
  ArrowBoth,
  ArrowEnd,
  ArrowNone,
  BringToFront,
  PathCurved,
  PathElbow,
  PathStraight,
  SendToBack,
} from "@/components/stickyboard/glyphs"

/**
 * The bar that floats over what is selected, holding how it looks. It is the
 * one control on the board that stands above the notes, so it takes a
 * popover's ground and shadow (§2) — it is drawn in screen space, not on the
 * board, and does not scale with the zoom.
 */
export function FloatingBar({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      role="toolbar"
      className={cn(
        "nodrag nopan nowheel flex max-w-[calc(100vw-2rem)] items-center gap-1 overflow-x-auto rounded-lg border bg-popover p-1 text-popover-foreground shadow-md",
        className,
      )}
      onPointerDown={(event) => event.stopPropagation()}
      onDoubleClick={(event) => event.stopPropagation()}
      {...props}
    />
  )
}

/** The width of the tool rail down the board's left edge, which the bar keeps clear of. */
const RAIL = 64
const GAP = 12

/**
 * Holds the bar over what is selected, in screen space: above it, or below it
 * when there is no room above, and always inside the board — slid along the
 * edge rather than cut off by it, and clear of the tool rail. React Flow's own
 * toolbars place themselves over a node and nothing else, so a selection near
 * an edge had its colours drawn off the board.
 */
export function Floating({ rect, children }: { rect: Rect; children: React.ReactNode }) {
  const transform = useStore((state) => state.transform)
  const width = useStore((state) => state.width)
  const height = useStore((state) => state.height)
  const ref = useRef<HTMLDivElement>(null)
  const [size, setSize] = useState({ w: 0, h: 0 })

  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const measure = () => setSize({ w: el.offsetWidth, h: el.offsetHeight })
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  const [tx, ty, zoom] = transform
  const left = rect.x * zoom + tx
  const top = rect.y * zoom + ty
  const right = (rect.x + rect.w) * zoom + tx
  const bottom = (rect.y + rect.h) * zoom + ty
  const offscreen = right < 0 || bottom < 0 || left > width || top > height
  let y = top - GAP - size.h
  if (y < GAP) y = bottom + GAP
  if (y + size.h > height - GAP)
    y = Math.max(GAP, Math.min(top - GAP - size.h, height - GAP - size.h))
  const x = Math.max(RAIL, Math.min((left + right) / 2 - size.w / 2, width - size.w - GAP))

  return (
    <div
      ref={ref}
      className="absolute top-0 left-0 z-10"
      style={{
        transform: `translate(${Math.round(x)}px, ${Math.round(y)}px)`,
        visibility: size.w === 0 || offscreen ? "hidden" : undefined,
      }}
    >
      {children}
    </div>
  )
}

function Divider() {
  return <span aria-hidden className="mx-0.5 h-5 w-px shrink-0 bg-hairline" />
}

/** The value every item shares, or nothing when they differ. */
function shared<T, K extends keyof T>(items: readonly T[], key: K): T[K] | "" {
  const first = items[0]?.[key]
  return items.every((item) => item[key] === first) ? (first as T[K]) : ""
}

/**
 * A run of options as one control. The pressed one is §3's selection; with
 * a mixed selection none is pressed, and pressing one sets it on all.
 */
function Choice<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string
  value: T | ""
  options: {
    value: T
    label: string
    icon?: React.ComponentType<{ className?: string }>
    text?: string
  }[]
  onChange: (value: T) => void
}) {
  return (
    <ToggleGroup
      type="single"
      size="sm"
      aria-label={label}
      value={value}
      onValueChange={(next) => next && onChange(next as T)}
      className="shrink-0 gap-0.5"
    >
      {options.map((option) => (
        <Tooltip key={option.value}>
          <TooltipTrigger asChild>
            <ToggleGroupItem
              value={option.value}
              aria-label={option.label}
              className="h-7 min-w-7 rounded-md px-1.5 text-xs data-[state=off]:text-muted-foreground"
            >
              {option.icon ? <option.icon className="size-4" /> : option.text}
            </ToggleGroupItem>
          </TooltipTrigger>
          <TooltipContent>{option.label}</TooltipContent>
        </Tooltip>
      ))}
    </ToggleGroup>
  )
}

/** The nine colours, as swatches of the paper itself. Digits 1–9 pick them too. */
function Swatches({
  value,
  onChange,
}: {
  value: NoteColor | ""
  onChange: (color: NoteColor) => void
}) {
  return (
    <ToggleGroup
      type="single"
      aria-label="Colour"
      value={value}
      onValueChange={(next) => next && onChange(next as NoteColor)}
      className="shrink-0 gap-0.5"
    >
      {NOTE_COLORS.map((color, index) => (
        <Tooltip key={color}>
          <TooltipTrigger asChild>
            <ToggleGroupItem
              value={color}
              aria-label={NOTE_COLOR_NAMES[color]}
              className="size-7 min-w-7 rounded-md p-0 data-[state=on]:bg-accent"
            >
              <span
                data-note-color={color}
                className={cn(
                  "size-4 rounded-sm border border-(--note-edge) bg-(--note)",
                  value === color && "outline-2 outline-offset-1 outline-foreground",
                )}
              />
            </ToggleGroupItem>
          </TooltipTrigger>
          <TooltipContent>
            {NOTE_COLOR_NAMES[color]}{" "}
            <span className="ml-1 text-muted-foreground">{index + 1}</span>
          </TooltipContent>
        </Tooltip>
      ))}
    </ToggleGroup>
  )
}

export function NoteBar({
  notes,
  onChange,
  onEdit,
  onReorder,
  onDuplicate,
  onCopy,
  onDelete,
}: {
  notes: readonly StickyNote[]
  onChange: (patch: Partial<StickyNote>) => void
  /** Present when one note that can be written in is selected. */
  onEdit?: () => void
  onReorder: (to: "front" | "back") => void
  onDuplicate: () => void
  onCopy: () => void
  onDelete: () => void
}) {
  const stickies = notes.filter((n) => n.kind === "sticky")
  const written = notes.filter((n) => n.kind !== "section")
  const locked = notes.every((n) => n.locked)
  const mod = useApple() ? "⌘" : "Ctrl "
  return (
    <FloatingBar aria-label="Format">
      <Swatches value={shared(notes, "color")} onChange={(color) => onChange({ color })} />
      {stickies.length > 0 && (
        <>
          <Divider />
          <Choice<NoteStyle>
            label="Paper"
            value={shared(stickies, "style")}
            onChange={(style) => onChange({ style })}
            options={[
              { value: "paper", label: "Solid paper", text: "Paper" },
              { value: "tint", label: "Tinted on the board", text: "Tint" },
            ]}
          />
        </>
      )}
      <Divider />
      <Choice<NoteSize>
        label="Text size"
        value={shared(notes, "size")}
        onChange={(size) => onChange({ size })}
        options={[
          { value: "s", label: "Small", text: "S" },
          { value: "m", label: "Medium", text: "M" },
          { value: "l", label: "Large", text: "L" },
          { value: "xl", label: "Extra large", text: "XL" },
        ]}
      />
      {written.length > 0 && (
        <>
          <Divider />
          <Choice<NoteAlign>
            label="Alignment"
            value={shared(written, "align")}
            onChange={(align) => onChange({ align })}
            options={[
              { value: "left", label: "Align left", icon: AlignLeft },
              { value: "center", label: "Centre", icon: AlignCenter },
            ]}
          />
          <Choice<NoteFont>
            label="Typeface"
            value={shared(written, "font")}
            onChange={(font) => onChange({ font })}
            options={[
              { value: "sans", label: "Sans", text: "Aa" },
              { value: "mono", label: "Monospace", text: "{ }" },
            ]}
          />
        </>
      )}
      <Divider />
      {onEdit && (
        <IconAction label="Edit text (Enter)" className="size-7" onClick={onEdit}>
          <Pencil />
        </IconAction>
      )}
      <IconAction
        label={locked ? "Unlock" : "Lock in place"}
        className={cn("size-7", locked && "bg-accent text-foreground")}
        aria-pressed={locked}
        onClick={() => onChange({ locked: !locked })}
      >
        {locked ? <LockClosed /> : <LockOpen />}
      </IconAction>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="icon-sm"
            className="size-7"
            aria-label="More for the selection"
          >
            <MoreHorizontal />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-52">
          <DropdownMenuItem onSelect={() => onReorder("front")}>
            <BringToFront />
            Bring to front
            <DropdownMenuShortcut>]</DropdownMenuShortcut>
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => onReorder("back")}>
            <SendToBack />
            Send to back
            <DropdownMenuShortcut>[</DropdownMenuShortcut>
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={onDuplicate}>
            <Copy />
            Duplicate
            <DropdownMenuShortcut>{mod}D</DropdownMenuShortcut>
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={onCopy}>
            <Copy />
            Copy
            <DropdownMenuShortcut>{mod}C</DropdownMenuShortcut>
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem variant="destructive" onSelect={onDelete}>
            <Trash />
            Delete
            <DropdownMenuShortcut>Del</DropdownMenuShortcut>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </FloatingBar>
  )
}

export function LinkBar({
  links,
  onChange,
  onLabel,
  onReverse,
  onDelete,
}: {
  links: readonly StickyLink[]
  onChange: (patch: Partial<StickyLink>) => void
  /** Present when one link is selected. */
  onLabel?: () => void
  onReverse: () => void
  onDelete: () => void
}) {
  return (
    <FloatingBar aria-label="Link">
      <Swatches value={shared(links, "color")} onChange={(color) => onChange({ color })} />
      <Divider />
      <Choice<LinkStyle>
        label="Line"
        value={shared(links, "style")}
        onChange={(style) => onChange({ style })}
        options={[
          { value: "solid", label: "Solid line", text: "Solid" },
          { value: "dashed", label: "Dashed line", text: "Dashed" },
        ]}
      />
      <Divider />
      <Choice<LinkPath>
        label="Shape"
        value={shared(links, "path")}
        onChange={(path) => onChange({ path })}
        options={[
          { value: "curved", label: "Curved", icon: PathCurved },
          { value: "straight", label: "Straight", icon: PathStraight },
          { value: "elbow", label: "Elbow", icon: PathElbow },
        ]}
      />
      <Divider />
      <Choice<LinkArrow>
        label="Arrows"
        value={shared(links, "arrow")}
        onChange={(arrow) => onChange({ arrow })}
        options={[
          { value: "none", label: "No arrow", icon: ArrowNone },
          { value: "end", label: "Arrow at the end", icon: ArrowEnd },
          { value: "both", label: "Arrows at both ends", icon: ArrowBoth },
        ]}
      />
      <Divider />
      {onLabel && (
        <IconAction label="Edit label (Enter)" className="size-7" onClick={onLabel}>
          <Pencil />
        </IconAction>
      )}
      <IconAction label="Reverse direction" className="size-7" onClick={onReverse}>
        <ArrowLeftRight />
      </IconAction>
      <IconAction label="Delete link (Del)" className="size-7" onClick={onDelete}>
        <Trash />
      </IconAction>
    </FloatingBar>
  )
}
