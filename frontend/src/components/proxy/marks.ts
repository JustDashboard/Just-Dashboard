import type { Certificate, VHost } from "@/lib/types"
import { issuerProduct } from "@/components/product-logo"
import type { ProxyStatus } from "@/components/proxy/proxy-context"

/**
 * What the proxy pages draw as itself (§14): the engine, and who signed a
 * certificate. Read once here so the overview's site list, the Sites cards,
 * the certificate rows and the TLS report agree about which mark a thing gets.
 */

/** The engine this host runs, as the product it is. */
export function engineProduct(status: ProxyStatus | undefined): string | undefined {
  if (status?.nginx) return "nginx-static"
  if (status?.caddy) return "caddy"
  return undefined
}

/** A site as the engine serving it: an nginx site is nginx's, a Caddy route Caddy's. */
export function siteProduct(vhost: Pick<VHost, "kind">): string {
  return vhost.kind === "caddy" ? "caddy" : "nginx-static"
}

/**
 * Who signed a certificate. The issuer's name says it when it is read from
 * the file; certbot's live directory says it when the file has not been read
 * yet — a watched domain, a site's `certPath`. Anything else is unnamed: an
 * imported certificate from a company CA has no mark this product could
 * honestly draw.
 */
export function certificateProduct(
  cert: Partial<Pick<Certificate, "issuer" | "source" | "path">> | undefined,
): string | undefined {
  if (!cert) return undefined
  return (
    issuerProduct(cert.issuer) ??
    (cert.source === "certbot" || certPathProduct(cert.path) ? "lets-encrypt" : undefined)
  )
}

/** Let's Encrypt, from a certificate path under certbot's live directory. */
export function certPathProduct(path: string | undefined): string | undefined {
  return path?.includes("/letsencrypt/") ? "lets-encrypt" : undefined
}

/**
 * The DNS providers certbot has plugins for, by the key the backend lists
 * them under. Only the ones with a mark of their own; the rest keep a glyph.
 */
const DNS_PROVIDERS: Record<string, string> = {
  cloudflare: "cloudflare",
  route53: "aws",
  google: "google-cloud",
  azure: "azure",
}

export function dnsProviderProduct(key: string): string | undefined {
  return DNS_PROVIDERS[key]
}
