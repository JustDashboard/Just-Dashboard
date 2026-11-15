import { redirect } from "next/navigation"
import { ImagesPage } from "@/components/docker/images-page"
import { imageHref } from "@/components/docker/images"

// An image's detail was a sheet over the table, opened by `?image=`. A link
// kept from then, or one the server map writes, lands on the image's page:
// answered here, before the table is drawn, so nothing flashes on the way.
export default async function DockerImagesPage({
  searchParams,
}: {
  searchParams: Promise<{ image?: string | string[] }>
}) {
  const { image } = await searchParams
  const linked = Array.isArray(image) ? image[0] : image
  if (linked) redirect(imageHref(linked))
  return <ImagesPage />
}
