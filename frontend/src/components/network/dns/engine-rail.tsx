"use client"

import { dnsEngineName, type DNSConnection } from "@/lib/network/dns-services"
import { plural } from "@/lib/format"
import { Globe } from "@/components/icons"
import { ProductLogo } from "@/components/product-logo"
import { Rail, RailLine, RailName, RailRow } from "@/components/rail"
import { StatusDot } from "@/components/status-dot"
import { engineHref, type EngineTab } from "./engine-views"
import { ENGINE_PRODUCT } from "./services"

/**
 * The connected engines, beside the one that is open, from `xl`: each as the
 * product it is, with its name and whether changes may be reviewed through
 * it, the one on screen filled. The Services view's list has no filter, so
 * neither has this; the view on screen goes with you to the next engine.
 */
export function EngineRail({
  engines,
  current,
  tab,
  loading,
  failed,
  error,
  onRetry,
  className,
}: {
  engines: DNSConnection[]
  current: string
  tab: EngineTab
  loading: boolean
  /** The list could not be read: the column says so instead of counting nothing. */
  failed?: boolean
  /** The failure itself, where the page has it. */
  error?: Error
  onRetry?: () => void
  className?: string
}) {
  const failure =
    error ?? (failed ? new Error("The connected engines could not be read.") : undefined)
  return (
    <Rail
      slot="engine-rail"
      label="DNS engines"
      listLabel="Engines to open"
      caption={
        failure
          ? undefined
          : loading && engines.length === 0
            ? "Reading the engines…"
            : plural(engines.length, "connected engine")
      }
      rows={engines}
      keyOf={(engine) => engine.id}
      current={(engine) => engine.id === current}
      row={(engine, here) => (
        <RailRow
          item={engine.id}
          name={engine.name}
          current={here}
          href={engineHref(engine.id, tab)}
          mark={
            <ProductLogo
              id={ENGINE_PRODUCT[engine.engine]}
              fallback={Globe}
              size="sm"
              className="size-7 rounded-md [&_img]:size-4 [&_svg]:size-3.5"
            />
          }
          dot={<StatusDot tone={engine.management ? "notice" : "stopped"} className="size-1.5" />}
        >
          <RailName current={here}>{engine.name}</RailName>
          <RailLine>
            {dnsEngineName(engine.engine)} · {engine.management ? "reviewed changes" : "read-only"}
          </RailLine>
        </RailRow>
      )}
      loading={loading}
      loadingRows={3}
      error={failure}
      onRetry={onRetry}
      className={className}
    />
  )
}
