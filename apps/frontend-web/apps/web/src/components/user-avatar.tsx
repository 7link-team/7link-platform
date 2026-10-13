import { UserRound } from "lucide-react"
import { useEffect, useState } from "react"
import { getTenant, iamApi, type AvatarSettings } from "../lib/iam-api"
import { getAccountView } from "../lib/account-view"

export function UserAvatar({
  id,
  avatar,
  className = "size-9",
}: {
  id: string
  avatar?: AvatarSettings | null
  className?: string
}) {
  const view = getAccountView()
  const tenant = getTenant()
  const valid = avatar?.url === `/api/principals/${id}/avatar/content`
  const [viewImage, setViewImage] = useState<{
    id: string
    source: string
  } | null>(null)
  useEffect(() => {
    if (!view || !valid) return
    let active = true
    let source = ""
    void iamApi.avatarContent(id, tenant || undefined).then(
      (blob) => {
        if (!active) return
        source = URL.createObjectURL(blob)
        setViewImage({ id, source })
      },
      () => {}
    )
    return () => {
      active = false
      if (source) URL.revokeObjectURL(source)
    }
  }, [id, view, valid, tenant])
  if (!valid || (view && viewImage?.id !== id))
    return (
      <span
        className={`grid shrink-0 place-items-center rounded-full bg-muted ${className}`}
      >
        <UserRound className="size-4" aria-hidden="true" />
      </span>
    )
  const source = view
    ? viewImage?.source
    : avatar?.url + (tenant ? `?tenant_id=${encodeURIComponent(tenant)}` : "")
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
