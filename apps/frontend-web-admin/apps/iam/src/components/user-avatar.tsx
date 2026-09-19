import { UserRound } from "lucide-react"
import { getTenant, type AvatarSettings } from "../lib/iam-api"

export function UserAvatar({
  id,
  avatar,
  className = "size-9",
}: {
  id: string
  avatar?: AvatarSettings | null
  className?: string
}) {
  if (avatar?.url !== `/api/principals/${id}/avatar`)
    return (
      <span
        className={`grid shrink-0 place-items-center rounded-full bg-muted ${className}`}
      >
        <UserRound className="size-4" aria-hidden="true" />
      </span>
    )
  const tenant = getTenant()
  const source =
    avatar.url + (tenant ? `?tenant_id=${encodeURIComponent(tenant)}` : "")
  return (
    <img
      src={source}
      alt=""
      width={256}
      height={256}
      className={`shrink-0 rounded-full bg-muted object-cover ${className}`}
    />
  )
}
