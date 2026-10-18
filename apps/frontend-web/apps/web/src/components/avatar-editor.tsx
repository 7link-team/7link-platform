import { useEffect, useState } from "react"
import Cropper, { type Area } from "react-easy-crop"
import { RefreshCw, Upload } from "lucide-react"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { AutoForm } from "@workspace/ui/components/auto-form/auto-form"
import { Label } from "@workspace/ui/components/label"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { Alert } from "./alert"
import { UserAvatar } from "./user-avatar"
import { ApiError, iamApi, type AvatarSettings } from "../lib/iam-api"

const styles = ["robot", "pixel", "monster", "geometric"] as const

export function AvatarEditor({ id }: { id: string }) {
  const t = useTranslations("iam")
  const [avatar, setAvatar] = useState<AvatarSettings | null>(null)
  const [style, setStyle] = useState<(typeof styles)[number]>("robot")
  const [candidates, setCandidates] = useState<AvatarSettings[]>([])
  const [selected, setSelected] = useState<AvatarSettings | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [source, setSource] = useState<string | null>(null)
  const [crop, setCrop] = useState({ x: 0, y: 0 })
  const [zoom, setZoom] = useState(1)
  const [area, setArea] = useState<Area | null>(null)
  useEffect(() => {
    let active = true
    void iamApi.myAvatar().then(
      (next) => {
        if (active) setAvatar(next)
      },
      (cause: Error) => {
        if (active) {
          setAvatar(null)
          setError(
            cause instanceof ApiError && cause.status === 404
              ? ""
              : cause.message
          )
        }
      }
    )
    return () => {
      active = false
    }
  }, [id])
  const save = async (input: {
    style?: AvatarSettings["style"]
    image_data?: string
    candidate_url?: string
  }) => {
    setBusy(true)
    setError("")
    setNotice("")
    try {
      setAvatar(await iamApi.setMyAvatar(input))
      setSelected(null)
      window.dispatchEvent(new Event("avatar-change"))
      setNotice(t("avatarSaved"))
      return true
    } catch (cause) {
      setError((cause as Error).message)
      return false
    } finally {
      setBusy(false)
    }
  }
  const generate = async () => {
    setBusy(true)
    setError("")
    setNotice("")
    try {
      setCandidates(await iamApi.avatarCandidates(style))
      setSelected(null)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const upload = async (file?: File) => {
    if (!file) return
    if (
      !["image/png", "image/jpeg", "image/webp"].includes(file.type) ||
      file.size > 2 * 1024 * 1024
    ) {
      setError(t("avatarInvalidImage"))
      return
    }
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const data = await new Promise<string>((resolve, reject) => {
        const reader = new FileReader()
        reader.onload = () => resolve(String(reader.result))
        reader.onerror = () => reject(new Error(t("avatarInvalidImage")))
        reader.readAsDataURL(file)
      })
      const image = await createImageBitmap(file)
      try {
        if (image.width > 2048 || image.height > 2048)
          throw new Error(t("avatarInvalidImage"))
      } finally {
        image.close()
      }
      setCrop({ x: 0, y: 0 })
      setZoom(1)
      setArea(null)
      setSource(data)
    } catch {
      setError(t("avatarInvalidImage"))
    } finally {
      setBusy(false)
    }
  }
  const confirmCrop = async () => {
    if (!source || !area) return
    setBusy(true)
    try {
      const image = new Image()
      image.src = source
      await image.decode()
      const canvas = document.createElement("canvas")
      canvas.width = canvas.height = 256
      canvas
        .getContext("2d")!
        .drawImage(
          image,
          area.x,
          area.y,
          area.width,
          area.height,
          0,
          0,
          256,
          256
        )
      if (
        await save({
          style: "custom",
          image_data: canvas.toDataURL("image/png"),
        })
      )
        setSource(null)
    } catch {
      setError(t("avatarInvalidImage"))
    } finally {
      setBusy(false)
    }
  }
  return (
    <section className="border-b py-5" aria-label={t("avatar")}>
      <h2 className="mb-4 text-base font-semibold">{t("avatar")}</h2>
      {error && <Alert>{error}</Alert>}
      <div className="flex flex-wrap items-start gap-5">
        <UserAvatar id={id} avatar={avatar} className="size-24" />
        <div className="min-w-0 flex-1 space-y-3">
          <AutoForm
            className="md:grid-cols-1 [&>*]:col-span-1"
            onChange={(values) =>
              setStyle(values.style as (typeof styles)[number])
            }
            fields={{
              style: {
                name: t("avatarStyle"),
                type: "radio",
                value: style,
                disabled: busy,
                config: {
                  options: styles.map((value) => ({
                    value,
                    label: t(`avatarStyle_${value}`),
                  })),
                },
              },
            }}
          />
          <div className="flex flex-wrap gap-2">
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => void generate()}
            >
              <RefreshCw />
              {t("avatarShuffle")}
            </Button>
            <Label
              className={`relative flex min-h-11 cursor-pointer items-center gap-2 rounded-md border px-3 text-sm focus-within:ring-2 focus-within:ring-ring ${busy ? "opacity-50" : "hover:bg-accent"}`}
            >
              <Upload className="size-4" />
              {t("avatarUpload")}
              <input
                aria-label={t("avatarUpload")}
                type="file"
                accept="image/png,image/jpeg,image/webp"
                disabled={busy}
                className="absolute inset-0 w-full cursor-pointer opacity-0"
                onChange={(event) => {
                  void upload(event.target.files?.[0])
                  event.target.value = ""
                }}
              />
            </Label>
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => void save({})}
            >
              {t("avatarReset")}
            </Button>
          </div>
          {candidates.length > 0 && (
            <fieldset disabled={busy} aria-label={t("avatarCandidates")}>
              <legend className="mb-2 text-sm">{t("avatarCandidates")}</legend>
              <div className="grid max-w-md grid-cols-3 gap-2 sm:grid-cols-6">
                {candidates.map((candidate, index) => (
                  <Label
                    key={candidate.url}
                    className="flex cursor-pointer flex-col items-center gap-2 rounded-md border p-2 has-[:checked]:border-primary has-[:checked]:bg-accent"
                  >
                    <UserAvatar
                      id={id}
                      avatar={candidate}
                      className="size-12"
                    />
                    <input
                      type="radio"
                      name="avatar-candidate"
                      aria-label={t("avatarCandidate", { number: index + 1 })}
                      checked={selected?.url === candidate.url}
                      onChange={() => setSelected(candidate)}
                    />
                  </Label>
                ))}
              </div>
              <Button
                className="mt-3"
                disabled={busy || !selected}
                onClick={() =>
                  selected &&
                  void save({
                    style: selected.style,
                    candidate_url: selected.url,
                  })
                }
              >
                {t("save")}
              </Button>
            </fieldset>
          )}
          {busy && (
            <p role="status" className="text-sm">
              {t("saving")}
            </p>
          )}
          {notice && (
            <p role="status" className="text-sm">
              {notice}
            </p>
          )}
        </div>
      </div>
      <Dialog
        open={source !== null}
        onOpenChange={(open) => {
          if (!open && !busy) setSource(null)
        }}
      >
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>{t("avatarCrop")}</DialogTitle>
          </DialogHeader>
          <div className="relative aspect-square w-full overflow-hidden rounded-md bg-muted">
            {source && (
              <Cropper
                image={source}
                crop={crop}
                zoom={zoom}
                aspect={1}
                onCropChange={setCrop}
                onZoomChange={setZoom}
                onCropComplete={(_, pixels) => setArea(pixels)}
              />
            )}
          </div>
          <Label htmlFor="avatar-zoom">{t("avatarZoom")}</Label>
          <input
            id="avatar-zoom"
            type="range"
            min={1}
            max={3}
            step={0.05}
            value={zoom}
            disabled={busy}
            onChange={(event) => setZoom(Number(event.target.value))}
            className="min-h-11 w-full accent-primary"
          />
          {error && <Alert>{error}</Alert>}
          <DialogFooter>
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => setSource(null)}
            >
              {t("cancel")}
            </Button>
            <Button disabled={busy || !area} onClick={() => void confirmCrop()}>
              {busy ? t("saving") : t("avatarCropSave")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  )
}
