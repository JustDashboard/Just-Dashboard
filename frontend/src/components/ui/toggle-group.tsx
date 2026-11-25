"use client"

import * as React from "react"
import { type VariantProps } from "class-variance-authority"
import { ToggleGroup as ToggleGroupPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"
import { useField } from "@/components/form"
import { hitAreaClasses } from "@/components/ui/button"
import { toggleVariants } from "@/components/ui/toggle"

const ToggleGroupContext = React.createContext<
  VariantProps<typeof toggleVariants> & {
    spacing?: number
    segmented?: boolean
  }
>({
  size: "default",
  variant: "default",
  spacing: 0,
  segmented: false,
})

/**
 * A segment's height inside the track, so the whole control is a button's
 * height: a 1px edge and 3px of padding on each side, then the segment. 36/44
 * at `default` (a form's line), 32/40 at `sm`, 40/48 at `lg`. The compact one
 * keeps the hit area the compact toggle had.
 */
const SEGMENT_SIZE = {
  default: "h-7 pointer-coarse:h-9",
  sm: `h-6 pointer-coarse:h-8 [&_svg:not([class*='size-'])]:size-3.5 ${hitAreaClasses}`,
  lg: "h-8 pointer-coarse:h-10",
} as const

/**
 * A row of toggles. Joined (`variant="outline"`, no spacing) it is the
 * segmented control Just Hub draws (its `.stitch/DESIGN.md` 4, Segmented
 * control): a control-filled track with 3px of padding, and the chosen segment
 * on a raised face under the strong line that slides to the segment chosen
 * next. `Segments` is the one single-choice caller.
 *
 * The face is one element measured onto the chosen segment rather than a fill
 * on each, which is what lets it slide. Until it has been measured, and
 * wherever the group allows several choices at once, each chosen segment draws
 * its own face instead, so the control never shows nothing chosen. Reduced
 * motion makes the slide a jump (the root rule in `globals.css`).
 *
 * Inside a `Field` the group is named by the field's label — a group is not a
 * control a `for` can point at — and described by its hint and error, unless
 * the caller named it.
 */
function ToggleGroup({
  className,
  variant,
  size,
  spacing = 0,
  children,
  ref,
  ...rest
}: React.ComponentProps<typeof ToggleGroupPrimitive.Root> &
  VariantProps<typeof toggleVariants> & {
    spacing?: number
  }) {
  const field = useField()
  const named = Boolean(rest["aria-label"] || rest["aria-labelledby"])
  const describedBy =
    [rest["aria-describedby"], field?.describedBy].filter(Boolean).join(" ") || undefined
  const props = field
    ? {
        ...rest,
        "aria-labelledby": named ? rest["aria-labelledby"] : field.labelId,
        "aria-describedby": describedBy,
        "aria-invalid": rest["aria-invalid"] ?? (field.invalid || undefined),
      }
    : rest

  const segmented = variant === "outline" && spacing === 0
  const single = rest.type === "single"
  const root = React.useRef<HTMLDivElement | null>(null)
  const [face, setFace] = React.useState<{ x: number; y: number; w: number; h: number } | null>(
    null,
  )
  const [sliding, setSliding] = React.useState(false)

  React.useLayoutEffect(() => {
    const el = root.current
    if (!segmented || !single || !el) return
    const measure = () => {
      const on = el.querySelector<HTMLElement>(
        ":scope > [data-slot=toggle-group-item][data-state=on]",
      )
      setFace(
        on ? { x: on.offsetLeft, y: on.offsetTop, w: on.offsetWidth, h: on.offsetHeight } : null,
      )
    }
    measure()
    // Placed first without a transition, then allowed to slide.
    const frame = requestAnimationFrame(() => setSliding(true))
    const resize = new ResizeObserver(measure)
    resize.observe(el)
    const mutate = new MutationObserver(measure)
    mutate.observe(el, { subtree: true, attributes: true, attributeFilter: ["data-state"] })
    return () => {
      cancelAnimationFrame(frame)
      resize.disconnect()
      mutate.disconnect()
    }
  }, [segmented, single])

  const setRef = (node: HTMLDivElement | null) => {
    root.current = node
    if (typeof ref === "function") ref(node)
    else if (ref) ref.current = node
  }

  return (
    <ToggleGroupPrimitive.Root
      ref={setRef}
      data-slot="toggle-group"
      data-variant={variant}
      data-size={size}
      data-spacing={spacing}
      data-face={face ? "" : undefined}
      style={{ "--gap": spacing } as React.CSSProperties}
      className={cn(
        "group/toggle-group flex w-fit items-center rounded-md",
        segmented
          ? "relative isolate max-w-full items-stretch gap-0.5 border border-border bg-control p-0.75 aria-invalid:border-destructive"
          : "gap-[--spacing(var(--gap))]",
        className,
      )}
      {...props}
    >
      {segmented && face && (
        <span
          aria-hidden
          data-slot="toggle-group-face"
          // First in the track and placed absolutely, so the segments, each
          // placed relatively after it, are drawn over it without a z-index.
          className={cn(
            "pointer-events-none absolute top-0 left-0 rounded-sm border border-border-strong bg-popover shadow-edge",
            sliding && "transition-[transform,width,height] duration-base ease-arrive",
          )}
          style={{
            width: face.w,
            height: face.h,
            transform: `translate(${face.x}px, ${face.y}px)`,
          }}
        />
      )}
      <ToggleGroupContext.Provider value={{ variant, size, spacing, segmented }}>
        {children}
      </ToggleGroupContext.Provider>
    </ToggleGroupPrimitive.Root>
  )
}

function ToggleGroupItem({
  className,
  children,
  variant,
  size,
  ...props
}: React.ComponentProps<typeof ToggleGroupPrimitive.Item> & VariantProps<typeof toggleVariants>) {
  const context = React.useContext(ToggleGroupContext)
  const itemSize = context.size || size || "default"

  if (context.segmented) {
    return (
      <ToggleGroupPrimitive.Item
        data-slot="toggle-group-item"
        data-variant="outline"
        data-size={itemSize}
        className={cn(
          // The ring is inset: the track is only 3px deep around a segment.
          "relative inline-flex min-w-0 shrink-0 items-center justify-center gap-1.5 rounded-sm border border-transparent px-3 text-body font-medium whitespace-nowrap text-muted-foreground focus-ring-inset transition-colors",
          "hover:text-foreground disabled:pointer-events-none disabled:opacity-50 data-[state=on]:text-foreground",
          // Each chosen segment's own face, until the sliding one is measured.
          "data-[state=on]:border-border-strong data-[state=on]:bg-popover data-[state=on]:shadow-edge group-data-[face]/toggle-group:data-[state=on]:border-transparent group-data-[face]/toggle-group:data-[state=on]:bg-transparent group-data-[face]/toggle-group:data-[state=on]:shadow-none",
          "[&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
          SEGMENT_SIZE[itemSize],
          className,
        )}
        {...props}
      >
        {children}
      </ToggleGroupPrimitive.Item>
    )
  }

  return (
    <ToggleGroupPrimitive.Item
      data-slot="toggle-group-item"
      data-variant={context.variant || variant}
      data-size={itemSize}
      data-spacing={context.spacing}
      className={cn(
        toggleVariants({
          variant: context.variant || variant,
          size: itemSize,
        }),
        "w-auto min-w-0 shrink-0 px-3 focus:z-raised focus-visible:z-raised",
        "data-[spacing=0]:rounded-none data-[spacing=0]:first:rounded-l-md data-[spacing=0]:last:rounded-r-md",
        className,
      )}
      {...props}
    >
      {children}
    </ToggleGroupPrimitive.Item>
  )
}

export { ToggleGroup, ToggleGroupItem }
