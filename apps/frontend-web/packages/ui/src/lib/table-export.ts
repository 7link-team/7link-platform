/** Quote CSV cells and keep spreadsheet applications from executing cell content. */
export function tableCsv(rows: readonly (readonly string[])[]): string {
  return rows
    .map((row) =>
      row
        .map((value) => {
          const text = /^[\s]*[=+@-]/.test(value) ? `'${value}` : value
          return `"${text.replaceAll('"', '""')}"`
        })
        .join(",")
    )
    .join("\r\n")
}

export function downloadTableCsv(
  rows: readonly (readonly string[])[],
  filename = "table.csv"
) {
  const url = URL.createObjectURL(
    new Blob(["\ufeff", tableCsv(rows)], { type: "text/csv;charset=utf-8" })
  )
  const link = document.createElement("a")
  link.href = url
  link.download = filename
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
