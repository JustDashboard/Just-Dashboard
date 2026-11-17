/**
 * Where a mono value — a path, an address, a unit's name — may break: after
 * each run of its separators (`/`, `.`, `:`), never inside a word. The pieces
 * joined are the value again, so `MonoValue` draws a `<wbr>` between them.
 *
 * A leading run stays on the first word ("/etc/", not "/" alone on a line),
 * and so does a separator that ends the value. A value with no separator is
 * one piece.
 */
export function monoBreaks(value: string): string[] {
  const pieces = value.match(/[^/.:]*[/.:]*/g)?.filter((piece) => piece !== "") ?? []
  if (pieces.length > 1 && /^[/.:]+$/.test(pieces[0])) {
    const [lead, next, ...rest] = pieces
    return [lead + next, ...rest]
  }
  return pieces.length > 0 ? pieces : [value]
}
