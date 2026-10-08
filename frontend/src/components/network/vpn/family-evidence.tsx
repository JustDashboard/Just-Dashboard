import { Detail, DetailList } from "@/components/page"
import { Status } from "@/components/status-dot"
import type { WGInterface } from "@/lib/types"

export function WireGuardFamilyEvidence({ tunnel }: { tunnel: WGInterface }) {
  if (!tunnel.families) return null
  return (
    <div className="mb-5 space-y-3" aria-label={`${tunnel.name} address family evidence`}>
      <DetailList>
        {(["ipv4", "ipv6"] as const).map((family) => {
          const state = tunnel.families![family]
          const label = family === "ipv4" ? "IPv4" : "IPv6"
          const exit = state.exit
          return (
            <Detail key={family} label={label}>
              <div className="space-y-1">
                <p className="font-mono break-all">
                  {state.subnet || "No tunnel network"}
                  {" · "}
                  {state.runtime.replaceAll("_", " ")}
                </p>
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-muted-foreground">
                    Exit {exit.configured ? "configured" : "off"}
                    {exit.interface ? ` through ${exit.interface}` : ""}
                  </span>
                  {exit.runtime !== "disabled" && (
                    <Status
                      label={
                        exit.runtime === "verified"
                          ? "Rules verified"
                          : exit.runtime === "degraded"
                            ? "Degraded"
                            : "Unverified"
                      }
                      tone={
                        exit.runtime === "verified"
                          ? "running"
                          : exit.runtime === "degraded"
                            ? "warning"
                            : "unknown"
                      }
                    />
                  )}
                </div>
                {(state.reason || exit.reason) && (
                  <p className="text-hint text-muted-foreground">{state.reason || exit.reason}</p>
                )}
                {!exit.configured && state.configured && !exit.capability.writable && (
                  <p className="text-hint text-muted-foreground">{exit.capability.reason}</p>
                )}
              </div>
            </Detail>
          )
        })}
      </DetailList>
      <p className="text-hint text-muted-foreground">
        Rules and a fresh route from the first client address are local evidence. Public endpoint,
        provider reachability and another client&rsquo;s source policy remain untested.
      </p>
    </div>
  )
}
