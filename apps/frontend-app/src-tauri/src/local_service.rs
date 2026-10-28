use serde_json::{json, Value};
use std::io::{BufRead, BufReader, Write};
use std::process::{Child, ChildStdin, Command, Stdio};
use std::sync::{mpsc, Mutex, OnceLock};
use std::time::{Duration, Instant};
use tauri::Manager;

pub(crate) type LocalState = Mutex<Option<LocalService>>;

pub(crate) struct LocalService {
    child: Child,
    input: Option<ChildStdin>,
    events: mpsc::Receiver<Value>,
    status: Value,
}

impl LocalService {
    fn receive(&mut self) -> Result<Value, String> {
        self.receive_timeout(Duration::from_secs(120))
    }

    fn receive_timeout(&mut self, timeout: Duration) -> Result<Value, String> {
        let event = self.events.recv_timeout(timeout).map_err(|_| {
            "本地服务未能启动，请检查用户数据目录中的 local/service.log。".to_string()
        })?;
        if event["event"] == "error" {
            return Err(event["message"]
                .as_str()
                .unwrap_or("本地服务启动失败")
                .to_string());
        }
        if event["event"] == "ready" {
            let origin = event["origin"].as_str().ok_or("本地服务地址缺失")?;
            let url = tauri::Url::parse(origin).map_err(|error| error.to_string())?;
            if url.scheme() != "http"
                || url.host_str() != Some("127.0.0.1")
                || url.port().is_none()
                || !url.username().is_empty()
                || url.password().is_some()
                || url.path() != "/"
                || url.query().is_some()
                || url.fragment().is_some()
            {
                return Err("本地服务返回了无效地址。".to_string());
            }
        } else if event["event"] != "setup" {
            return Err("本地服务返回了无效状态。".to_string());
        }
        self.status = event.clone();
        Ok(event)
    }
}

impl Drop for LocalService {
    fn drop(&mut self) {
        if let Some(mut input) = self.input.take() {
            let _ = writeln!(input, "{{\"command\":\"stop\"}}");
        }
        let deadline = Instant::now() + Duration::from_secs(45);
        loop {
            match self.child.try_wait() {
                Ok(Some(_)) => return,
                Ok(None) if Instant::now() < deadline => {
                    std::thread::sleep(Duration::from_millis(50));
                }
                _ => break,
            }
        }
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

fn require_chooser(window: &tauri::WebviewWindow) -> Result<(), String> {
    let mut current = window.url().map_err(|error| error.to_string())?;
    current.set_fragment(None);
    let launcher = window.state::<OnceLock<tauri::Url>>();
    if window.label() != "main" || launcher.get() != Some(&current) {
        return Err("本地服务只能从应用内置启动页操作。".to_string());
    }
    Ok(())
}

#[tauri::command]
pub(crate) async fn start_local(window: tauri::WebviewWindow) -> Result<Value, String> {
    require_chooser(&window)?;
    let app = window.app_handle().clone();
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<LocalState>();
        let mut state = state.lock().map_err(|_| "本地服务状态不可用")?;
        if let Some(service) = state.as_mut() {
            if service
                .child
                .try_wait()
                .map_err(|error| error.to_string())?
                .is_none()
            {
                return Ok(service.status.clone());
            }
        }
        *state = None;
        let resources = app
            .path()
            .resource_dir()
            .map_err(|error| error.to_string())?;
        let data = app
            .path()
            .app_local_data_dir()
            .map_err(|error| error.to_string())?
            .join("local");
        // Debug browser acceptance must never initialize a developer's real
        // local account/database. Release builds always use OS app data.
        #[cfg(debug_assertions)]
        let data = std::env::var_os("DESKTOP_TEST_DATA_DIR")
            .map(std::path::PathBuf::from)
            .unwrap_or(data);
        std::fs::create_dir_all(&data).map_err(|error| error.to_string())?;
        let mut log_options = std::fs::OpenOptions::new();
        log_options.create(true).append(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};
            std::fs::set_permissions(&data, std::fs::Permissions::from_mode(0o700))
                .map_err(|error| error.to_string())?;
            log_options.mode(0o600);
        }
        let log = log_options
            .open(data.join("service.log"))
            .map_err(|error| error.to_string())?;
        let binary = resources.join("local").join(if cfg!(windows) {
            "local-service.exe"
        } else {
            "local-service"
        });
        let mut command = Command::new(binary);
        command
            .arg("desktop")
            .arg("--data-dir")
            .arg(&data)
            .arg("--web-dir")
            .arg(resources.join("local/web"))
            .arg("--name")
            .arg(app.package_info().name.clone())
            .current_dir(&data)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::from(log));
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            command.creation_flags(0x08000000); // CREATE_NO_WINDOW
        }
        let mut child = command
            .spawn()
            .map_err(|error| format!("无法启动内置服务：{error}"))?;
        let input = child.stdin.take();
        let stdout = child.stdout.take().ok_or("无法读取本地服务状态")?;
        let (send, events) = mpsc::channel();
        std::thread::spawn(move || {
            for line in BufReader::new(stdout).lines() {
                let Ok(line) = line else { break };
                // Existing application diagnostics may also use stdout.
                if let Ok(event) = serde_json::from_str::<Value>(&line) {
                    if matches!(event["event"].as_str(), Some("setup" | "ready" | "error"))
                        && send.send(event).is_err()
                    {
                        break;
                    }
                }
            }
        });
        let mut service = LocalService {
            child,
            input,
            events,
            status: Value::Null,
        };
        let event = service.receive()?;
        *state = Some(service);
        Ok(event)
    })
    .await
    .map_err(|error| error.to_string())?
}

#[tauri::command]
pub(crate) async fn setup_local(
    window: tauri::WebviewWindow,
    login: String,
    password: String,
) -> Result<Value, String> {
    require_chooser(&window)?;
    let app = window.app_handle().clone();
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<LocalState>();
        let mut state = state.lock().map_err(|_| "本地服务状态不可用")?;
        let service = state.as_mut().ok_or("请先启动本地服务")?;
        if service.status["event"] != "setup" {
            return Err("本地服务已经初始化。".to_string());
        }
        if login.len() > 256 || password.len() > 4096 {
            return Err("账号或密码过长。".to_string());
        }
        let input = service.input.as_mut().ok_or("本地服务已经停止")?;
        serde_json::to_writer(
            &mut *input,
            &json!({"command": "setup", "login": login, "password": password}),
        )
        .map_err(|error| error.to_string())?;
        writeln!(input).map_err(|error| error.to_string())?;
        input.flush().map_err(|error| error.to_string())?;
        finish_setup(&mut state, Duration::from_secs(120))
    })
    .await
    .map_err(|error| error.to_string())?
}

fn finish_setup(state: &mut Option<LocalService>, timeout: Duration) -> Result<Value, String> {
    let result = state
        .as_mut()
        .ok_or("本地服务已经停止")?
        .receive_timeout(timeout);
    if result.is_err() {
        // Setup may have committed just before a timeout. Closing this owned
        // process lets the next explicit start re-read persisted admin state.
        *state = None;
    }
    result
}

pub(crate) fn stop(app: &tauri::AppHandle) {
    if let Ok(mut state) = app.state::<LocalState>().lock() {
        *state = None;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn timed_out_setup_releases_cached_setup_and_owned_process() {
        let mut command = Command::new(std::env::current_exe().unwrap());
        command
            .arg("--list")
            .stdin(Stdio::piped())
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            command.creation_flags(0x08000000);
        }
        let mut child = command.spawn().unwrap();
        let input = child.stdin.take();
        let (_send, events) = mpsc::channel();
        let mut state = Some(LocalService {
            child,
            input,
            events,
            status: json!({"event": "setup"}),
        });
        assert!(finish_setup(&mut state, Duration::ZERO).is_err());
        assert!(
            state.is_none(),
            "retry must start from persisted backend state, not cached setup"
        );
    }
}
