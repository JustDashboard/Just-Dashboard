"use client"

import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { Tabs as TabsPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"

/**
 * Whether the panels here are the one view a strip did not draw (`ViewTabs`
 * with `hideWhenSingle`). Such a panel has no tab to be labelled by: a tab
 * panel pointing at a trigger that is not there has no name, and announces a
 * tab list nobody can reach. So it is a plain box, neither a tab panel nor a
 * tab stop. What the view holds names itself: an account's view is a region
 * named for it, and a second region of that name round it was the same
 * landmark twice.
 */
const LoneView = React.createContext(false)

function Tabs({
  className,
  orientation = "horizontal",
  children,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Root>) {
  return (
    <TabsPrimitive.Root
      data-slot="tabs"
      data-orientation={orientation}
      orientation={orientation}
      className={cn("group/tabs flex gap-2 data-[orientation=horizontal]:flex-col", className)}
      {...props}
    >
      {/* A strip of its own inside a lone view's panel draws its tabs again. */}
      <LoneView.Provider value={false}>{children}</LoneView.Provider>
    </TabsPrimitive.Root>
  )
}

/**
 * Radix's tabs, drawn as Just Hub draws its two switchers (its
 * `.stitch/DESIGN.md` 4, Tabs and chips):
 *
 *   `default` — the segmented control inside a form: a control-filled track
 *               under the subtle line with 3px of padding, the chosen tab on
 *               the chosen fill with no outline. The same mark `Segments`
 *               slides.
 *   `line`    — quiet tabs: the muted ink, the chosen one in the foreground on
 *               a control face, as `ViewTabs` draws them. Never an underline.
 *
 * A page's views are `ViewTabs` (`components/tabs.tsx`), not this.
 */
const tabsListVariants = cva(
  // max-w-full + scroll: four tab labels do not fit on a phone, and a strip
  // that runs off the screen drags the whole page sideways with it. The
  // scrollbar is hidden because the cut-off tab is already the affordance.
  "group/tabs-list inline-flex w-fit max-w-full [scrollbar-width:none] items-center justify-start overflow-x-auto text-muted-foreground [-ms-overflow-style:none] group-data-[orientation=vertical]/tabs:h-fit group-data-[orientation=vertical]/tabs:flex-col [&::-webkit-scrollbar]:hidden",
  {
    variants: {
      variant: {
        // 36px, a form's control height: a 1px edge and 3px of padding round
        // a 28px segment.
        default:
          "gap-0.5 rounded-md border border-hairline bg-control p-0.75 group-data-[orientation=horizontal]/tabs:h-9",
        line: "gap-1 rounded-none border-b border-hairline bg-transparent py-1 group-data-[orientation=horizontal]/tabs:h-10",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  },
)

function TabsList({
  className,
  variant = "default",
  ...props
}: React.ComponentProps<typeof TabsPrimitive.List> & VariantProps<typeof tabsListVariants>) {
  return (
    <TabsPrimitive.List
      data-slot="tabs-list"
      data-variant={variant}
      className={cn(tabsListVariants({ variant }), className)}
      {...props}
    />
  )
}

// Forced colours (Windows High Contrast) repaint every fill as the page and
// every ink as the text's, so the chosen tab read as any other. There it takes
// the system's own selection, Highlight under HighlightText, kept from that
// repaint, as the page's tabs do (`components/tabs.tsx`). The fill is said per
// variant, to outweigh each variant's own chosen fill. The ring is inset,
// inside that face, so there it is HighlightText: a Highlight ring on a
// Highlight face would not be seen. Whole class names, for Tailwind.
const ACTIVE_FORCED =
  "group-data-[variant=default]/tabs-list:data-[state=active]:forced-colors:bg-[Highlight] group-data-[variant=line]/tabs-list:data-[state=active]:forced-colors:bg-[Highlight] data-[state=active]:forced-colors:text-[HighlightText] data-[state=active]:forced-colors:forced-color-adjust-none data-[state=active]:forced-colors:[--ring:HighlightText]"

function TabsTrigger({ className, ...props }: React.ComponentProps<typeof TabsPrimitive.Trigger>) {
  return (
    <TabsPrimitive.Trigger
      data-slot="tabs-trigger"
      // One mode: the resting ink is the muted one rather than a faded
      // foreground. The ring is inset in both: the list scrolls, and a
      // scroller clips a ring drawn outside it.
      className={cn(
        "relative inline-flex shrink-0 items-center justify-center gap-1.5 border border-transparent px-3 text-body font-medium whitespace-nowrap text-muted-foreground focus-ring-inset transition-colors group-data-[orientation=vertical]/tabs:w-full group-data-[orientation=vertical]/tabs:justify-start hover:text-foreground disabled:pointer-events-none disabled:opacity-50 data-[state=active]:text-foreground [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        // The segment: the chosen fill with the lit top edge every raised
        // control has (`shadow-edge`), and no outline.
        "group-data-[variant=default]/tabs-list:h-full group-data-[variant=default]/tabs-list:flex-1 group-data-[variant=default]/tabs-list:rounded-sm group-data-[variant=default]/tabs-list:data-[state=active]:bg-chosen group-data-[variant=default]/tabs-list:data-[state=active]:shadow-edge",
        // The quiet tab: the chosen one is a filled face, never an underline.
        "group-data-[variant=line]/tabs-list:h-8 group-data-[variant=line]/tabs-list:rounded-md group-data-[variant=line]/tabs-list:hover:bg-accent group-data-[variant=line]/tabs-list:data-[state=active]:bg-control",
        ACTIVE_FORCED,
        className,
      )}
      {...props}
    />
  )
}

function TabsContent({ className, ...props }: React.ComponentProps<typeof TabsPrimitive.Content>) {
  const lone = React.useContext(LoneView)
  return (
    <TabsPrimitive.Content
      data-slot="tabs-content"
      className={cn("flex-1 focus-ring", className)}
      {...props}
      // Radix spreads these over its own role, label and tab stop.
      {...(lone && { role: undefined, "aria-labelledby": undefined, tabIndex: undefined })}
    />
  )
}

export { Tabs, TabsList, TabsTrigger, TabsContent, LoneView, tabsListVariants }
