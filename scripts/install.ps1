param(
  [string]$Version = "latest",
  [string]$InstallDir = "",
  [string]$ConfigDir = "",
  [string]$DownloadBaseUrl = "",
  [switch]$NoPathUpdate
)

$ErrorActionPreference = "Stop"

$AppName = "obs-agent-connector"
$ScriptPath = $MyInvocation.MyCommand.Path

if (-not $DownloadBaseUrl) {
  $DownloadBaseUrl = $env:DOWNLOAD_BASE_URL
}
if (-not $DownloadBaseUrl) {
  $DownloadBaseUrl = $env:OBS_AGENT_CONNECTOR_OSS_ENDPOINT
}
if (-not $DownloadBaseUrl) {
  throw "download_base_url is required; pass -DownloadBaseUrl <url> or set DOWNLOAD_BASE_URL / OBS_AGENT_CONNECTOR_OSS_ENDPOINT"
}
$DownloadBaseUrl = $DownloadBaseUrl.TrimEnd("/")

if (-not $InstallDir) {
  $InstallDir = Join-Path ([Environment]::GetFolderPath("LocalApplicationData")) "Programs\obs-agent-connector\bin"
}
if (-not $ConfigDir) {
  $ConfigDir = Join-Path $HOME ".obs-agent-connector"
}

function Get-LatestVersion {
  $latestUrl = "$DownloadBaseUrl/latest.txt"
  return (Invoke-RestMethod -Uri $latestUrl).ToString().Trim()
}

if ($Version -eq "latest") {
  $Version = Get-LatestVersion
  if (-not $Version) {
    throw "Failed to resolve latest version from $DownloadBaseUrl/latest.txt"
  }
}

$processorArchitecture = $env:PROCESSOR_ARCHITECTURE
if ($env:PROCESSOR_ARCHITEW6432) {
  $processorArchitecture = $env:PROCESSOR_ARCHITEW6432
}

switch ($processorArchitecture.ToUpperInvariant()) {
  "AMD64" { $GoArch = "amd64" }
  "ARM64" { $GoArch = "arm64" }
  default { throw "Unsupported architecture: $processorArchitecture" }
}

$AssetBaseName = "$AppName-windows-$GoArch"
$AssetName = "$AssetBaseName.zip"
$BinaryName = "$AssetBaseName.exe"
$DownloadUrl = "$DownloadBaseUrl/$AssetName"
$ConfigPath = Join-Path $ConfigDir "config.json"
$TempDir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString("N"))

try {
  New-Item -ItemType Directory -Path $TempDir | Out-Null
  New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
  New-Item -ItemType Directory -Force -Path $ConfigDir | Out-Null

  $ZipPath = Join-Path $TempDir $AssetName
  Invoke-WebRequest -Uri $DownloadUrl -OutFile $ZipPath
  Expand-Archive -LiteralPath $ZipPath -DestinationPath $TempDir -Force
  Copy-Item -LiteralPath (Join-Path $TempDir $BinaryName) -Destination (Join-Path $InstallDir "$AppName.exe") -Force

  @{ download_base_url = $DownloadBaseUrl } | ConvertTo-Json | Set-Content -LiteralPath $ConfigPath -Encoding UTF8

  if (-not $NoPathUpdate) {
    $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $PathItems = @()
    if ($UserPath) {
      $PathItems = $UserPath -split ";"
    }
    if ($PathItems -notcontains $InstallDir) {
      $NextUserPath = if ($UserPath) { "$UserPath;$InstallDir" } else { $InstallDir }
      [Environment]::SetEnvironmentVariable("Path", $NextUserPath, "User")
    }
    $CurrentPathItems = $env:Path -split ";"
    if ($CurrentPathItems -notcontains $InstallDir) {
      $env:Path = "$InstallDir;$env:Path"
    }
  }

  Write-Host "Installed $AppName $Version to $(Join-Path $InstallDir "$AppName.exe")"
  Write-Host "Wrote config to $ConfigPath"
  if ($NoPathUpdate) {
    Write-Host "PATH update skipped."
  }
}
finally {
  if (Test-Path -LiteralPath $TempDir) {
    Remove-Item -LiteralPath $TempDir -Recurse -Force
  }
}

if ($ScriptPath -and (Test-Path -LiteralPath $ScriptPath)) {
  Start-Process -FilePath "cmd.exe" -ArgumentList "/c ping 127.0.0.1 -n 2 > nul & del /f /q `"$ScriptPath`"" -WindowStyle Hidden | Out-Null
}
