$ErrorActionPreference = 'Stop'
$config = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'apply.json') -Raw | ConvertFrom-Json
$stage = [IO.Path]::GetFullPath($config.stage)
$executable = [IO.Path]::GetFullPath($config.executable)
$root = [IO.Path]::GetDirectoryName($executable)
$name = [IO.Path]::GetFileName($executable)
if ($stage -ne [IO.Path]::GetFullPath($PSScriptRoot) -or [IO.Path]::GetDirectoryName($stage) -ne $root -or -not [IO.Path]::GetFileName($stage).StartsWith('.desktop-update-')) { throw 'Invalid update staging directory' }
$payload = Join-Path $stage 'payload'
$backup = Join-Path $stage 'backup'
$old = @()
$new = @()
$success = $false
try {
    $parent = Get-Process -Id $config.pid -ErrorAction SilentlyContinue
    if ($parent) {
        if ($parent.Path -ne $executable) { throw 'Update parent executable changed' }
        if (-not $parent.WaitForExit(120000)) { throw 'Application did not exit for update' }
    }
    foreach ($relative in @($name, 'local/local-service.exe', 'local/web/index.html')) {
        if (-not (Test-Path -LiteralPath (Join-Path $payload $relative) -PathType Leaf)) { throw 'Incomplete application update' }
    }
    New-Item -ItemType Directory -Path $backup | Out-Null
    foreach ($relative in @($name, 'local')) {
        $target = Join-Path $root $relative
        if (Test-Path -LiteralPath $target) { Move-Item -LiteralPath $target -Destination (Join-Path $backup $relative); $old += $relative }
        Move-Item -LiteralPath (Join-Path $payload $relative) -Destination $target
        $new += $relative
    }
    Start-Process -FilePath $executable -WorkingDirectory $root
    $success = $true
} catch {
    $_ | Out-String | Set-Content -LiteralPath (Join-Path $stage 'error.log')
    foreach ($relative in $new) { Remove-Item -LiteralPath (Join-Path $root $relative) -Recurse -Force }
    foreach ($relative in $old) { Move-Item -LiteralPath (Join-Path $backup $relative) -Destination (Join-Path $root $relative) }
}
if ($success) {
    # Only this verified, uniquely created staging tree is removed.
    Remove-Item -LiteralPath $stage -Recurse -Force
} else {
    Start-Process -FilePath $executable -WorkingDirectory $root -ArgumentList '--desktop-update-failed'
}
