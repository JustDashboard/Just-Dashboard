import { type NextRequest, NextResponse } from "next/server"

function webSocketSource(raw: string | undefined): string | undefined {
  if (!raw) return undefined
  try {
    const url = new URL(raw)
    if (url.protocol === "http:") url.protocol = "ws:"
    if (url.protocol === "https:") url.protocol = "wss:"
    if (
      (url.protocol !== "ws:" && url.protocol !== "wss:") ||
      url.username ||
      url.password ||
      (url.pathname !== "/" && url.pathname !== "") ||
      url.search ||
      url.hash
    ) {
      return undefined
    }
    return url.origin
  } catch {
    return undefined
  }
}

export function proxy(request: NextRequest) {
  // A new value per document lets Next nonce its runtime and inline data
  // without granting every inline script permission to execute.
  const nonce = Buffer.from(crypto.randomUUID()).toString("base64")
  const isDev = process.env.NODE_ENV === "development"
  // nextUrl describes the internal standalone server behind Caddy. Forwarded
  // scheme plus Host describe the browser origin whose socket CSP must allow.
  const forwardedProtocol = request.headers.get("x-forwarded-proto")?.split(",", 1)[0]?.trim()
  const requestProtocol = forwardedProtocol === "https" ? "https" : "http"
  const requestHost = request.headers.get("x-forwarded-host") ?? request.headers.get("host")
  const requestSocket = webSocketSource(
    requestHost ? `${requestProtocol}://${requestHost}` : undefined,
  )
  const socketSources = new Set(requestSocket ? [requestSocket] : [])
  const explicitSocket = webSocketSource(process.env.NEXT_PUBLIC_WS_BASE)
  if (explicitSocket) socketSources.add(explicitSocket)

  const policy = [
    "default-src 'self'",
    `script-src 'self' 'nonce-${nonce}' 'strict-dynamic'${isDev ? " 'unsafe-eval'" : ""}`,
    // React and the charting/editor surfaces intentionally use calculated
    // style attributes. Script remains nonce-only; CSS is the narrow exception.
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: blob:",
    "font-src 'self' data:",
    `connect-src 'self' ${[...socketSources].join(" ")}`,
    "worker-src 'self' blob:",
    "frame-src 'self' blob:",
    "media-src 'self' blob:",
    "object-src 'none'",
    "base-uri 'self'",
    "form-action 'self'",
    "frame-ancestors 'none'",
    "manifest-src 'self'",
    // Only where the browser actually arrived over HTTPS. The ssh-tunnel
    // install serves http://localhost deliberately — the tunnel is the
    // encrypted hop, and localhost is a secure context — and while the
    // upgrade algorithm is specified to leave trustworthy origins alone, a
    // directive whose whole job is to rewrite this page's own scheme has no
    // business being sent to a page that has no HTTPS to be upgraded to.
    ...(isDev || requestProtocol !== "https" ? [] : ["upgrade-insecure-requests"]),
  ].join("; ")

  const requestHeaders = new Headers(request.headers)
  requestHeaders.set("x-nonce", nonce)
  // Next reads this request header and applies the nonce to framework scripts.
  requestHeaders.set("Content-Security-Policy", policy)

  const response = NextResponse.next({ request: { headers: requestHeaders } })
  response.headers.set("Content-Security-Policy", policy)
  return response
}

export const config = {
  matcher: [
    {
      source: "/((?!api|_next/static|_next/image|favicon.ico).*)",
      missing: [
        { type: "header", key: "next-router-prefetch" },
        { type: "header", key: "purpose", value: "prefetch" },
      ],
    },
  ],
}
