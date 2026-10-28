const phases = {
  idle: "点击检查，获取可用的新版本。",
  checking: "正在检查更新…",
  current: "当前已是最新版本。",
  available: "发现新版本，可下载更新并重启。",
  downloading: "正在下载并校验更新…",
  installing: "正在安装更新，请勿关闭应用…",
  error: "更新未完成，你可以重新检查后重试。",
}

export function renderUpdate(doc, state) {
  const busy = ["checking", "downloading", "installing"].includes(state.phase)
  doc.querySelector("#version").textContent = `当前版本 ${state.current || "—"} · ${state.channel === "candidate" ? "候选版" : "正式版"}${state.version ? ` → ${state.version}` : ""}`
  doc.querySelector("#update-status").textContent = phases[state.phase] || phases.idle
  doc.querySelector("#update-error").textContent = state.error || ""
  doc.querySelector("#check-update").disabled = busy
  const install = doc.querySelector("#install-update")
  install.hidden = !["available", "downloading", "installing"].includes(state.phase)
  install.disabled = busy
  const progress = doc.querySelector("#update-progress")
  progress.hidden = state.phase !== "downloading"
  if (state.total > 0) { progress.max = state.total; progress.value = Math.min(state.downloaded || 0, state.total) }
  else progress.removeAttribute("value")
  doc.querySelector("#notes").textContent = state.notes || ""
  doc.querySelector("#release-notes").hidden = !state.notes
}

export function startUpdater(doc, invoke) {
  const refresh = () => invoke("update_status").then(state => renderUpdate(doc, state), error => {
    doc.querySelector("#update-error").textContent = String(error)
  })
  const action = command => {
    doc.querySelector("#check-update").disabled = true
    doc.querySelector("#install-update").disabled = true
    void invoke(command).catch(error => {
      doc.querySelector("#update-error").textContent = String(error)
    }).finally(refresh)
  }
  doc.querySelector("#check-update").addEventListener("click", () => action("check_update"))
  doc.querySelector("#install-update").addEventListener("click", () => action("install_update"))
  void refresh()
  const timer = setInterval(refresh, 500)
  return () => clearInterval(timer)
}

if (typeof document !== "undefined") {
  const stop = startUpdater(document, (command, args) => window.__TAURI_INTERNALS__.invoke(command, args))
  window.addEventListener("pagehide", stop, { once: true })
}
