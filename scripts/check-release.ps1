# Cross-platform, offline checks for PowerShell release selection.
$ErrorActionPreference = 'Stop'
$temporary = Join-Path ([IO.Path]::GetTempPath()) ('subbridge-channel-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $temporary | Out-Null
try {
    $root = Split-Path $PSScriptRoot -Parent
    $installer = Get-Content -LiteralPath (Join-Path $root 'install.ps1') -Raw
    $boundary = $installer.IndexOf('$identity =')
    if ($boundary -lt 0) { throw 'Installer validation boundary is missing.' }
    $probe = Join-Path $temporary 'selection.ps1'
    Set-Content -LiteralPath $probe -Value ($installer.Substring(0, $boundary) + "`nWrite-Output `$Version")
    if ((& $probe) -ne 'latest') { throw 'Default installer channel must be stable.' }
    if ((& $probe -Dev) -ne 'dev') { throw '-Dev did not select dev.' }
    if ((& $probe -Version dev) -ne 'dev') { throw '-Version dev was rejected.' }
    if ((& $probe -Version v9.8.7) -ne 'v9.8.7') { throw 'Explicit version changed.' }
    foreach ($arguments in @(@{Dev=$true;Version='latest'}, @{Version='invalid'})) {
        $rejected = $false
        try { & $probe @arguments | Out-Null } catch { $rejected = $true }
        if (-not $rejected) { throw 'Invalid release selection was accepted.' }
    }
    Copy-Item -LiteralPath (Join-Path $root 'subbridge.ps1') -Destination $temporary
    Set-Content -LiteralPath (Join-Path $temporary 'repository') -Value 'example/subbridge'
    Set-Content -LiteralPath (Join-Path $temporary 'install.ps1') -Value @'
param([string]$Repository, [string]$Version, [string]$Destination)
Set-Content -LiteralPath (Join-Path $Destination 'requested-version') -Value $Version
'@
    $manager = Join-Path $temporary 'subbridge.ps1'
    $channel = Join-Path $temporary 'release-channel'
    foreach ($saved in @('stable','dev')) {
        Set-Content -LiteralPath $channel -Value $saved
        & $manager -Command update
        $expected = if ($saved -eq 'dev') { 'dev' } else { 'latest' }
        if ((Get-Content -LiteralPath (Join-Path $temporary 'requested-version') -Raw).Trim() -ne $expected) { throw 'Update changed the saved channel.' }
        & $manager -Command update -Dev
        if ((Get-Content -LiteralPath (Join-Path $temporary 'requested-version') -Raw).Trim() -ne 'dev') { throw 'Explicit dev update failed.' }
        & $manager -Command update -Version latest
        if ((Get-Content -LiteralPath (Join-Path $temporary 'requested-version') -Raw).Trim() -ne 'latest') { throw 'Explicit stable update failed.' }
        & $manager -Command update -Version v9.8.7
        if ((Get-Content -LiteralPath (Join-Path $temporary 'requested-version') -Raw).Trim() -ne 'v9.8.7') { throw 'Explicit version update failed.' }
    }
    Write-Host 'Offline PowerShell stable/dev selection and channel updates passed.'
} finally { Remove-Item -LiteralPath $temporary -Recurse -Force }
