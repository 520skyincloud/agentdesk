$ErrorActionPreference = "Stop"

$RootDir = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$EnvFile = Join-Path $RootDir ".wecom-hook-bridge.env"

if (Test-Path $EnvFile) {
  Get-Content $EnvFile | ForEach-Object {
    $line = $_.Trim()
    if ($line -eq "" -or $line.StartsWith("#")) {
      return
    }
    $parts = $line.Split("=", 2)
    if ($parts.Length -eq 2) {
      [Environment]::SetEnvironmentVariable($parts[0].Trim(), $parts[1].Trim(), "Process")
    }
  }
}

if (-not $env:AGENT_DESK_BASE_URL) {
  $env:AGENT_DESK_BASE_URL = "http://127.0.0.1:8083"
}
if (-not $env:WECOM_HOOK_API_URL) {
  $env:WECOM_HOOK_API_URL = "http://127.0.0.1:8060/"
}
if (-not $env:WECOM_HOOK_WS_URL) {
  $env:WECOM_HOOK_WS_URL = "ws://127.0.0.1:8061/message/"
}
if (-not $env:POLL_INTERVAL_MS) {
  $env:POLL_INTERVAL_MS = "3000"
}

if (-not $env:AGENT_DESK_CHANNEL_ID -or -not $env:AGENT_DESK_BRIDGE_TOKEN) {
  Write-Error "Missing AGENT_DESK_CHANNEL_ID or AGENT_DESK_BRIDGE_TOKEN. Create .wecom-hook-bridge.env from .wecom-hook-bridge.env.example first."
}

Set-Location $RootDir
node scripts/wecom-hook-bridge.mjs
