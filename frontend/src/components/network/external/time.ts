/** A moment as a reader writes it down: "8 Oct 2026, 10:04". */
export function calendarTime(iso: string | undefined | null) {
  if (!iso) return "not recorded"
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return "not recorded"
  return at.toLocaleString("en-GB", { dateStyle: "medium", timeStyle: "short" })
}
