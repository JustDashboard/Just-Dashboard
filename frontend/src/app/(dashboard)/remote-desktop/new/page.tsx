import { AddComputer } from "@/components/remote-desktop/add-computer"

/**
 * `?peer=` arrives from the overview's tailnet list with that device chosen,
 * so the flow opens on the step after it.
 */
export default async function AddComputerPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>
}) {
  const { peer } = await searchParams
  return <AddComputer peer={typeof peer === "string" ? peer : undefined} />
}
