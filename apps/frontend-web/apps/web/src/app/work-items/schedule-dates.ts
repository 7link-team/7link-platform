export function calendarRange(day: string, mode: string) {
  const parsed = /^\d{4}-\d{2}-\d{2}$/.test(day) ? new Date(`${day}T00:00:00`) : new Date()
  const start = Number.isNaN(parsed.getTime()) ? new Date() : parsed
  start.setHours(0, 0, 0, 0)
  if (mode === "month") start.setDate(1)
  if (mode === "week") start.setDate(start.getDate() - ((start.getDay() + 6) % 7))
  const end = new Date(start)
  if (mode === "month") end.setMonth(end.getMonth() + 1)
  else end.setDate(end.getDate() + 7)
  const days: Date[] = []
  for (const date = new Date(start); date < end; date.setDate(date.getDate() + 1)) days.push(new Date(date))
  return { start, end, days }
}
export function localDateTime(value: string | null | undefined) {
  if (!value) return ""
  const date = new Date(value)
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}
export function overlapsDay(start: string | null | undefined, end: string | null | undefined, day: Date) {
  if (!start || !end) return false
  const next = new Date(day); next.setDate(next.getDate() + 1)
  return new Date(start) < next && new Date(end) > day
}
