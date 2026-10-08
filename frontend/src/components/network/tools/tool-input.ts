import type { ToolDef } from "./tool-defs"

/** Port zero means a backend default; a user's malformed input must never select that default. */
export function portProblem(port: string): string | undefined {
  const text = port.trim()
  if (!/^\d+$/.test(text) || Number(text) < 1 || Number(text) > 65535) {
    return "Use a whole port number from 1 to 65535."
  }
}

export function toolReady(def: ToolDef, target: string, port: string, option: string): boolean {
  return (
    (!def.needsTarget || target.trim().length > 0) &&
    (!def.needsPort || !portProblem(port)) &&
    (!def.optionRequired || option.trim().length > 0)
  )
}
