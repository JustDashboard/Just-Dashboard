import { Globe } from "@/components/icons"
import { Tag } from "@/components/tag"

/**
 * How far a thing on this host can be reached from — an interface's address, a
 * connected peer's origin. One rendering, used by both the Network and the
 * Connections page, so the same distinction does not read two different ways
 * across the section.
 *
 * It is a *tag* (a fixed property of the row), not the app's status vocabulary:
 * `warning` for the internet because that is the one worth looking at, and
 * everything private stays quiet so a column of them does not compete with it.
 */
export function Reach({ scope }: { scope: "internet" | "private" | "local" }) {
  if (scope === "internet") {
    return (
      <Tag tone="warning" icon={Globe}>
        internet
      </Tag>
    )
  }
  return <Tag>{scope === "local" ? "local only" : "private"}</Tag>
}
