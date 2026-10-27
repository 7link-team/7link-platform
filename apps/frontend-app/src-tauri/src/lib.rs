use std::sync::OnceLock;
use tauri::Manager;

mod local_service;

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .manage(OnceLock::<tauri::Url>::new())
        .manage(local_service::LocalState::new(None))
        .invoke_handler(tauri::generate_handler![
            local_service::start_local,
            local_service::setup_local
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
                    &[&Submenu::with_items(
                        app,
                        "连接 / Connection",
                        true,
                        &[&servers],
                    )?],
                )?;
                app.set_menu(menu)?;
                app.on_menu_event(|app, event| {
                    if event.id().as_ref() == "servers" {
                        if let (Some(window), Some(launcher)) = (
                            app.get_webview_window("main"),
                            app.state::<OnceLock<tauri::Url>>().get(),
                        ) {
                            if let Err(error) = window.navigate(launcher.clone()) {
                                eprintln!("Cannot open server selection: {error}");
                            }
                        }
                    }
                });
            }
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building tauri application")
        .run(|app, event| {
            if matches!(event, tauri::RunEvent::Exit) {
                local_service::stop(app);
            }
        });
}
