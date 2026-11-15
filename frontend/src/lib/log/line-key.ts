import type { LogLine } from "@/lib/types"

// Evicting the head of a live buffer must not change the identity of every
// retained row. Weak keys release the metadata when the line leaves the buffer;
// identical text from two arrivals still represents two separate lines.
const arrivals = new WeakMap<LogLine, number>()
let nextArrival = 0

export function logLineKey(line: LogLine): number {
  let key = arrivals.get(line)
  if (key === undefined) {
    key = nextArrival++
    arrivals.set(line, key)
  }
  return key
}
