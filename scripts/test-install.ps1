$ErrorActionPreference = "Stop"

. (Join-Path $PSScriptRoot "install.ps1")

function Assert-ThrowsWithMessage {
  param(
    [scriptblock]$Action,
    [string]$ExpectedMessage
  )

  try {
    & $Action
  }
  catch {
    if ($_.Exception.Message -notlike "*$ExpectedMessage*") {
      throw "Expected error containing '$ExpectedMessage', got '$($_.Exception.Message)'"
    }
    return
  }
  throw "Expected action to fail with an error containing '$ExpectedMessage'"
}

$UnicodeUserName = -join [char[]](0x6D4B, 0x8BD5, 0x7528, 0x6237)

Assert-TagAssignments -Assignments @(
  "user_id=user-redacted",
  "user_name=$UnicodeUserName",
  "env=prod"
)
Assert-TagAssignments -Assignments @("regions=us-east,us-west")

Assert-ThrowsWithMessage {
  Assert-TagAssignments -Assignments @("user_id=user-redacted,user_name=$UnicodeUserName,env=prod")
} "comma-separated tag assignments are ambiguous"

Assert-ThrowsWithMessage {
  Assert-TagAssignments -Assignments @("missing-value-separator")
} "tag must use KEY=VALUE format"

foreach ($Value in @($null, "", "   ", "relative/path", "//example.com/path", "https://", "https://bad host/path", "file:///tmp/example.com", "ftp://example.com", "mailto:user@example.com", "http://localhost", "http://127.0.0.1", "http://[::1]")) {
  $Actual = Get-DownloadBaseFromEndpoint -Value $Value
  if ($Actual -ne "") {
    throw "Expected no derived download base for '$Value', got '$Actual'"
  }
}

foreach ($Value in @("https://llm-openway.guance.com", "http://llm-openway.guance.com:8080/path", "https://llm-openway.guance.com./path")) {
  $Actual = Get-DownloadBaseFromEndpoint -Value $Value
  if ($Actual -ne "https://static.guance.com/obs-agent-connector") {
    throw "Unexpected derived download base for '$Value': '$Actual'"
  }
}

# Reproduce BinaryOnly RC installation without Endpoint or PluginBaseUrl.
# Evaluate only the installer's actual URL-resolution block: no network or writes.
$InstallerText = Get-Content -LiteralPath (Join-Path $PSScriptRoot "install.ps1") -Raw
$Start = $InstallerText.IndexOf('if (-not $DownloadBaseUrl) {', $InstallerText.IndexOf('function Get-NormalizedOssPluginBase'))
$End = $InstallerText.IndexOf('Assert-TagAssignments -Assignments $Tag', $Start)
if (($Start -lt 0) -or ($End -le $Start)) {
  throw "Installer URL-resolution block not found"
}
& {
  $DownloadBaseUrl = "https://github.com/GuanceCloud/obs-agent-connector/releases/download/v0.1.31-rc1"
  $Endpoint = ""
  $EndpointWasProvided = $false
  $PluginSource = ""
  $PluginBaseUrl = ""
  . ([scriptblock]::Create($InstallerText.Substring($Start, $End - $Start)))
  if ($DownloadBaseUrl -ne "https://github.com/GuanceCloud/obs-agent-connector/releases/download/v0.1.31-rc1") {
    throw "Explicit RC download base changed"
  }
  if (($PluginSource -ne "oss") -or ($PluginBaseUrl -ne "https://static.guance.com/agent_plugins")) {
    throw "Expected default OSS plugin fallback without an endpoint"
  }
}

Write-Host "PowerShell installer contract tests passed."
