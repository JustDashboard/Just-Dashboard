/** A successful flush acknowledges every edit, including edits made during a save. */
export async function drainBoardSaves(
  isDirty: () => boolean,
  save: () => Promise<boolean>,
): Promise<boolean> {
  while (isDirty()) {
    if (!(await save())) return false
  }
  return true
}
