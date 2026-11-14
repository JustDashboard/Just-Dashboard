import { NewAccount } from "@/components/system-users/new-account"
import { AdminOnly } from "@/components/system-users/read-state"

export default function NewSystemUserPage() {
  return (
    <AdminOnly title="New system account">
      <NewAccount />
    </AdminOnly>
  )
}
