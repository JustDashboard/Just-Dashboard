import type { DotTone } from "@/components/status-dot"
import type { ControlCheck, PolicyControl } from "@/lib/types"

/**
 * Reading a site's service policy and its measurement. A control the build
 * lacks is said beside its setting; a measured one is verified only by what
 * nginx answered.
 */

export function controlSetting(control: PolicyControl): string {
  if (!control.configured) return "not set"
  return control.setting ?? "set"
}

/** The build's part, worded only where it is not simply there. */
export function supportWord(control: PolicyControl): string | undefined {
  switch (control.support) {
    case "missing":
      return "this nginx lacks its module"
    case "unknown":
      return "build not read"
  }
  return undefined
}

const STATE: Record<ControlCheck["state"], { label: string; tone: DotTone }> = {
  verified: { label: "Verified", tone: "running" },
  "not-effective": { label: "Not in effect", tone: "danger" },
  "not-measured": { label: "Not measured", tone: "unknown" },
  "not-configured": { label: "Not set", tone: "unknown" },
}

export function checkState(check: ControlCheck): { label: string; tone: DotTone } {
  return STATE[check.state]
}

/** A path the measurement may ask for: starts with /, no spaces or fragment. */
export function validPath(raw: string): boolean {
  return raw === "" || (raw.startsWith("/") && !/[\s#]/.test(raw) && raw.length <= 512)
}
