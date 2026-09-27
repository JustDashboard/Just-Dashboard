import { bytes, relativeTime } from "@/lib/format"
import type { LogSource } from "@/lib/types"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { KIND_TAG } from "@/components/logs/source-rail"

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
