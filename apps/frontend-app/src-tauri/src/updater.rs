use serde_json::{json, Value};
use std::sync::{Mutex, OnceLock};
use std::time::Duration;
use tauri::{Manager, WebviewUrl};
use tauri_plugin_updater::{Update, UpdaterExt};

pub(crate) struct UpdateState(Mutex<Inner>);

struct Inner {
    status: Value,
    update: Option<Update>,
}

impl UpdateState {
    pub(crate) fn new() -> Self {
        Self(Mutex::new(Inner {
            status: json!({"phase":"idle"}),
            update: None,
        }))
    }
}

fn require_updater(window: &tauri::WebviewWindow) -> Result<(), String> {
    let launcher = window.state::<OnceLock<tauri::Url>>();
    let mut expected = launcher.get().ok_or("应用启动页尚未就绪")?.clone();
    expected.set_path("/update.html");
    expected.set_query(None);
    expected.set_fragment(None);
    if window.label() != "updater" || window.url().map_err(|e| e.to_string())? != expected {
        return Err("更新只能从应用内置更新窗口操作。".into());
    }
    Ok(())
}

fn busy(status: &Value) -> bool {
    matches!(
        status["phase"].as_str(),
        Some("checking" | "downloading" | "installing")
    )
}

fn status(app: &tauri::AppHandle) -> Result<Value, String> {
    let mut value = app
        .state::<UpdateState>()
        .0
        .lock()
        .map_err(|_| "更新状态不可用")?
        .status
        .clone();
    value["current"] = json!(app.package_info().version.to_string());
    value["channel"] = json!(if app.package_info().version.pre.is_empty() {
        "stable"
    } else {
        "candidate"
    });
    Ok(value)
}

fn fail(app: &tauri::AppHandle, message: impl ToString) {
    if let Ok(mut inner) = app.state::<UpdateState>().0.lock() {
        inner.status["phase"] = json!("error");
        inner.status["error"] = json!(message.to_string());
    }
}

pub(crate) fn installation_in_progress(app: &tauri::AppHandle) -> bool {
    status(app).is_ok_and(|status| status["phase"] == "installing")
}

pub(crate) fn report_install_failure(app: &tauri::AppHandle) {
    fail(app, "更新安装失败，已尝试恢复旧版本。请重新检查更新；绿色版的安装错误记录保留在应用目录的 .desktop-update- 文件夹内。");
    let _ = open(app);
}

pub(crate) fn open(app: &tauri::AppHandle) -> Result<(), String> {
    let window = if let Some(window) = app.get_webview_window("updater") {
        window
    } else {
        tauri::WebviewWindowBuilder::new(app, "updater", WebviewUrl::App("update.html".into()))
            .title(format!("{} · 软件更新", app.package_info().name))
            .inner_size(520.0, 560.0)
            .min_inner_size(360.0, 400.0)
            .build()
            .map_err(|e| e.to_string())?
    };
    window.show().map_err(|e| e.to_string())?;
    window.set_focus().map_err(|e| e.to_string())
}

pub(crate) fn check(app: tauri::AppHandle, manual: bool) {
    {
        let state = app.state::<UpdateState>();
        let Ok(mut inner) = state.0.lock() else {
            return;
        };
        if busy(&inner.status) {
            return;
        }
        inner.status = json!({"phase":"checking"});
        inner.update = None;
    }
    tauri::async_runtime::spawn(async move {
        let result = async {
            let config = app.config().plugins.0.get("updater");
            if !config.is_some_and(|c| {
                c["pubkey"].as_str().is_some_and(|s| !s.is_empty())
                    && c["endpoints"].as_array().is_some_and(|v| !v.is_empty())
            }) {
                return Err("此开发构建未配置更新源。正式发行包由发布流程配置更新源。".to_string());
            }
            let stopping = app.clone();
            let builder = app
                .updater_builder()
                .timeout(Duration::from_secs(60))
                .on_before_exit(move || {
                    crate::local_service::stop(&stopping);
                    stopping.cleanup_before_exit();
                });
            #[cfg(windows)]
            let builder = if tauri::utils::platform::bundle_type().is_none() {
                builder.target(format!("windows-{}-portable", std::env::consts::ARCH))
            } else {
                builder
            };
            builder
                .build()
                .map_err(|e| e.to_string())?
                .check()
                .await
                .map_err(|e| e.to_string())
        }
        .await;
        match result {
            Ok(update) => {
                let available = update.is_some();
                {
                    let state = app.state::<UpdateState>();
                    let Ok(mut inner) = state.0.lock() else {
                        return;
                    };
                    inner.status = match &update {
                        Some(update) => {
                            json!({"phase":"available", "version":update.version, "notes":update.body})
                        }
                        None => json!({"phase":"current"}),
                    };
                    inner.update = update;
                }
                if available || manual {
                    let _ = open(&app);
                }
            }
            Err(error) => {
                fail(&app, error);
                if manual {
                    let _ = open(&app);
                }
            }
        }
    });
}

#[tauri::command]
pub(crate) fn update_status(window: tauri::WebviewWindow) -> Result<Value, String> {
    require_updater(&window)?;
    status(window.app_handle())
}

#[tauri::command]
pub(crate) fn check_update(window: tauri::WebviewWindow) -> Result<(), String> {
    require_updater(&window)?;
    check(window.app_handle().clone(), true);
    Ok(())
}

#[tauri::command]
pub(crate) async fn install_update(window: tauri::WebviewWindow) -> Result<(), String> {
    require_updater(&window)?;
    let app = window.app_handle().clone();
    let update = {
        let state = app.state::<UpdateState>();
        let mut inner = state.0.lock().map_err(|_| "更新状态不可用")?;
        if busy(&inner.status) {
            return Err("更新正在进行中。".into());
        }
        let update = inner.update.take().ok_or("请先检查并选择可用更新。")?;
        inner.status["phase"] = json!("downloading");
        inner.status["downloaded"] = json!(0);
        update
    };
    let progress = app.clone();
    let bytes = update
        .download(
            move |chunk, total| {
                if let Ok(mut inner) = progress.state::<UpdateState>().0.lock() {
                    let downloaded =
                        inner.status["downloaded"].as_u64().unwrap_or(0) + chunk as u64;
                    inner.status["downloaded"] = json!(downloaded);
                    inner.status["total"] = json!(total);
                }
            },
            || {},
        )
        .await;
    let result = match bytes {
        Ok(bytes) => {
            if let Ok(mut inner) = app.state::<UpdateState>().0.lock() {
                inner.status["phase"] = json!("installing");
            }
            let installing = app.clone();
            tauri::async_runtime::spawn_blocking(move || -> Result<(), String> {
                #[cfg(windows)]
                if tauri::utils::platform::bundle_type().is_none() {
                    let staged = crate::portable_update::stage(&bytes)?;
                    crate::local_service::stop(&installing);
                    staged.launch()?;
                    installing.exit(0);
                    return Ok(());
                }
                crate::local_service::stop(&installing);
                update.install(bytes).map_err(|e| e.to_string())?;
                installing.restart();
            })
            .await
            .map_err(|e| e.to_string())
            .and_then(|r| r)
        }
        Err(error) => Err(error.to_string()),
    };
    if let Err(error) = &result {
        fail(&app, error);
    }
    result
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn only_in_flight_states_block_a_new_check_or_install() {
        for phase in ["checking", "downloading", "installing"] {
            assert!(busy(&json!({"phase":phase})));
        }
        for phase in ["idle", "available", "current", "error"] {
            assert!(!busy(&json!({"phase":phase})));
        }
    }
}
