/**
 * Which app a domain routes to, where it is not the deployment's own: "→
 * Seerr" on the row, read as "serves Seerr". A stack gives each of its apps
 * a name of its own, and every list of the names says which is which.
 */
export function ServiceRoute({ title, className }: { title: string; className?: string }) {
  return (
    <span data-slot="service-route" className={className}>
      <span aria-hidden="true">→ </span>
      <span className="sr-only">Serves </span>
      {title}
    </span>
  )
}
