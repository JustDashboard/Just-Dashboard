/** One container a `ContainerPicker` offers. */
export type PickerItem = {
  name: string
  /** The engine's own (`pg_catalog`, `admin`): listed last, under their own word. */
  system?: boolean
  /** What it holds, at the end of its line: "12", "3 · 2.1 MB". */
  figure?: string
  title?: string
}

/**
 * The picker's two runs, narrowed by what was typed in its find box: the
 * containers somebody made, then the engine's own. The order within each is
 * the order given — the server's.
 */
export function pickerGroups(items: readonly PickerItem[], query: string) {
  const needle = query.trim().toLowerCase()
  const match = (item: PickerItem) => !needle || item.name.toLowerCase().includes(needle)
  return {
    own: items.filter((item) => !item.system && match(item)),
    system: items.filter((item) => item.system && match(item)),
  }
}
