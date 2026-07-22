param(
  [string]$Version = "latest",
  [string]$InstallDir = "",
  [string]$ConfigDir = "",
  [string]$DownloadBaseUrl = "",
  [string]$PluginSource = "",
  [string]$PluginBaseUrl = "",
  [string]$Endpoint = "",
  [string]$XToken = "",
  [switch]$BinaryOnly,
  [switch]$NoPathUpdate
)

$ErrorActionPreference = "Stop"

$AppName = "obs-agent-connector"
$ScriptPath = $MyInvocation.MyCommand.Path
$EndpointWasProvided = [bool]$Endpoint

if (-not $ConfigDir) {
  $ConfigDir = Join-Path $HOME ".obs-agent-connector"
}
$ConfigPath = Join-Path $ConfigDir "config.json"
$ExistingConfig = $null
if (Test-Path -LiteralPath $ConfigPath) {
  $ExistingContent = Get-Content -LiteralPath $ConfigPath -Raw
  if ($ExistingContent) {
    $ExistingConfig = $ExistingContent | ConvertFrom-Json
  }
}

if (-not $DownloadBaseUrl) {
  $DownloadBaseUrl = $env:DOWNLOAD_BASE_URL
}
if (-not $DownloadBaseUrl) {
  $DownloadBaseUrl = $env:OBS_AGENT_CONNECTOR_OSS_ENDPOINT
}
if ((-not $Endpoint) -and $ExistingConfig -and $ExistingConfig.endpoint) {
  $Endpoint = [string]$ExistingConfig.endpoint
}
if ((-not $XToken) -and $ExistingConfig -and $ExistingConfig.x_token) {
  $XToken = [string]$ExistingConfig.x_token
}
if ((-not $PluginSource) -and $ExistingConfig -and $ExistingConfig.plugin_source) {
  $PluginSource = [string]$ExistingConfig.plugin_source
}
if ((-not $PluginBaseUrl) -and $ExistingConfig -and $ExistingConfig.plugin_base_url) {
  $PluginBaseUrl = [string]$ExistingConfig.plugin_base_url
}

function Get-DownloadBaseFromEndpoint {
  param([string]$Value)

  try {
    $Uri = [System.Uri]$Value
  }
  catch {
    return ""
  }
  $Parts = $Uri.DnsSafeHost.TrimEnd(".").Split(".")
  if ($Parts.Count -lt 2) {
    return ""
  }
  $RootDomain = "$($Parts[$Parts.Count - 2]).$($Parts[$Parts.Count - 1])"
  return "https://static.$RootDomain/obs-agent-connector"
}

function Get-PluginBaseFromDownloadBase {
  param([string]$Value)

  if (-not $Value) {
    return ""
  }
  $Trimmed = $Value.TrimEnd("/")
  $SlashIndex = $Trimmed.LastIndexOf("/")
  if ($SlashIndex -lt 0) {
    return $Trimmed
  }
  return $Trimmed.Substring(0, $SlashIndex)
}

if (-not $DownloadBaseUrl) {
  if ($EndpointWasProvided) {
    $DownloadBaseUrl = Get-DownloadBaseFromEndpoint -Value $Endpoint
  }
  elseif ($ExistingConfig -and $ExistingConfig.download_base_url) {
    $DownloadBaseUrl = [string]$ExistingConfig.download_base_url
  }
  else {
    $DownloadBaseUrl = Get-DownloadBaseFromEndpoint -Value $Endpoint
  }
}
if (-not $PluginSource) {
  $PluginSource = "oss"
}
if ((-not $PluginBaseUrl) -and ($PluginSource -eq "oss")) {
  $PluginBaseUrl = Get-PluginBaseFromDownloadBase -Value $DownloadBaseUrl
}
if (-not $DownloadBaseUrl) {
  throw "download_base_url is required; pass -DownloadBaseUrl <url> or set DOWNLOAD_BASE_URL / OBS_AGENT_CONNECTOR_OSS_ENDPOINT"
}
if (($PluginSource -eq "github") -and (-not $PluginBaseUrl)) {
  throw "plugin_base_url is required when plugin_source=github; pass -PluginBaseUrl <url> or update config.json"
}
if ((-not $BinaryOnly) -and (-not $Endpoint)) {
  throw "endpoint is required; pass -Endpoint <url> on first install or keep it in config.json"
}
if ((-not $BinaryOnly) -and (-not $XToken)) {
  throw "x-token is required; pass -XToken <token> on first install or keep it in config.json"
}
$DownloadBaseUrl = $DownloadBaseUrl.TrimEnd("/")
$PluginBaseUrl = $PluginBaseUrl.TrimEnd("/")

if (-not $InstallDir) {
  $InstallDir = Join-Path ([Environment]::GetFolderPath("LocalApplicationData")) "Programs\obs-agent-connector\bin"
}

function Get-LatestVersion {
  $latestUrl = "$DownloadBaseUrl/latest.txt?v=$([DateTimeOffset]::UtcNow.ToUnixTimeSeconds())"
  return (Invoke-RestMethod -Uri $latestUrl).ToString().Trim()
}

function Add-CacheKey {
  param(
    [string]$Url,
    [string]$Key
  )

  $Uri = [System.Uri]$Url
  if (($Uri.Scheme -ne "http") -and ($Uri.Scheme -ne "https")) {
    return $Url
  }
  return "${Url}?v=$([System.Uri]::EscapeDataString($Key))"
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
$DownloadUrl = Add-CacheKey -Url "$DownloadBaseUrl/$AssetName" -Key $Version
$ChecksumsUrl = Add-CacheKey -Url "$DownloadBaseUrl/SHA256SUMS" -Key $Version
$TempDir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString("N"))

try {
  New-Item -ItemType Directory -Path $TempDir | Out-Null
  New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
  New-Item -ItemType Directory -Force -Path $ConfigDir | Out-Null

  $ZipPath = Join-Path $TempDir $AssetName
  $ChecksumsPath = Join-Path $TempDir "SHA256SUMS"
  Invoke-WebRequest -Uri $DownloadUrl -OutFile $ZipPath
  Invoke-WebRequest -Uri $ChecksumsUrl -OutFile $ChecksumsPath

  $ExpectedHash = $null
  foreach ($Line in Get-Content -LiteralPath $ChecksumsPath) {
    $Parts = $Line.Trim() -split "\s+"
    if (($Parts.Count -ge 2) -and ($Parts[1].TrimStart("*") -eq $AssetName)) {
      $ExpectedHash = $Parts[0].ToLowerInvariant()
      break
    }
  }
  if (-not $ExpectedHash) {
    throw "Checksum entry not found for $AssetName"
  }
  $ActualHash = (Get-FileHash -LiteralPath $ZipPath -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($ActualHash -ne $ExpectedHash) {
    throw "Checksum verification failed for $AssetName"
  }
  Write-Host "Verified SHA-256 for $AssetName"

  Expand-Archive -LiteralPath $ZipPath -DestinationPath $TempDir -Force
  Copy-Item -LiteralPath (Join-Path $TempDir $BinaryName) -Destination (Join-Path $InstallDir "$AppName.exe") -Force

  if (-not $BinaryOnly) {
    $ConfigJson = [ordered]@{
      download_base_url = $DownloadBaseUrl
      plugin_source = $PluginSource
      plugin_base_url = $PluginBaseUrl
      endpoint = $Endpoint
      x_token = $XToken
    } | ConvertTo-Json
    [System.IO.File]::WriteAllText($ConfigPath, $ConfigJson, (New-Object System.Text.UTF8Encoding($false)))
  }

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
  if (-not $BinaryOnly) {
    Write-Host "Wrote config to $ConfigPath"
  }
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
