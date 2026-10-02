import { fitWidth, type OrderedColumn } from "./layout"
import { valueAt, type GridModel } from "./model"
import { formatCell } from "./values"

/**
 * The grid's dealings with the document: which cell an event landed on, a drag
 * that outlives the element it began on, and how wide a column's text is.
 */

export interface Hit {
  row: number
  /** -1 for the gutter. */
  col: number
  gutter: boolean
}

/** Which cell an event landed on, read off the attributes the rows carry. */
export function hitTest(target: EventTarget | null): Hit | null {
  if (!(target instanceof Element)) return null
  const rowElement = target.closest("[data-row]")
  if (!rowElement) return null
  const row = Number(rowElement.getAttribute("data-row"))
  const cell = target.closest("[data-col]")
  if (cell) return { row, col: Number(cell.getAttribute("data-col")), gutter: false }
  return target.closest("[data-gutter]") ? { row, col: -1, gutter: true } : null
}

/**
 * Follows the pointer until it is released, wherever that happens.
 *
 * Listeners on the window rather than pointer capture: a captured pointer
 * retargets the click and the double-click that follow to the capturing
 * element, and the grid reads which cell was double-clicked from the event's
 * target. The price is a release outside the window going unheard, so a move
 * that arrives with no button down ends the drag itself.
 *
 * Returns the function that ends it early — for an unmount in mid-drag.
 */
export function trackPointer(onMove: (event: PointerEvent) => void, onEnd: () => void): () => void {
  const move = (event: PointerEvent) => {
    if (event.buttons === 0) end()
    else onMove(event)
  }
  const end = () => {
    window.removeEventListener("pointermove", move)
    window.removeEventListener("pointerup", end)
    window.removeEventListener("pointercancel", end)
    onEnd()
  }
  window.addEventListener("pointermove", move)
  window.addEventListener("pointerup", end)
  window.addEventListener("pointercancel", end)
  return end
}

function fontOf(root: Element, selector: string, fallback: string): string {
  const element = root.querySelector(selector)
  if (!element) return fallback
  // The `font` shorthand reads back empty in some browsers; its parts do not.
  const style = getComputedStyle(element)
  return `${style.fontWeight} ${style.fontSize} ${style.fontFamily}`
}

/**
 * The width that fits a column to what it shows.
 *
 * Measured on a canvas in the cells' own font, over the rows on screen and a
 * little past them: fitting to rows nobody has scrolled to would cost a pass
 * over the whole page for a width they may never see.
 */
export function measureColumn(
  root: Element,
  model: GridModel,
  entry: OrderedColumn,
  firstRow: number,
): number | null {
  const canvas = document.createElement("canvas").getContext("2d")
  if (!canvas) return null
  const { column } = entry
  canvas.font = fontOf(root, "[data-hcol]", "500 11px sans-serif")
  const marks = (column.primaryKey ? 18 : 0) + (column.foreignKey ? 18 : 0)
  const header =
    canvas.measureText(column.name).width + Math.min(column.typeName.length, 18) * 6.2 + marks + 64
  canvas.font = fontOf(root, "[data-col]", "12px monospace")
  const texts: string[] = []
  const end = Math.min(firstRow + 200, model.ids.length)
  for (let row = firstRow; row < end; row++) {
    const shown = formatCell(valueAt(model, row, entry), column)
    texts.push(shown.lead ? `${shown.lead}  ${shown.text}` : shown.text)
  }
  return fitWidth(texts, (text) => canvas.measureText(text).width, {
    padding: column.foreignKey ? 40 : 20,
    header,
  })
}
