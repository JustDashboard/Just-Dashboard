import { Fragment } from "react"
import { bytes, relativeTime } from "@/lib/format"
import type { LogSource } from "@/lib/types"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { ProductLogos, imageProducts } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { KIND_TAG, kindIcon } from "@/components/logs/source-rail"
import type { ServiceLogSource } from "@/components/logs/service-logs"

/**
 * What the chosen source is: its kind, where it lives, how big it is and
 * whether its writer is running. The facts sit beside the name in the
 * workspace's strip rather than under a title as a caption, and give way
 * rather than wrap so the strip stays one line.
 *
 * They give way by the strip's own width, not the window's: the same pane is
 * a column beside the logs page's rail, the whole of a service page, and a
 * sheet, and at 1280 beside the rail the facts used to push the source's name
 * down to "postgresql-16-ma…" and then clip their own last one mid-word. The
 * path goes first — the name already says which file — then the rotated set,
 * then the size; the kind and the state stay while there is any room.
 */
export function SourceFacts({ source }: { source: Omit<LogSource, "rotated"> }) {
  const hasSize = source.size !== undefined && source.size > 0
  return (
    // Shrinking a thousand times faster than the name beside it: the name
    // says which source this is, and loses a character only once the facts
    // have nothing left to give.
    <span className="hidden min-w-0 shrink-[1000] items-center gap-x-3 overflow-hidden text-hint text-muted-foreground @sm:flex">
      <Tag>{KIND_TAG[source.kind]}</Tag>
      {source.status && <Status state={source.status} className="shrink-0 text-hint" />}
      {source.path && (
        <span className="hidden min-w-0 truncate font-mono @4xl:block" title={source.path}>
          {source.path}
        </span>
      )}
      {hasSize ? (
        <span className="numeric hidden shrink-0 whitespace-nowrap @md:block">
          {bytes(source.size)} · {relativeTime(source.modified)}
        </span>
      ) : (
        source.detail && <span className="min-w-0 truncate">{source.detail}</span>
      )}
      {(source.archives ?? 0) > 0 && (
        <span className="numeric hidden shrink-0 whitespace-nowrap @2xl:block">
          {source.archives} rotated · {bytes(source.archiveBytes)}
        </span>
      )}
    </span>
  )
}

/**
 * The source being read, as the line the logs page opens on — the shape the
 * Overview and Metrics open on, and a deployment's Logs page under its
 * header: the source drawn as the product that writes it, its name, and what
 * it is in one line of facts. The page's verbs for the source sit at its end.
 *
 * It took the name and the facts out of the workbench's strip, where they
 * shared one 40px row with the Export button and the view tabs: the box and
 * the underline met edge to edge, and at 1280 the facts gave way before
 * anything else. The strip is the views now, as a deployment's pane is. Its
 * rule sits a step closer than the Overview's: the console under it needs
 * the height.
 */
export function SourceIdentity({
  source,
  aside,
}: {
  source: ServiceLogSource
  aside?: React.ReactNode
}) {
  const stack = source.kind === "stack" && source.images?.length ? source.images : undefined
  const hasSize = source.size !== undefined && source.size > 0
  const facts = [
    <Tag key="kind">{KIND_TAG[source.kind]}</Tag>,
    source.status && <Status key="status" state={source.status} />,
    source.path && (
      <span key="path" className="min-w-0 truncate font-mono" title={source.path}>
        {source.path}
      </span>
    ),
    hasSize ? (
      <span key="size" className="numeric whitespace-nowrap">
        {bytes(source.size)}, written {relativeTime(source.modified)}
      </span>
    ) : (
      source.detail && (
        <span key="detail" className="min-w-0 truncate">
          {source.detail}
        </span>
      )
    ),
    (source.archives ?? 0) > 0 && (
      <span key="archives" className="numeric whitespace-nowrap">
        {source.archives} rotated · {bytes(source.archiveBytes)}
      </span>
    ),
  ].filter(Boolean)
  return (
    <HostIdentity
      className="animate-rise pb-4"
      mark={source.product}
      logo={stack ? <ProductLogos ids={imageProducts(stack)} size="md" /> : undefined}
      fallback={kindIcon(source.kind)}
      title={source.label}
      facts={facts.map((fact, i) => (
        <Fragment key={i}>
          {i > 0 && <FactDot />}
          {fact}
        </Fragment>
      ))}
      aside={aside}
    />
  )
}
