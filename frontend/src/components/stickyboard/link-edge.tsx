"use client"

import { createContext, memo, useContext } from "react"
import {
  BaseEdge,
  EdgeLabelRenderer,
  useInternalNode,
  type Edge,
  type EdgeProps,
  type InternalNode,
} from "@xyflow/react"
import {
  arrowhead,
  linkGeometry,
  type NoteColor,
  type Rect,
  type StickyLink,
} from "@/lib/stickyboard"
import { cn } from "@/lib/utils"

/** What a link's label asks of the board, beside the note actions. */
export type LinkActions = {
  editable: boolean
  selectLink: (id: string) => void
  startEditingLink: (id: string) => void
  editLabel: (id: string, label: string) => void
  stopEditing: () => void
}

export const LinkContext = createContext<LinkActions | null>(null)

export type LinkData = { link: StickyLink; editing: boolean }
export type LinkEdgeType = Edge<LinkData, "link">

/** A link's ink. Graphite is the board's own quiet line rather than a dark one. */
export function linkStroke(color: NoteColor): string {
  return color === "graphite" ? "var(--muted-foreground)" : `var(--note-${color})`
}

function rectOfNode(node: InternalNode): Rect {
  return {
    x: node.internals.positionAbsolute.x,
    y: node.internals.positionAbsolute.y,
    w: node.measured.width ?? node.width ?? 0,
    h: node.measured.height ?? node.height ?? 0,
  }
}

/**
 * A link between two notes, drawn from edge to edge of the notes themselves
 * rather than between fixed anchors, so it meets whichever side of each note
 * faces the other however they are moved (`linkGeometry`). Its arrowheads are
 * part of the drawing, in the link's own colour, rather than shared SVG
 * markers that would need one definition per colour.
 */
export const LinkEdge = memo(function LinkEdge({
  id,
  source,
  target,
  data,
  selected,
}: EdgeProps<LinkEdgeType>) {
  const actions = useContext(LinkContext)
  const from = useInternalNode(source)
  const to = useInternalNode(target)
  if (!from || !to || !data || !actions) return null
  const { link, editing } = data
  const g = linkGeometry(rectOfNode(from), rectOfNode(to), link.path)
  const stroke = linkStroke(link.color)

  return (
    <>
      {selected && (
        <path
          d={g.d}
          fill="none"
          stroke="var(--rule-brand)"
          strokeWidth={9}
          strokeLinecap="round"
        />
      )}
      <BaseEdge
        id={id}
        path={g.d}
        interactionWidth={20}
        style={{
          stroke,
          strokeWidth: selected ? 2.75 : 2,
          strokeDasharray: link.style === "dashed" ? "8 6" : undefined,
          strokeLinecap: "round",
        }}
      />
      {link.arrow !== "none" && <path d={arrowhead(g.end, 13)} style={{ fill: stroke }} />}
      {link.arrow === "both" && <path d={arrowhead(g.start, 13)} style={{ fill: stroke }} />}
      {(link.label || editing) && (
        <EdgeLabelRenderer>
          <div
            className="nodrag nopan pointer-events-auto absolute"
            style={{ transform: `translate(-50%, -50%) translate(${g.label.x}px, ${g.label.y}px)` }}
          >
            {editing ? (
              <input
                autoFocus
                aria-label="Link label"
                defaultValue={link.label}
                maxLength={200}
                placeholder="Label"
                className="w-40 rounded-sm border border-brand bg-background px-1.5 py-0.5 text-center text-foreground outline-none"
                style={{ fontSize: 13 }}
                onFocus={(event) => event.currentTarget.select()}
                onChange={(event) => actions.editLabel(id, event.target.value)}
                onBlur={actions.stopEditing}
                onKeyDown={(event) => {
                  event.stopPropagation()
                  if (event.key === "Enter" || event.key === "Escape") {
                    event.preventDefault()
                    actions.stopEditing()
                  }
                }}
              />
            ) : (
              <button
                type="button"
                className={cn(
                  "max-w-56 truncate rounded-sm border bg-background px-1.5 py-0.5 leading-tight focus-ring",
                  selected ? "border-brand" : "border-hairline",
                  link.color === "graphite" ? "text-foreground" : "text-(--link-ink)",
                )}
                style={{ fontSize: 13, ["--link-ink" as string]: stroke }}
                onClick={() => actions.selectLink(id)}
                onDoubleClick={(event) => {
                  event.stopPropagation()
                  if (actions.editable) actions.startEditingLink(id)
                }}
              >
                {link.label}
              </button>
            )}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  )
})
