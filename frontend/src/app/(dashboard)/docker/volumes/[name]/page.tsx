import { redirect } from "next/navigation"

// A deployment's dependency and removal links name a volume by path; the
// page addresses one by `?volume=`, which opens its sheet over the table.
export default async function VolumeLink({ params }: { params: Promise<{ name: string }> }) {
  const { name } = await params
  redirect(`/docker/volumes?volume=${encodeURIComponent(decodeURIComponent(name))}`)
}
