/**
 * How the three columns of the table editor share the frame they are in.
 *
 * The frame is what is left of the window once the dashboard's own sidebar
 * has taken its part, so the window is the wrong thing to ask: at 1280 wide
 * the editor has about a thousand pixels, and a rail and a row panel drawn
 * because "the window is a desktop" left the rows a strip of 135 with their
 * controls laid over each other. The choice is made from the frame's measured
 * width, by one rule: the rows keep a floor, and a side column that would
 * take them under it gives way.
 *
 * The rail gives way last — it is how another table is reached — so it is a
 * column while the rows keep the smaller floor beside it, and lies over them
 * otherwise. The row panel needs the rows wide enough to be read beside it:
 * it is a column next to the rail when all three fit; when only two fit the
 * rail steps aside while a row is open; and when not even that fits it lies
 * over the table, as it does on a phone.
 */

export const RAIL = { min: 200, max: 480, fallback: 256 }
export const INSPECTOR = { min: 280, max: 640, fallback: 360 }

/** The least the rows are left with beside the rail alone. */
export const ROWS_BESIDE_RAIL = 480
/** The least the rows are left with beside the row panel: their strip still one line, three columns to read. */
export const ROWS_BESIDE_PANEL = 560

export interface Panes {
  /** The rail is a column of the frame, or lies over the table. */
  rail: "beside" | "over"
  /**
   * `beside`: a column, with the rail if it is shown. `alone`: a column the
   * rail steps aside for. `over`: laid over the table.
   */
  inspector: "beside" | "alone" | "over"
  /** The row panel's width as a column: what was asked for, less what the rows need. */
  inspectorWidth: number
  /** The widest each column can be dragged to here. */
  railMax: number
  inspectorMax: number
}

const clamp = (value: number, min: number, max: number) => Math.min(Math.max(value, min), max)

/**
 * `frame` is the editor's measured width; `rail` and `inspector` the widths
 * the reader last gave the two side columns; `pinned` whether the reader
 * keeps the rail shown.
 */
export function arrange(frame: number, rail: number, inspector: number, pinned: boolean): Panes {
  const railWidth = clamp(rail, RAIL.min, RAIL.max)
  const wanted = clamp(inspector, INSPECTOR.min, INSPECTOR.max)
  const railMode = frame - railWidth >= ROWS_BESIDE_RAIL ? "beside" : "over"
  const taken = railMode === "beside" && pinned ? railWidth : 0
  const beside = frame - taken - ROWS_BESIDE_PANEL
  const alone = frame - ROWS_BESIDE_PANEL
  const mode = beside >= INSPECTOR.min ? "beside" : alone >= INSPECTOR.min ? "alone" : "over"
  const room = mode === "beside" ? beside : alone
  return {
    rail: railMode,
    inspector: mode,
    inspectorWidth: mode === "over" ? wanted : Math.min(wanted, room),
    // The rail is not dragged past where the row panel beside it would have to go.
    railMax:
      mode === "beside"
        ? clamp(frame - ROWS_BESIDE_PANEL - INSPECTOR.min, RAIL.min, RAIL.max)
        : clamp(frame - ROWS_BESIDE_RAIL, RAIL.min, RAIL.max),
    inspectorMax: mode === "over" ? INSPECTOR.max : clamp(room, INSPECTOR.min, INSPECTOR.max),
  }
}
