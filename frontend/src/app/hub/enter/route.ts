import { hubEnterDocument } from "@/lib/hub-enter"

/**
 * `/hub/enter`, where Just Hub opens this server with a sign-in ticket in the fragment
 * (hub-link-protocol §9.5). A hand-written document rather than a page of the app, so that its
 * first script, which takes the ticket out of the address, runs before anything else can; why is
 * in `lib/hub-enter.ts`.
 */
export const dynamic = "force-dynamic"

export function GET(request: Request) {
  // The proxy makes a nonce per document and puts it in the CSP it sends with this response.
  const nonce = request.headers.get("x-nonce") ?? ""
  return new Response(hubEnterDocument(nonce), {
    headers: {
      "Content-Type": "text/html; charset=utf-8",
      "Cache-Control": "no-store",
      "Referrer-Policy": "no-referrer",
    },
  })
}
