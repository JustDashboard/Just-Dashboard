"use client"

import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { ProductLogos } from "@/components/product-logo"

/**
 * The parts of a gateway that are not a port or a network: serving a domain
 * from something behind this server, passing a raw TCP or UDP port to it, and
 * the certificates that make the first one HTTPS. They live in Proxy & TLS,
 * where the engine that does them is configured, so each is a lit card that
 * goes there rather than a second form here that would write the same thing
 * twice. Each is drawn as the product that does the work, with its title and
 * arrow and nothing under them: a card you open says where it goes, not what
 * is behind it.
 */
export function ProxyHandoffs() {
  return (
    <ChoiceGrid>
      <ChoiceCard
        index={0}
        href="/proxy/sites"
        verb="Open reverse proxy sites"
        logo={<ProductLogos ids={["caddy", "nginx"]} />}
        title="Reverse proxy"
      />
      <ChoiceCard
        index={1}
        href="/proxy/streams"
        verb="Open TCP and UDP streams"
        logo={<ProductLogos ids={["nginx"]} />}
        title="TCP and UDP streams"
      />
      <ChoiceCard
        index={2}
        href="/proxy/certificates"
        verb="Open certificates"
        logo={<ProductLogos ids={["lets-encrypt"]} />}
        title="Certificates"
      />
    </ChoiceGrid>
  )
}
