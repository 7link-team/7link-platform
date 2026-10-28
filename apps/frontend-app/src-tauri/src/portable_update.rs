use serde_json::json;
use std::io::{Cursor, Write};
use std::path::{Component, Path};
use std::process::{Command, Stdio};

pub(crate) struct StagedUpdate(tempfile::TempDir);

fn extract(bytes: &[u8], destination: &Path, executable: &str) -> Result<(), String> {
    let mut archive = zip::ZipArchive::new(Cursor::new(bytes)).map_err(|e| e.to_string())?;
    for index in 0..archive.len() {
        let mut entry = archive.by_index(index).map_err(|e| e.to_string())?;
        let path = entry.enclosed_name().ok_or("更新压缩包包含无效路径")?;
        if entry.is_symlink()
            || path
                .components()
                .any(|c| !matches!(c, Component::Normal(_)))
            || entry
                .name()
                .map_err(|e| e.to_string())?
                .contains(['\\', ':'])
        {
            return Err("更新压缩包包含不支持的路径或链接".into());
        }
        if path != Path::new(executable) && !path.starts_with("local") {
            return Err("更新压缩包包含非应用文件".into());
        }
        let target = destination.join(path);
        if entry.is_dir() {
            std::fs::create_dir_all(target).map_err(|e| e.to_string())?;
        } else {
            if let Some(parent) = target.parent() {
                std::fs::create_dir_all(parent).map_err(|e| e.to_string())?;
            }
            let mut file = std::fs::File::create(target).map_err(|e| e.to_string())?;
            std::io::copy(&mut entry, &mut file).map_err(|e| e.to_string())?;
            file.sync_all().map_err(|e| e.to_string())?;
        }
    }
    for file in [
        executable,
        "local/local-service.exe",
        "local/web/index.html",
    ] {
        if !destination.join(file).is_file() {
            return Err("更新压缩包缺少完整应用文件".into());
        }
    }
    Ok(())
}

pub(crate) fn stage(bytes: &[u8]) -> Result<StagedUpdate, String> {
    let executable = std::env::current_exe().map_err(|e| e.to_string())?;
    let parent = executable.parent().ok_or("应用目录无效")?;
    let name = executable
        .file_name()
        .and_then(|s| s.to_str())
        .ok_or("应用文件名无效")?;
    let directory = tempfile::Builder::new()
        .prefix(".desktop-update-")
        .tempdir_in(parent)
        .map_err(|e| format!("无法写入应用目录，请将绿色版解压到有写入权限的目录：{e}"))?;
    extract(bytes, &directory.path().join("payload"), name)?;
    std::fs::write(
        directory.path().join("apply.ps1"),
        include_str!("portable_update.ps1"),
    )
    .map_err(|e| e.to_string())?;
    let config =
        json!({"pid": std::process::id(), "executable": executable, "stage": directory.path()});
    let mut file =
        std::fs::File::create(directory.path().join("apply.json")).map_err(|e| e.to_string())?;
    file.write_all(config.to_string().as_bytes())
        .map_err(|e| e.to_string())?;
    file.sync_all().map_err(|e| e.to_string())?;
    Ok(StagedUpdate(directory))
}

impl StagedUpdate {
    pub(crate) fn launch(self) -> Result<(), String> {
        use std::os::windows::process::CommandExt;
        let powershell = std::env::var_os("SystemRoot")
            .map(std::path::PathBuf::from)
            .ok_or("系统目录不可用")?
            .join("System32/WindowsPowerShell/v1.0/powershell.exe");
        Command::new(powershell)
            .args([
                "-NoProfile",
                "-NonInteractive",
                "-ExecutionPolicy",
                "Bypass",
                "-File",
            ])
            .arg(self.0.path().join("apply.ps1"))
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .creation_flags(0x08000000)
            .spawn()
            .map_err(|e| e.to_string())?;
        let _ = self.0.keep(); // The helper owns cleanup after the parent exits.
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn archive(paths: &[&str]) -> Vec<u8> {
        let mut writer = zip::ZipWriter::new(Cursor::new(Vec::new()));
        for path in paths {
            writer
                .start_file(*path, zip::write::SimpleFileOptions::default())
                .unwrap();
            writer.write_all(b"application").unwrap();
        }
        writer.finish().unwrap().into_inner()
    }
    #[test]
    fn portable_archive_requires_complete_application_and_cannot_write_outside_its_stage() {
        let dir = tempfile::tempdir().unwrap();
        let files = [
            "desktop.exe",
            "local/local-service.exe",
            "local/web/index.html",
        ];
        extract(&archive(&files), dir.path(), "desktop.exe").unwrap();
        for path in [
            "../escape.exe",
            "C:/escape.exe",
            "local/file:stream",
            "other.exe",
            "local\\escape",
        ] {
            assert!(
                extract(&archive(&[path]), dir.path(), "desktop.exe").is_err(),
                "{path}"
            );
        }
        assert!(extract(
            &archive(&["desktop.exe"]),
            dir.path().join("incomplete").as_path(),
            "desktop.exe"
        )
        .is_err());
    }
}
