import type { DriftReport } from "@/lib/network-drift"
import { Disclosure } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { RowList } from "@/components/row-list"
import { Status } from "@/components/status-dot"
import { Digest } from "@/components/network/drift/digest"

/** Each blocklist's three generations — cached, rendered and in the kernel — which are what "enforced" is compared on. */
export function DriftBlocklists({ lists }: { lists: DriftReport["blocklists"] }) {
  return (
    <Panel plain>
      <PanelHeader title="Blocklist generations" />
      <PanelBody>
        <RowList>
          {lists.map((list) => (
            <li key={list.id} className="space-y-2 py-3">
              <div className="flex items-center justify-between gap-4">
                <span className="text-body font-medium">{list.name}</span>
                <Status
                  tone={
                    list.enforcement === "verified"
                      ? "running"
                      : list.enforcement === "degraded"
                        ? "warning"
                        : "unknown"
                  }
                  label={list.enforcement}
                />
              </div>
              <p className="text-body text-muted-foreground">
                Cache: {list.cache.status} · Runtime set: {list.runtime.status}
              </p>
              <Disclosure quiet summary="Set evidence">
                <div className="space-y-2 text-body">
                  <Digest label="Cached contents" value={list.cache.generation} />
                  <Digest label="Rendered contents" value={list.renderedGeneration} />
                  <Digest label="Kernel contents" value={list.runtime.generation} />
                  {list.cache.error && <p>{list.cache.error}</p>}
                  {list.runtime.error && <p>{list.runtime.error}</p>}
                </div>
              </Disclosure>
            </li>
          ))}
        </RowList>
      </PanelBody>
    </Panel>
  )
}
