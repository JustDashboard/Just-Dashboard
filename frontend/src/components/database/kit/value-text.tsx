import { bytes } from "@/lib/format"
import { cn } from "@/lib/utils"
import { KIND_HUE } from "@/components/database/grid/legend"
import {
  binarySize,
  valueKind,
  valuePreview,
  type ValueKind,
} from "@/components/database/kit/values"

/**
 * How each kind of value is drawn. The hues are the grid's legend
 * (`KIND_HUE`), so a number, a moment or a document is the same colour as a
 * field here and as a cell there: text and numbers in the foreground, since
 * they are most of what there is, and none of green, amber or red, which on
 * these pages mean a row added, a cell changed and a row going.
 *
 * NULL and the empty string are not kinds of value and have no hue: they are
 * words about one, in the quiet voice.
 */
export const VALUE_KIND_CLASS: Record<ValueKind, string> = {
  null: "text-muted-foreground/60",
  empty: "text-muted-foreground/60 italic",
  string: KIND_HUE.text,
  number: KIND_HUE.number,
  boolean: KIND_HUE.boolean,
  date: KIND_HUE.datetime,
  json: KIND_HUE.json,
  binary: KIND_HUE.binary,
}

/** Whether a kind is set against the right edge of a column, as figures are. */
export function alignsRight(kind: ValueKind) {
  return kind === "number"
}

/**
 * One value, drawn inline and typed: a cell of a grid, a field of a document,
 * a key's value in a list.
 *
 * NULL and the empty string are two different words in two different voices,
 * because they are two different facts and the commonest bug a table editor
 * hides is one read as the other. A number keeps every digit it arrived with;
 * a JSON value is its compact text; bytes are their first eight and their
 * size; long text is cut at `clamp` characters with the cut said. The whole
 * value is never put in the page for a cell — a megabyte of text costs one
 * line — and the caller's own detail view is where it is read in full.
 */
export function ValueText({
  value,
  type,
  clamp,
  className,
}: {
  value: unknown
  /** The column's own type name, which is what says a string is a number or a moment. */
  type?: string
  /** The most characters drawn. */
  clamp?: number
  className?: string
}) {
  const kind = valueKind(value, type)
  const preview = valuePreview(value, kind, clamp)
  return (
    <span
      data-slot="value-text"
      data-kind={kind}
      className={cn("font-mono text-xs", VALUE_KIND_CLASS[kind], className)}
    >
      {preview.text}
      {kind === "binary" && (
        <span className="text-muted-foreground">
          {preview.clipped ? "… " : " "}
          {bytes(binarySize(String(value)))}
        </span>
      )}
    </span>
  )
}
