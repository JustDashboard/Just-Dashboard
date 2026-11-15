/**
 * A dynamic route segment as the name it encodes.
 *
 * Next hands a page its `[name]` segment still percent-encoded, and
 * `decodeURIComponent` throws on an escape that is not one: an address with a
 * stray "%" (`/docker/stacks/50%off`) threw a URIError during render and
 * landed on the error page instead of the page's own "no such stack". So a
 * segment that does not decode is taken as typed, and the page looks that up
 * and says it is not there. A missing segment is "".
 *
 * Every detail page reads its segment through this; `decodeURIComponent`
 * anywhere else is for a value that is not a route segment.
 */
export function decodeSegment(segment: string | undefined | null): string {
  if (segment === undefined || segment === null) return ""
  try {
    return decodeURIComponent(segment)
  } catch {
    return segment
  }
}
