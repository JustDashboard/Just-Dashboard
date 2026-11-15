import { redirect } from "next/navigation"
import { VolumesPage } from "@/components/docker/volumes-page"
import { volumeHref } from "@/components/docker/volumes"

// A volume was a sheet over this table, addressed as `?volume=`. It is a page
// of its own now, and every address that opened the sheet lands on it.
export default async function DockerVolumesPage({
  searchParams,
}: {
  searchParams: Promise<{ volume?: string | string[] }>
}) {
  const { volume } = await searchParams
  const name = Array.isArray(volume) ? volume[0] : volume
  if (name) redirect(volumeHref(name))
  return <VolumesPage />
}
