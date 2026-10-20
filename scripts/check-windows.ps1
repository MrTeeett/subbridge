$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot 'check-release.ps1')
function Run-Go {
    & go @args
    if ($LASTEXITCODE -ne 0) { throw "Go check failed: $args" }
}
function Assert-PrivateInstallationAcl {
    param([string]$Directory)
    $expectedSids = @('S-1-5-18', 'S-1-5-32-544', [Security.Principal.WindowsIdentity]::GetCurrent().User.Value)
    $fullControl = [Security.AccessControl.FileSystemRights]::FullControl
    foreach ($relative in @('', 'config', 'state', 'config/config.json', 'config/management.json', 'config/setup-credentials.txt')) {
        $path = if ($relative) { Join-Path $Directory $relative } else { $Directory }
        $acl = Get-Acl -LiteralPath $path
        if (-not $relative -and -not $acl.AreAccessRulesProtected) { throw 'Installation directory still inherits parent permissions.' }
        $rules = @($acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))
        foreach ($rule in $rules) {
            if ($rule.IdentityReference.Value -notin $expectedSids -or
                $rule.AccessControlType -ne [Security.AccessControl.AccessControlType]::Allow -or
                ($rule.FileSystemRights -band $fullControl) -ne $fullControl) {
                throw "Unexpected filesystem permission on $path."
            }
        }
        foreach ($sid in $expectedSids) {
            if (-not ($rules | Where-Object { $_.IdentityReference.Value -eq $sid })) {
                throw "Required SID grant is missing on $path."
            }
        }
    }
}
if ((go env GOVERSION) -notmatch '^go1\.26\.') { throw 'Go 1.26.x is required.' }
Run-Go vet -p=1 ./...
Run-Go test -p=1 -parallel=1 -count=1 ./...
Run-Go build -p=1 -trimpath -o "$env:RUNNER_TEMP/subbridge.exe" .
& "$env:RUNNER_TEMP/subbridge.exe" -version
if ($LASTEXITCODE -ne 0) { throw 'Binary startup failed.' }
foreach ($path in @('install.ps1','subbridge.ps1')) {
    $tokens = $null; $errors = $null
    [System.Management.Automation.Language.Parser]::ParseFile((Join-Path $PWD $path), [ref]$tokens, [ref]$errors) | Out-Null
    if ($errors.Count) { throw ($errors | Out-String) }
}
$releaseDirectory = Join-Path $PWD 'release'
$archive = Get-ChildItem -Path "$releaseDirectory/subbridge-*-windows-amd64.zip" | Select-Object -First 1
if (-not $archive) { throw 'Windows release archive is missing.' }
$unpack = Join-Path $env:RUNNER_TEMP 'subbridge-release'
$stage = Join-Path $env:RUNNER_TEMP 'subbridge stage'
Expand-Archive -LiteralPath $archive.FullName -DestinationPath $unpack
& "$unpack/install.ps1" -Archive $archive.FullName -Checksums "$releaseDirectory/checksums.txt" -Destination $stage -Stage
Assert-PrivateInstallationAcl -Directory $stage
$expectedChannel = if ($archive.Name -like 'subbridge-dev-*') { 'dev' } else { 'stable' }
if ((Get-Content -LiteralPath "$stage/release-channel" -Raw).Trim() -ne $expectedChannel) { throw 'Installer saved an incorrect release channel.' }
$before = (Get-FileHash "$stage/config/config.json","$stage/config/management.json","$stage/config/setup-credentials.txt").Hash -join ','
& "$unpack/install.ps1" -Archive $archive.FullName -Checksums "$releaseDirectory/checksums.txt" -Destination $stage -Stage
Assert-PrivateInstallationAcl -Directory $stage
$after = (Get-FileHash "$stage/config/config.json","$stage/config/management.json","$stage/config/setup-credentials.txt").Hash -join ','
if ($before -ne $after) { throw 'Upgrade changed private settings or credentials.' }
& "$stage/subbridge.ps1" -Command check
Write-Host 'Windows install, numeric SID grants, private ACL inheritance and update preservation passed.'
