param(
    [string]$Repository = 'MrTeeett/subbridge',
    [string]$Version = 'latest',
    [string]$Archive = '',
    [string]$Checksums = '',
    [string]$Destination = "$env:ProgramData\SubBridge",
    [switch]$Dev,
    [switch]$NoAutostart,
    [switch]$Stage
)
$ErrorActionPreference = 'Stop'
if ($Dev -and $PSBoundParameters.ContainsKey('Version')) { throw 'Choose either -Dev or -Version.' }
if ($Dev) { $Version = 'dev' }
if ($Repository -notmatch '^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$') { throw 'Invalid repository.' }
if ($Version -notin @('latest','dev') -and $Version -notmatch '^v\d+\.\d+\.\d+([.-][a-zA-Z0-9.-]+)?$') { throw 'Invalid version.' }
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($identity)
if (-not $Stage -and -not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Run PowerShell as administrator.' }
$Destination = [IO.Path]::GetFullPath($Destination)
if ($Destination -eq [IO.Path]::GetPathRoot($Destination) -or $Destination.Contains('"')) { throw 'Use a dedicated installation directory.' }
$architecture = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
$architecture = switch ($architecture) { 'AMD64' { 'amd64' } 'ARM64' { 'arm64' } default { throw '64-bit Windows (amd64/arm64) is required.' } }
$temporary = Join-Path ([IO.Path]::GetTempPath()) ('subbridge-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $temporary | Out-Null
try {
    if (-not $Archive) {
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
        if ($Version -eq 'latest') { $Version = (Invoke-RestMethod "https://api.github.com/repos/$Repository/releases/latest").tag_name }
        if ($Version -ne 'dev' -and $Version -notmatch '^v\d+\.\d+\.\d+([.-][a-zA-Z0-9.-]+)?$') { throw 'No published release found.' }
        $asset = "subbridge-$Version-windows-$architecture.zip"
        $Archive = Join-Path $temporary $asset; $Checksums = Join-Path $temporary 'checksums.txt'
        Invoke-WebRequest "https://github.com/$Repository/releases/download/$Version/$asset" -OutFile $Archive -UseBasicParsing
        Invoke-WebRequest "https://github.com/$Repository/releases/download/$Version/checksums.txt" -OutFile $Checksums -UseBasicParsing
    }
    $asset = [IO.Path]::GetFileName($Archive)
    $matchesFound = @(Get-Content -LiteralPath $Checksums | Where-Object { $_ -match ('^[0-9a-fA-F]{64}\s+\*?' + [Regex]::Escape($asset) + '$') })
    if ($matchesFound.Count -ne 1) { throw 'Archive must have a unique SHA-256 checksum.' }
    $expected = ($matchesFound[0] -split '\s+')[0]
    if ((Get-FileHash -LiteralPath $Archive -Algorithm SHA256).Hash -ne $expected) { throw 'Checksum mismatch; nothing was installed.' }
    $unpack = Join-Path $temporary 'unpack'
    # Inspect all entries before Expand-Archive can write to disk.
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [IO.Compression.ZipFile]::OpenRead($Archive)
    try {
        foreach ($entry in $zip.Entries) {
            $name = $entry.FullName.Replace('\','/')
            if ($name.StartsWith('/') -or $name.Contains('..') -or $name.Contains(':') -or $name -match '[\x00-\x1F]') { throw 'Unsafe archive path.' }
            if ((($entry.ExternalAttributes -shr 16) -band 0xF000) -eq 0xA000) { throw 'Archive symlinks are not allowed.' }
        }
    } finally { $zip.Dispose() }
    Expand-Archive -LiteralPath $Archive -DestinationPath $unpack
    foreach ($file in @('subbridge.exe','subbridge.ps1','install.ps1')) { if (-not (Test-Path -LiteralPath (Join-Path $unpack $file))) { throw "Missing release file: $file" } }
    & (Join-Path $unpack 'subbridge.exe') -version
    if ($LASTEXITCODE) { throw 'Binary does not run on this machine.' }
    New-Item -ItemType Directory -Force -Path $Destination | Out-Null
    $sid = $identity.User.Value
    # icacls requires a leading * for numeric SIDs, including the current user.
    & icacls.exe $Destination '/inheritance:r' '/grant:r' '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' "*${sid}:(OI)(CI)F" | Out-Null
    if ($LASTEXITCODE) { throw 'Could not apply private filesystem ACLs.' }
    $configDir = Join-Path $Destination 'config'; $dataDir = Join-Path $Destination 'state'
    New-Item -ItemType Directory -Force -Path $configDir,$dataDir | Out-Null
    & (Join-Path $unpack 'subbridge.exe') -config "$configDir\config.json" -management-config "$configDir\management.json" -data-dir $dataDir -setup
    if ($LASTEXITCODE) { throw 'Setup failed.' }
    & (Join-Path $unpack 'subbridge.exe') -config "$configDir\config.json" -check
    if ($LASTEXITCODE) { throw 'Configuration check failed.' }
    $previousTask = $null; $wasRunning = $false
    if (-not $Stage) { $previousTask = Get-ScheduledTask -TaskName SubBridge -ErrorAction SilentlyContinue; $wasRunning = $previousTask -and $previousTask.State -eq 'Running'; if ($wasRunning) { Stop-ScheduledTask -TaskName SubBridge; Start-Sleep -Seconds 2 } }
    $binary = Join-Path $Destination 'subbridge.exe'
    $backup = Join-Path $Destination 'subbridge.previous.exe'
    $hadBinary = Test-Path -LiteralPath $binary
    if ($hadBinary) { Copy-Item -LiteralPath $binary -Destination $backup -Force }
    try {
        Copy-Item -LiteralPath (Join-Path $unpack 'subbridge.exe') -Destination $binary -Force
        Copy-Item -LiteralPath (Join-Path $unpack 'subbridge.ps1'),(Join-Path $unpack 'install.ps1') -Destination $Destination -Force
        Set-Content -LiteralPath (Join-Path $Destination 'repository') -Value $Repository -Encoding ASCII
        $channel = if ($Version -eq 'dev' -or $asset -like 'subbridge-dev-windows-*.zip') { 'dev' } else { 'stable' }
        Set-Content -LiteralPath (Join-Path $Destination 'release-channel') -Value $channel -Encoding ASCII
        if (-not $Stage) {
            if (-not $previousTask) {
                $action = New-ScheduledTaskAction -Execute $binary -Argument "-config `"$configDir\config.json`" -management-config `"$configDir\management.json`" -log-file `"$Destination\subbridge.log`"" -WorkingDirectory $Destination
                $trigger = New-ScheduledTaskTrigger -AtStartup
                $settings = New-ScheduledTaskSettingsSet -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew -StartWhenAvailable
                Register-ScheduledTask -TaskName SubBridge -Action $action -Trigger $trigger -Settings $settings -User SYSTEM -RunLevel Highest -Force | Out-Null
                if ($NoAutostart) { Disable-ScheduledTask -TaskName SubBridge | Out-Null }
            }
            if ($wasRunning -or (-not $previousTask -and -not $NoAutostart)) {
                Start-ScheduledTask -TaskName SubBridge
                Start-Sleep -Seconds 2
                if ((Get-ScheduledTask -TaskName SubBridge).State -ne 'Running') { throw 'Scheduled task did not stay running.' }
            }
        }
    } catch {
        if ($hadBinary) { Copy-Item -LiteralPath $backup -Destination $binary -Force; if (-not $Stage -and $wasRunning) { Start-ScheduledTask -TaskName SubBridge } }
        throw
    }
    Remove-Item -LiteralPath $backup -Force -ErrorAction SilentlyContinue
    Write-Host "Installed: $Destination"
    Write-Host "Console: & '$Destination\subbridge.ps1'"
    Write-Host "Initial credentials: $configDir\setup-credentials.txt"
    Write-Host 'Default panel: http://127.0.0.1:8780/admin/'
} finally { Remove-Item -LiteralPath $temporary -Recurse -Force -ErrorAction SilentlyContinue }
