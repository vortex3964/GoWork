# GoWork uninstaller for Windows.
#
# Thin wrapper around install.ps1 so removal behavior (path safety, PATH
# handling and user data preservation) stays in one place.

$installer = Join-Path $PSScriptRoot "install.ps1"
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
