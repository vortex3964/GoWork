# GoWork installer and updater for Windows.
#
# Installs a release under %LOCALAPPDATA%\Programs\GoWork, verifies the
# downloaded archive against checksums.txt, and adds the install directory to
# the user PATH. Use -Update to replace an existing install and -Uninstall to
# remove it.

[CmdletBinding()]
param(
    [switch]$Update,
    [switch]$Uninstall,
    [string]$Version = "latest",
    [string]$Dir,
    [switch]$NoModifyPath,
    [switch]$Help
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
}
catch {
    # Older .NET builds may not expose Tls12; Invoke-WebRequest will report
    # the connection error if the endpoint still requires it.
}

$Repo = "vortex3964/GoWork"
$ReleasesUrl = "https://github.com/$Repo/releases"
$ReleasesApi = "https://api.github.com/repos/$Repo/releases/latest"
$script:TempDir = $null

function Show-Usage {
    @"
GoWork installer

Usage:
  .\install.ps1 [options]

Options:
  -Update              Replace an existing installation (latest or -Version)
  -Uninstall           Remove GoWork and its user PATH entry
  -Version <tag>       Release tag to install (default: latest)
  -Dir <path>          Install directory
  -NoModifyPath        Do not add the install directory to the user PATH
  -Help                Show this help

Defaults:
  install directory    %LOCALAPPDATA%\Programs\GoWork

Examples:
  .\install.ps1
  .\install.ps1 -Update
  .\install.ps1 -Uninstall
  .\install.ps1 -Version v1.2.3 -Dir C:\Tools\GoWork
"@ | Write-Host
}

function Cleanup {
    if ($script:TempDir -and (Test-Path -LiteralPath $script:TempDir)) {
        Remove-Item -LiteralPath $script:TempDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}

trap {
    Cleanup
    Write-Host ("error: " + $_.Exception.Message) -ForegroundColor Red
    exit 1
}

function Get-GoArch {
    $arch = $env:PROCESSOR_ARCHITECTURE
    if ([string]::IsNullOrWhiteSpace($arch)) {
        throw "could not determine the processor architecture"
    }
    if ($arch -eq "x86" -and -not [string]::IsNullOrWhiteSpace($env:PROCESSOR_ARCHITEW6432)) {
        $arch = $env:PROCESSOR_ARCHITEW6432
    }
    switch ($arch.ToUpperInvariant()) {
        "AMD64" { return "amd64" }
        "ARM64" { return "arm64" }
        default { throw "unsupported architecture: $arch (GoWork provides amd64 and arm64 builds)" }
    }
}

function Resolve-Directory([string]$Path) {
    if ([string]::IsNullOrWhiteSpace($Path)) {
        return $null
    }
    if ($Path -eq "~") {
        $Path = $HOME
    }
    elseif ($Path.StartsWith("~/") -or $Path.StartsWith("~\")) {
        $Path = Join-Path $HOME $Path.Substring(2)
    }
    return [System.IO.Path]::GetFullPath($Path)
}

function Download-File([string]$Url, [string]$Destination) {
    $ProgressPreference = "SilentlyContinue"
    Invoke-WebRequest -Uri $Url -OutFile $Destination -UseBasicParsing
}

function Get-LatestTag {
    try {
        $ProgressPreference = "SilentlyContinue"
        $headers = @{ "User-Agent" = "GoWork-Installer"; "Accept" = "application/vnd.github+json" }
        $release = Invoke-RestMethod -Uri $ReleasesApi -Headers $headers -UseBasicParsing
        if ($release -and $release.tag_name) {
            return [string]$release.tag_name
        }
    }
    catch {
        return $null
    }
    return $null
}

function Get-ExpectedChecksum([string]$ChecksFile, [string]$Asset) {
    foreach ($line in Get-Content -LiteralPath $ChecksFile) {
        if ($line -match '^\s*([0-9a-fA-F]{64})\s+\*?(.+?)\s*$') {
            if ($Matches[2] -eq $Asset) {
                return $Matches[1].ToLowerInvariant()
            }
        }
    }
    return $null
}

function Get-InstalledVersion([string]$InstallDirectory) {
    $exe = Join-Path $InstallDirectory "gowork.exe"
    if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) {
        return $null
    }
    try {
        $output = & $exe --version 2>$null
        if ($LASTEXITCODE -ne 0) {
            return $null
        }
        $lines = @($output)
        if ($lines.Count -eq 0) {
            return $null
        }
        $parts = ([string]$lines[0]).Trim() -split '\s+'
        if ($parts.Count -eq 0) {
            return $null
        }
        return [string]$parts[$parts.Count - 1]
    }
    catch {
        return $null
    }
}

function Get-UserPath {
    $value = [Environment]::GetEnvironmentVariable("Path", [EnvironmentVariableTarget]::User)
    if ($null -eq $value) {
        return ""
    }
    return [string]$value
}

function Add-ToUserPath([string]$Directory) {
    $current = Get-UserPath
    $entries = @($current -split ';' | Where-Object { $_ -ne "" })
    foreach ($entry in $entries) {
        if ([string]::Equals($entry.TrimEnd('\'), $Directory.TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase)) {
            Write-Host "$Directory is already on the user PATH."
            return $false
        }
    }

    if ([string]::IsNullOrWhiteSpace($current)) {
        $updated = $Directory
    }
    else {
        $updated = $current.TrimEnd(';') + ";" + $Directory
    }
    [Environment]::SetEnvironmentVariable("Path", $updated, [EnvironmentVariableTarget]::User)
    Write-Host "Added $Directory to the user PATH."
    Write-Host "Open a new terminal for the PATH change to take effect."
    return $true
}

function Remove-FromUserPath([string]$Directory) {
    $current = Get-UserPath
    if ([string]::IsNullOrWhiteSpace($current)) {
        return $false
    }
    $entries = @($current -split ';' | Where-Object { $_ -ne "" })
    $kept = @($entries | Where-Object {
        -not [string]::Equals($_.TrimEnd('\'), $Directory.TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase)
    })
    if ($kept.Count -eq $entries.Count) {
        return $false
    }

    [Environment]::SetEnvironmentVariable("Path", ($kept -join ';'), [EnvironmentVariableTarget]::User)
    Write-Host "Removed $Directory from the user PATH."
    return $true
}

function Install-Bundle([string]$Source, [string]$Destination) {
    $parent = Split-Path -Parent $Destination
    if (-not (Test-Path -LiteralPath $parent)) {
        New-Item -ItemType Directory -Path $parent -Force | Out-Null
    }

    $backup = $null
    if (Test-Path -LiteralPath $Destination) {
        $backup = $Destination + ".old." + $PID
        if (Test-Path -LiteralPath $backup) {
            Remove-Item -LiteralPath $backup -Recurse -Force
        }
        Move-Item -LiteralPath $Destination -Destination $backup
    }

    try {
        New-Item -ItemType Directory -Path $Destination -Force | Out-Null
        Get-ChildItem -LiteralPath $Source -Force | Copy-Item -Destination $Destination -Recurse -Force
    }
    catch {
        if ($backup -and (Test-Path -LiteralPath $backup)) {
            if (Test-Path -LiteralPath $Destination) {
                Remove-Item -LiteralPath $Destination -Recurse -Force
            }
            Move-Item -LiteralPath $backup -Destination $Destination
        }
        elseif (Test-Path -LiteralPath $Destination) {
            Remove-Item -LiteralPath $Destination -Recurse -Force
        }
        throw
    }

    if ($backup -and (Test-Path -LiteralPath $backup)) {
        Remove-Item -LiteralPath $backup -Recurse -Force
    }
}

function Install-Release([string]$PlatformArch, [string]$InstallDirectory) {
    $asset = "gowork_windows_$PlatformArch.zip"
    if ($Version -eq "latest") {
        $baseUrl = "$ReleasesUrl/latest/download"
        $label = "latest"
    }
    else {
        $baseUrl = "$ReleasesUrl/download/$Version"
        $label = $Version
    }

    $script:TempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("gowork-install-" + [Guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Path $script:TempDir -Force | Out-Null
    $archive = Join-Path $script:TempDir $asset
    $checksums = Join-Path $script:TempDir "checksums.txt"
    $extract = Join-Path $script:TempDir "extract"

    Write-Host "Downloading GoWork ($label) for windows/$PlatformArch..."
    Download-File "$baseUrl/$asset" $archive

    Write-Host "Downloading checksums..."
    Download-File "$baseUrl/checksums.txt" $checksums

    $expected = Get-ExpectedChecksum $checksums $asset
    if (-not $expected) {
        throw "checksums.txt does not contain an entry for $asset"
    }
    $actual = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $expected) {
        throw "sha256 mismatch for ${asset}: expected $expected, got $actual"
    }
    Write-Host "Checksum verified."

    New-Item -ItemType Directory -Path $extract -Force | Out-Null
    Expand-Archive -LiteralPath $archive -DestinationPath $extract -Force
    $binary = Join-Path $extract "gowork.exe"
    if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) {
        throw "release archive did not contain gowork.exe"
    }

    Write-Host "Installing GoWork to $InstallDirectory..."
    Install-Bundle $extract $InstallDirectory
    Write-Host "Installed to $InstallDirectory"

    if ($NoModifyPath) {
        Write-Host "Skipping PATH changes (-NoModifyPath)."
    }
    else {
        Add-ToUserPath $InstallDirectory | Out-Null
    }

    $installed = Get-InstalledVersion $InstallDirectory
    Write-Host ""
    Write-Host "GoWork is ready."
    if ($installed) {
        Write-Host "  Version: $installed"
    }
    else {
        Write-Host "  Version: $label"
    }
    Write-Host "  Install: $InstallDirectory"
    Write-Host "  Command: $(Join-Path $InstallDirectory 'gowork.exe')"
    Write-Host "Project files and databases stay in the project you open."
}

if ($Help) {
    Show-Usage
    exit 0
}

if ($env:OS -ne "Windows_NT") {
    throw "this script is for Windows; on Linux or macOS use scripts/install.sh"
}

if ([string]::IsNullOrWhiteSpace($Version)) {
    $Version = "latest"
}
if ($Version -ne "latest") {
    if ($Version -match '^[vV]') {
        $Version = "v" + $Version.Substring(1)
    }
    else {
        $Version = "v" + $Version
    }
}

if ([string]::IsNullOrWhiteSpace($Dir)) {
    if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        $Dir = Join-Path $HOME "AppData\Local\Programs\GoWork"
    }
    else {
        $Dir = Join-Path $env:LOCALAPPDATA "Programs\GoWork"
    }
}
$InstallDir = Resolve-Directory $Dir
if (-not $InstallDir) {
    throw "the install directory is empty"
}
if ($InstallDir -eq [System.IO.Path]::GetPathRoot($InstallDir)) {
    throw "refusing to use a drive root as the install directory"
}

if ($Uninstall) {
    $removed = $false
    if (Test-Path -LiteralPath $InstallDir) {
        Remove-Item -LiteralPath $InstallDir -Recurse -Force
        Write-Host "Removed $InstallDir"
        $removed = $true
    }
    else {
        Write-Host "No installation found at $InstallDir."
    }

    if (Remove-FromUserPath $InstallDir) {
        $removed = $true
    }
    if (-not $removed) {
        Write-Host "Nothing to remove."
    }

    Write-Host "Project files and databases (for example Db\gowork.db) were not touched."
    Cleanup
    exit 0
}

if ($Update) {
    $current = Get-InstalledVersion $InstallDir
    if (-not $current) {
        throw "GoWork is not installed at $InstallDir; run the installer without -Update first"
    }

    $target = $Version
    if ($target -eq "latest") {
        $latest = Get-LatestTag
        if ($latest) {
            $target = $latest
        }
        else {
            Write-Host "warning: could not determine the latest release tag; installing the latest build"
            $target = $null
        }
    }

    if ($target -and $current -eq $target) {
        Write-Host "GoWork is already up to date ($current)."
        Cleanup
        exit 0
    }

    if ($target) {
        Write-Host "Updating GoWork $current -> $target"
        $Version = $target
    }
    else {
        Write-Host "Updating GoWork $current -> latest"
    }
}

$platformArch = Get-GoArch
Install-Release $platformArch $InstallDir
Cleanup
