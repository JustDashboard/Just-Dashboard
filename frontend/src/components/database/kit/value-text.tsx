import { bytes } from "@/lib/format"
import { cn } from "@/lib/utils"
import {
  binarySize,
  valueKind,
  valuePreview,
  type ValueKind,
} from "@/components/database/kit/values"

/**
 * The legend of value kinds, one hue each from the `--tag-*` set — the same
 * ones the log console gives a number and a string, so a kind keeps its
 * colour across the product. They sit at one lightness; none of them is a
 * status hue, because none of them is a reading of state.
 *
 * Text is the foreground: it is what a row is mostly made of, and a grid in
 * which every cell is tinted has no figure left to find.
 */
export const VALUE_KIND_CLASS: Record<ValueKind, string> = {
  null: "text-muted-foreground/60",
  empty: "text-muted-foreground/60 italic",
  string: "",
  number: "numeric text-[var(--tag-pink)]",
  boolean: "text-[var(--tag-violet)]",
  date: "text-[var(--tag-cyan)]",
  json: "text-[var(--tag-slate)]",
  binary: "text-[var(--tag-amber)]",
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
