"use client"

import { ErDiagram } from "@/components/database/diagram/er-diagram"
import { SectionFrame } from "@/components/database/kit"

/**
 * The schema as a diagram, for a SQL engine: the canvas in the page's frame.
 * Which schema it draws, what a table on it leads to and who may keep an
 * arrangement are all read from the database the page belongs to.
 */
export function Diagram() {
  return (
    <SectionFrame section="diagram">
      <ErDiagram />
    </SectionFrame>
  )
}
