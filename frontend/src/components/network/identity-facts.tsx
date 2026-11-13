import { Skeleton } from "@/components/ui/skeleton"

/**
 * The facts of an identity line, held to the room they take once they have
 * landed. `HostIdentity` wraps its facts as they come; on a phone a line of
 * four wraps to two, and a page that drew one grey bar while it loaded grew
 * by a line when the read came back, moving everything under it. Here the
 * facts keep two lines' room on a phone in every state, and the placeholder
 * is as tall as the words it stands for.
 */
export function IdentityFacts({
  loading,
  children,
}: {
  loading?: boolean
  children: React.ReactNode
}) {
  return (
    <span className="flex min-w-0 flex-wrap content-start items-center gap-x-3 gap-y-1 max-sm:min-h-9">
      {loading ? <Placeholder className="w-64" /> : children}
    </span>
  )
}

/** A state word that has not arrived, at the height of the word. */
export function IdentityStateLoading() {
  return (
    <span className="inline-flex items-center text-xs font-medium">
      <Placeholder className="w-32" />
    </span>
  )
}

/**
 * A grey bar on a line of text's own height: the space before it is a real
 * character, so the line box is the one the words will have.
 */
function Placeholder({ className }: { className: string }) {
  return (
    <span aria-hidden className="inline-flex max-w-full items-center">
      {" "}
      <Skeleton className={`h-3 max-w-full ${className}`} />
    </span>
  )
}
