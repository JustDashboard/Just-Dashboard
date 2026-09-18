/** Keep exact database types out of JavaScript's binary floating-point conversion. */
export function coerceDbValue(value: string, sqlType: string): unknown {
  const type = sqlType.toLowerCase()
  if (/bool/.test(type)) {
    if (/^(true|t|1|yes)$/i.test(value)) return true
    if (/^(false|f|0|no)$/i.test(value)) return false
    return value
  }
  // Even an unqualified INTEGER can be 64-bit (SQLite). The server binds
  // these strings according to the actual database column type.
  if (/numeric|decimal|number\b/.test(type)) return value
  if (/int|serial/.test(type) && !/interval|point/.test(type)) return value
  if (/real|double|float/.test(type)) {
    if (!value.trim()) return value
    const number = Number(value)
    if (!Number.isNaN(number) && !Number.isFinite(number)) {
      throw new Error("This number is outside the supported finite range.")
    }
    return Number.isNaN(number) ? value : number
  }
  return value
}
