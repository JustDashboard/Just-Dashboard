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
    (!def.needsPort || (def.portOptional && !port.trim()) || !portProblem(port)) &&
    (!def.optionRequired || option.trim().length > 0)
  )
}

/** A verification address is a literal: names would resolve differently after waking. */
export function verifyProblem(address: string, port: string): string | undefined {
  const text = address.trim()
  if (!text) return port.trim() ? "A verification port needs an address." : undefined
  const v4 = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/.test(text)
  const v6 = text.includes(":") && /^[0-9a-f:.]+$/i.test(text)
  if (!v4 && !v6) return "Use the device's IP address, not a name."
  if (port.trim()) return portProblem(port)
}
