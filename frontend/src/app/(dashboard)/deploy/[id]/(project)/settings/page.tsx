import { redirect } from "next/navigation"

export default async function SettingsIndex({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params
  redirect(`/deploy/${id}/settings/general`)
}
