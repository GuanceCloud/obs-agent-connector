param(
  [Parameter(Mandatory = $true)][string]$Endpoint,
  [Parameter(Mandatory = $true)][string]$XToken,
  [string]$Version = "latest",
  [string]$InstallDir = "",
  [string]$ConfigDir = "",
  [switch]$BinaryOnly,
  [switch]$NoPathUpdate
)

$ErrorActionPreference = "Stop"

$BaseUrl = "https://static.guance.com/obs-agent-connector-test"
$TempScript = Join-Path ([System.IO.Path]::GetTempPath()) ("obs-agent-connector-install-" + [System.Guid]::NewGuid().ToString("N") + ".ps1")

try {
  Invoke-WebRequest -Uri "$BaseUrl/install.ps1" -OutFile $TempScript

  $InstallArgs = @{
    DownloadBaseUrl = $BaseUrl
    Endpoint = $Endpoint
    XToken = $XToken
    Version = $Version
  }
  if ($InstallDir) { $InstallArgs.InstallDir = $InstallDir }
  if ($ConfigDir) { $InstallArgs.ConfigDir = $ConfigDir }
  if ($BinaryOnly) { $InstallArgs.BinaryOnly = $true }
  if ($NoPathUpdate) { $InstallArgs.NoPathUpdate = $true }

  & $TempScript @InstallArgs
}
finally {
  if (Test-Path -LiteralPath $TempScript) {
    Remove-Item -LiteralPath $TempScript -Force -ErrorAction SilentlyContinue
  }
}
