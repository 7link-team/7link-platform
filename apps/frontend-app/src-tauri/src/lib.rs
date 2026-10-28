use std::sync::OnceLock;
use tauri::Manager;

mod local_service;
#[cfg(windows)]
mod portable_update;
mod updater;

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .manage(OnceLock::<tauri::Url>::new())
        .manage(local_service::LocalState::new(None))
        .manage(updater::UpdateState::new())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .invoke_handler(tauri::generate_handler![
            local_service::start_local,
            local_service::setup_local,
            updater::update_status,
            updater::check_update,
            updater::install_update
        ])
        .on_page_load(|webview, payload| {
            // The first real navigation is the bundled chooser. During setup,
            // window.url() can still be about:blank on Windows.
            if webview.label() == "main" && payload.url().scheme() != "about" {
                let _ = webview
                    .state::<OnceLock<tauri::Url>>()
                    .set(payload.url().clone());
            }
        })
        .setup(|app| {
            if let Some(window) = app.get_webview_window("main") {
                window.set_title(&app.package_info().name)?;
            }
            #[cfg(desktop)]
            {
                use tauri::menu::{Menu, MenuItem, Submenu};

                let servers = MenuItem::with_id(
                    app,
                    "servers",
                    "服务器 / Servers…",
                    true,
                    Some("Ctrl+Shift+S"),
                )?;
                let menu = Menu::with_items(
                    app,
                    &[
                        &Submenu::with_items(app, "连接 / Connection", true, &[&servers])?,
                        &Submenu::with_items(
                            app,
                            "帮助 / Help",
                            true,
                            &[&MenuItem::with_id(
                                app,
                                "updates",
                                "检查更新 / Check for updates…",
                                true,
                                None::<&str>,
                            )?],
                        )?,
                    ],
                )?;
                app.set_menu(menu)?;
                app.on_menu_event(|app, event| {
                    if event.id().as_ref() == "servers" {
                        if let (Some(window), Some(launcher)) = (
                            app.get_webview_window("main"),
                            app.state::<OnceLock<tauri::Url>>().get(),
                        ) {
                            let mut chooser = launcher.clone();
                            chooser.set_fragment(Some("choose"));
                            if let Err(error) = window.navigate(chooser) {
                                eprintln!("Cannot open server selection: {error}");
                            }
                        }
                    } else if event.id().as_ref() == "updates" {
                        if let Err(error) = updater::open(app) {
                            eprintln!("Cannot open updater: {error}");
                        }
                        updater::check(app.clone(), true);
                    }
                });
            }
            let handle = app.handle().clone();
            if std::env::args().any(|argument| argument == "--desktop-update-failed") {
                updater::report_install_failure(&handle);
            } else {
                updater::check(handle, false);
            }
            Ok(())
        })
        .on_window_event(|window, event| {
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                if window.label() == "updater" {
                    api.prevent_close();
                    let _ = window.hide();
                } else if window.label() == "main" {
                    if updater::installation_in_progress(window.app_handle()) {
                        api.prevent_close();
                        let _ = updater::open(window.app_handle());
                    } else {
                        window.app_handle().exit(0);
                    }
                }
            }
        })
        .build(tauri::generate_context!())
        .expect("error while building tauri application")
        .run(|app, event| {
            if matches!(event, tauri::RunEvent::Exit) {
                local_service::stop(app);
            }
        });
}
