import { Link } from "react-router"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import {
  authEntrances,
  entranceLoginPath,
  type AuthEntrance,
} from "../lib/auth-entrance"

export function AuthEntranceLinks({ entry }: { entry: AuthEntrance | null }) {
  const t = useTranslations("iam")
  return (
    <nav
      aria-label={t("authEntrances")}
      className="grid grid-cols-4 border-b text-center text-sm"
    >
      {authEntrances.map((item) => (
        <Link
          key={item}
          to={entranceLoginPath(item)}
          aria-current={entry === item ? "page" : undefined}
          className={`px-1 py-3 ${entry === item ? "border-b-2 border-primary font-medium text-foreground" : "text-muted-foreground hover:text-foreground"}`}
        >
          {t(`authEntrance_${item}`)}
        </Link>
      ))}
    </nav>
  )
}
