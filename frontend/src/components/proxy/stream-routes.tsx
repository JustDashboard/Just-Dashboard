"use client"

import { Plus, Trash } from "@/components/icons"
import type { StreamModule, StreamRoute, StreamSpec } from "@/lib/types"
import { FormNote, OptionList, OptionRow } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

type StreamRoutesFields = Pick<StreamSpec, "protocol" | "tls" | "upstreamTls" | "routes">

/**
 * Several TLS services on one port: nginx reads the name each client asks
 * for from its TLS hello and passes the connection through, still
 * encrypted, to the backend for that name. nginx needs
 * stream_ssl_preread_module to read the name, so without it the control is
 * not offered — unless a stream already routes and has to be able to stop.
 */
export function StreamRoutes({
  spec,
  module,
  onChange,
}: {
  spec: StreamRoutesFields
  module: StreamModule
  onChange: (change: Partial<StreamSpec>) => void
}) {
  const routes = spec.routes ?? []
  const routing = routes.length > 0
  // An unknown module is left to nginx's test, which the save runs.
  if (module.preread === false && module.state !== "unknown" && !routing) {
    return (
      <FormNote>
        This nginx was built without stream_ssl_preread_module, so a stream cannot route by TLS
        name.
      </FormNote>
    )
  }
  const blocker =
    spec.protocol !== "tcp"
      ? "TCP only — TLS names are read from a TCP connection."
      : spec.tls || spec.upstreamTls
        ? "The TLS passes through unopened, so turn off serving TLS and encrypting to the backend first."
        : undefined
  const setRoutes = (next: StreamRoute[]) =>
    onChange({ routes: next.length > 0 ? next : undefined })
  const update = (i: number, change: Partial<StreamRoute>) =>
    setRoutes(routes.map((route, j) => (j === i ? { ...route, ...change } : route)))

  return (
    <OptionList>
      <OptionRow
        title="Route by TLS name"
        hint={
          (!routing && blocker) ||
          "nginx reads the name each client asks for and passes its TLS through unopened, so every backend keeps its own certificate. Any other name, no name, or a client not speaking TLS goes to the server above. A protocol where the server speaks first, like SSH or SMTP, waits for bytes the client never sends."
        }
        checked={routing}
        disabled={!routing && Boolean(blocker)}
        onCheckedChange={(v) => setRoutes(v ? [{ name: "", upstream: "" }] : [])}
      >
        <div className="space-y-2">
          <ol className="space-y-2">
            {routes.map((route, i) => (
              <li key={i} className="flex min-w-0 flex-col gap-1.5 sm:flex-row sm:items-center">
                <Input
                  value={route.name}
                  onChange={(e) => update(i, { name: e.target.value })}
                  placeholder="app.example.com"
                  aria-label={`Route ${i + 1} name`}
                  className="min-w-0 flex-1 font-mono text-xs"
                />
                <div className="flex min-w-0 flex-1 items-center gap-1.5">
                  <Input
                    value={route.upstream}
                    onChange={(e) => update(i, { upstream: e.target.value })}
                    placeholder="10.0.0.5:443"
                    aria-label={`Route ${i + 1} backend`}
                    className="min-w-0 flex-1 font-mono text-xs"
                  />
                  <IconAction
                    label={`Remove route ${i + 1}`}
                    className="text-muted-foreground hover:text-destructive"
                    onClick={() => setRoutes(routes.filter((_, j) => j !== i))}
                  >
                    <Trash />
                  </IconAction>
                </div>
              </li>
            ))}
          </ol>
          <Button
            size="xs"
            variant="outline"
            onClick={() => setRoutes([...routes, { name: "", upstream: "" }])}
          >
            <Plus />
            Route
          </Button>
          {blocker && <FormNote>{blocker}</FormNote>}
        </div>
      </OptionRow>
    </OptionList>
  )
}
