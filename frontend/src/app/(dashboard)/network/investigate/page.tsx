import { redirect } from "next/navigation"
import { pathToolHref } from "@/components/network/tools/tool-links"

// Connection path became one more diagnostic in the Tools workbench. Streams,
// the interfaces and connections pages and saved bookmarks still name this
// address with the tuple (`?target&port&protocol&family&measure`) or a
// container (`?container=`); every parameter travels to the tool, which
// fills the same fields the page did.
export default async function NetworkInvestigatorLink({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>
}) {
  redirect(pathToolHref(await searchParams))
}
