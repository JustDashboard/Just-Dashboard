"use client"

import { Copy } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import type { HTTPScan } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"

/**
 * Every header the HTTPS answer carried, as it came, for the questions the
 * graded rows do not ask: which cache answered, what the application calls
 * itself, which cookies it sets. Set-Cookie values are redacted by the scan
 * before they reach the page, so the list can be copied into a ticket.
 */
export function TLSResponseHeaders({ http }: { http: HTTPScan }) {
  const headers = http.responseHeaders ?? []
  const asked = [`${http.method ?? "GET"} ${http.path ?? "/"}`, http.host && `Host: ${http.host}`]
    .filter(Boolean)
    .join(" · ")
  const all = headers.map((header) => `${header.name}: ${header.value}`).join("\n")
  return (
    <Panel plain>
      <PanelHeader
        title="Response headers"
        actions={
          headers.length > 0 && (
            <Button
              type="button"
              variant="outline"
              size="xs"
              onClick={() => void copyText(all, "Response headers copied")}
            >
              <Copy className="size-3" />
              Copy all
            </Button>
          )
        }
      />
      <PanelBody className="space-y-3">
        <p className="text-hint leading-relaxed wrap-anywhere text-muted-foreground">
          <span className="font-mono">{asked}</span> answered {http.statusCode}
          {headers.length > 0 && ` with ${headers.length} header${headers.length === 1 ? "" : "s"}`}
          . Cookie values are replaced with &lt;redacted&gt; before the report leaves the server.
        </p>
        {headers.length > 0 && (
          <dl className="divide-y divide-hairline">
            {headers.map((header, index) => (
              <div
                key={`${header.name}-${index}`}
                className="group flex min-w-0 items-start gap-3 py-1.5 font-mono text-hint"
              >
                <dt className="w-2/5 shrink-0 wrap-anywhere text-muted-foreground sm:w-1/3">
                  {header.name}
                </dt>
                <dd className="min-w-0 flex-1 wrap-anywhere">{header.value}</dd>
                <IconAction
                  label={`Copy ${header.name}`}
                  reveal
                  className="-my-1 size-5"
                  onClick={() =>
                    void copyText(`${header.name}: ${header.value}`, `${header.name} copied`)
                  }
                >
                  <Copy />
                </IconAction>
              </div>
            ))}
          </dl>
        )}
        {http.headersTruncated && (
          <p className="text-hint text-muted-foreground">
            The list stops at the scan&apos;s cap of 150 headers.
          </p>
        )}
      </PanelBody>
    </Panel>
  )
}
