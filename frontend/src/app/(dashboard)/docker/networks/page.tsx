import { Suspense } from "react"
import { redirect } from "next/navigation"
import { Networks } from "@/components/docker/networks-page"
import { networkHref } from "@/components/docker/networks"

// A network was a sheet over this table, addressed as `?network=` by its id.
// It is a page of its own now, and every address that opened the sheet lands
// on it: the page takes an id as well as a name.
export default async function DockerNetworksPage({
  searchParams,
}: {
  searchParams: Promise<{ network?: string | string[] }>
}) {
  const { network } = await searchParams
  const id = Array.isArray(network) ? network[0] : network
  if (id) redirect(networkHref(id))
  return (
    // A shared address plan's reservation arrives in the query string, which
    // the App Router only hands out inside a Suspense boundary.
    <Suspense>
      <Networks />
    </Suspense>
  )
}
