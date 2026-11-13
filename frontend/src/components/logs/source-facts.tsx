import { Fragment } from "react"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
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
  journal,
  aside,
}: {
  source: ServiceLogSource
  /**
   * The whole journal's own facts: how many units write to it, what it holds
   * on disk and how far back it goes. It opened on an instruction ("pick one
   * below to narrow it") where every other source says what it is.
   */
  journal?: JournalFacts
  aside?: React.ReactNode
}) {
  const stack = source.kind === "stack" && source.images?.length ? source.images : undefined
  const hasSize = source.size !== undefined && source.size > 0
  const facts = journal ? journalFacts(journal) : sourceFacts(source, hasSize)
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

export type JournalFacts = {
  /** Units with a journal of their own, as the source index lists them. */
  units?: number
  /** Bytes on disk; absent until the journal has been asked, zero where it would not say. */
  size?: number
  /** When its first record was written. */
  oldest?: string
}

/**
 * The journal's line. The unit count is there with the page; the size and
 * the age arrive a moment later, on the same line, so nothing under it
 * moves.
 */
function journalFacts(journal: JournalFacts) {
  return [
    <Tag key="kind">{KIND_TAG.journal}</Tag>,
    journal.units !== undefined && (
      <span key="units" className="numeric whitespace-nowrap">
        {plural(journal.units, "unit")}
      </span>
    ),
    journal.size ? (
      <span key="size" className="numeric whitespace-nowrap">
        {bytes(journal.size)} on disk
      </span>
    ) : null,
    journal.oldest && (
      <span key="oldest" className="numeric min-w-0 truncate" title={timestamp(journal.oldest)}>
        oldest line {relativeTime(journal.oldest)}
      </span>
    ),
  ].filter(Boolean)
}

function sourceFacts(source: ServiceLogSource, hasSize: boolean) {
  return [
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
}
