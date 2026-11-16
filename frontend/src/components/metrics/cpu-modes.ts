/**
 * Where the processor's time went, as the one line the Overview and Metrics
 * both say under CPU: "12% user · 4% sys · 1% wait". It was the same template
 * literal in both files (HST-25).
 */
export function cpuModes(modes: { user: number; system: number; iowait: number }): string {
  return `${modes.user.toFixed(0)}% user · ${modes.system.toFixed(0)}% sys · ${modes.iowait.toFixed(0)}% wait`
}
