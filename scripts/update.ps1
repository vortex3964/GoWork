# GoWork updater for Windows.
#
# Thin wrapper around install.ps1 so update behavior (path safety, checksum
# verification and user data preservation) stays in one place.

$installer = Join-Path $PSScriptRoot "install.ps1"
if (-not (Test-Path -LiteralPath $installer -PathType Leaf)) {
    Write-Host "error: install.ps1 was not found next to update.ps1" -ForegroundColor Red
    exit 1
}

& $installer -Update @args

$exitCode = Get-Variable -Name LASTEXITCODE -ValueOnly -ErrorAction SilentlyContinue
if ($null -eq $exitCode) {
    exit 0
}
exit [int]$exitCode
