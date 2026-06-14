param(
    [string]$BinaryPath = "",
    [string]$ConfigPath = "",
    [string]$InstallDir = ""
)

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RootDir = Split-Path -Parent $ScriptDir

if ([string]::IsNullOrWhiteSpace($ConfigPath)) {
    $ConfigPath = Join-Path $RootDir "config\config.toml"
}
if ([string]::IsNullOrWhiteSpace($InstallDir)) {
    $InstallDir = Join-Path $env:LOCALAPPDATA "codex-bridge"
}
if ([string]::IsNullOrWhiteSpace($BinaryPath)) {
    $Candidate = Join-Path $RootDir "dist\codex-bridge-windows-amd64.exe"
    if (Test-Path $Candidate) {
        $BinaryPath = $Candidate
    }
}
if (-not (Test-Path $BinaryPath)) {
    throw "codex-bridge binary not found. Pass -BinaryPath or build dist\codex-bridge-windows-amd64.exe."
}

$ConfigPath = (Resolve-Path $ConfigPath).Path
$BinaryPath = (Resolve-Path $BinaryPath).Path
$InstallBinDir = Join-Path $InstallDir "bin"
$InstallBin = Join-Path $InstallBinDir "codex-bridge.exe"
New-Item -ItemType Directory -Force -Path $InstallBinDir | Out-Null
Copy-Item -Force $BinaryPath $InstallBin

& $InstallBin config check --config $ConfigPath
if ($env:CODEX_HOME) {
    & $InstallBin codex configure --config $ConfigPath --codex-home $env:CODEX_HOME
} else {
    & $InstallBin codex configure --config $ConfigPath
}

$TaskName = "codex-bridge"
$Action = New-ScheduledTaskAction -Execute $InstallBin -Argument "--config `"$ConfigPath`""
$Trigger = New-ScheduledTaskTrigger -AtLogOn
$Settings = New-ScheduledTaskSettingsSet -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero)
Register-ScheduledTask -TaskName $TaskName -Action $Action -Trigger $Trigger -Settings $Settings -Description "Codex Bridge user service" -Force | Out-Null
Start-ScheduledTask -TaskName $TaskName

Write-Host "installed Windows startup task: $TaskName"
