import { bytes, relativeTime } from "@/lib/format"
import type { LogSource } from "@/lib/types"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { KIND_TAG } from "@/components/logs/source-rail"

/**
 * What the chosen source is: its kind, where it lives, how big it is and
 * whether its writer is running. The facts sit beside the name in the
 * workspace's strip rather than under a title as a caption, and truncate
 * rather than wrap so the strip stays one line. The path goes first when
 * the strip runs short: the name beside it already says which file, and
 * the size and the rotated set are what the reader weighs.
 */
export function SourceFacts({ source }: { source: Omit<LogSource, "rotated"> }) {
  const hasSize = source.size !== undefined && source.size > 0
  return (
    <span className="hidden min-w-0 items-center gap-x-3 text-hint text-muted-foreground md:flex">
      <Tag>{KIND_TAG[source.kind]}</Tag>
      {source.status && <Status state={source.status} className="text-hint" />}
      {source.path && (
        <span className="truncate font-mono max-2xl:hidden" title={source.path}>
          {source.path}
        </span>
      )}
      {hasSize ? (
        <span className="numeric whitespace-nowrap">
          {bytes(source.size)} · {relativeTime(source.modified)}
        </span>
      ) : (
        source.detail && <span className="truncate">{source.detail}</span>
      )}
      {(source.archives ?? 0) > 0 && (
        <span className="numeric whitespace-nowrap max-xl:hidden">
          {source.archives} rotated · {bytes(source.archiveBytes)}
        </span>
      )}
    </span>
  )
}
