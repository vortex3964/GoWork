# GoWork uninstaller for Windows.
#
# Thin wrapper around install.ps1 so removal behavior (path safety, PATH
# handling and user data preservation) stays in one place.

# Resolve-GoWorkScriptPath follows the wrapper's symlink chain so a launcher in
# another directory still finds install.ps1 beside the real script.
function Resolve-GoWorkScriptPath([string]$Path) {
    if ([string]::IsNullOrWhiteSpace($Path)) {
        return $null
    }

    $current = [System.IO.Path]::GetFullPath($Path)
    for ($hops = 0; $hops -lt 40; $hops++) {
        $item = Get-Item -LiteralPath $current -Force -ErrorAction SilentlyContinue
        if ($null -eq $item) {
            return $current
        }

        $linkType = $item.PSObject.Properties["LinkType"]
        if ($null -eq $linkType -or
            [string]::IsNullOrWhiteSpace([string]$linkType.Value) -or
            $linkType.Value -eq "HardLink") {
            return $item.FullName
        }

        $target = $item.PSObject.Properties["Target"]
        if ($null -eq $target -or [string]::IsNullOrWhiteSpace([string]$target.Value)) {
            return $current
        }
        $targetPath = $target.Value
        if ($targetPath -is [System.Array]) {
            $targetPath = $targetPath[0]
        }
        if ([string]::IsNullOrWhiteSpace([string]$targetPath)) {
            return $current
        }
        $targetPath = [string]$targetPath

        if ([System.IO.Path]::IsPathRooted($targetPath)) {
            $current = [System.IO.Path]::GetFullPath($targetPath)
        }
        else {
            $parent = [System.IO.Path]::GetDirectoryName($current)
            $current = [System.IO.Path]::GetFullPath((Join-Path $parent $targetPath))
        }
    }
    return $null
}

$scriptPath = $PSCommandPath
if ([string]::IsNullOrWhiteSpace($scriptPath)) {
    $scriptPath = $MyInvocation.MyCommand.Path
}
if ([string]::IsNullOrWhiteSpace($scriptPath) -and -not [string]::IsNullOrWhiteSpace($PSScriptRoot)) {
    $scriptPath = Join-Path $PSScriptRoot "uninstall.ps1"
}

$resolvedScript = Resolve-GoWorkScriptPath $scriptPath
if ([string]::IsNullOrWhiteSpace([string]$resolvedScript)) {
    Write-Host "error: could not determine the location of uninstall.ps1" -ForegroundColor Red
    exit 1
}

$installer = Join-Path (Split-Path -Parent $resolvedScript) "install.ps1"
if (-not (Test-Path -LiteralPath $installer -PathType Leaf)) {
    Write-Host "error: install.ps1 was not found next to uninstall.ps1" -ForegroundColor Red
    exit 1
}

& $installer -Uninstall @args

$exitCode = Get-Variable -Name LASTEXITCODE -ValueOnly -ErrorAction SilentlyContinue
if ($null -eq $exitCode) {
    exit 0
}
exit [int]$exitCode
