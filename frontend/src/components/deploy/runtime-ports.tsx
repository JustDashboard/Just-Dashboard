import { ProductGlyph, portProduct } from "@/components/product-logo"
import { Tag } from "@/components/tag"
import { SCOPE_WORD, type PublishedPort } from "@/components/deploy/runtime-model"

/**
 * Every port a service publishes, each with who can reach it. The port is a
 * literal from the host, so it is a mono tag; the scope is a property of it,
 * so it is a tag too, and only the one worth catching — bound on every
 * interface — takes a tone, as Docker's own table draws it. A port the
 * container's side is known to speak (5432, 6379) carries that program's mark.
 *
 * The container's side is what names the program: the host's is whatever the
 * operator chose to map it to.
 */
export function RuntimePorts({ ports }: { ports: PublishedPort[] }) {
  return (
    <ul
      aria-label="Published ports"
      className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1.5"
    >
      {ports.map((port) => {
        const product = portProduct(port.containerPort)
        return (
          <li key={port.key} title={port.summary} className="inline-flex items-center gap-1.5">
            <Tag mono>
              {product && <ProductGlyph id={product} className="size-3" />}
              {port.label}
            </Tag>
            <Tag tone={port.scope === "all" ? "warning" : "default"}>{SCOPE_WORD[port.scope]}</Tag>
          </li>
        )
      })}
    </ul>
  )
}
