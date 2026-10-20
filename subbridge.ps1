param(
    [ValidateSet('menu','start','stop','restart','status','logs','enable','disable','update','uninstall','credentials','settings','reset-admin','management','url','check','version','run')]
    [string]$Command = 'menu',
    [string]$Version = 'latest',
    [switch]$Dev,
    [switch]$Yes
)
$ErrorActionPreference = 'Stop'
if ($Dev -and $PSBoundParameters.ContainsKey('Version')) { throw 'Choose either -Dev or -Version.' }
$versionSpecified = $PSBoundParameters.ContainsKey('Version')
$core = Join-Path $PSScriptRoot 'subbridge.exe'
$configDir = Join-Path $PSScriptRoot 'config'
$coreArgs = @('-config',"$configDir\config.json",'-management-config',"$configDir\management.json")
function Run-Core {
    & $core @coreArgs @args
    if ($LASTEXITCODE) { throw 'SubBridge command failed.' }
}
function Invoke-CommandChoice([string]$choice) {
    switch ($choice) {
        'start' { Start-ScheduledTask -TaskName SubBridge }
        'stop' { Stop-ScheduledTask -TaskName SubBridge }
        'restart' { Stop-ScheduledTask -TaskName SubBridge; Start-Sleep -Seconds 1; Start-ScheduledTask -TaskName SubBridge }
        'status' { Get-ScheduledTask -TaskName SubBridge | Format-Table TaskName,State; Get-ScheduledTaskInfo -TaskName SubBridge | Format-List LastRunTime,LastTaskResult }
        'logs' { Get-Content -LiteralPath "$PSScriptRoot\subbridge.log" -Tail 100 }
        'enable' { Enable-ScheduledTask -TaskName SubBridge | Out-Null }
        'disable' { Disable-ScheduledTask -TaskName SubBridge | Out-Null }
        'update' {
            $repo = (Get-Content -LiteralPath "$PSScriptRoot\repository" -Raw).Trim()
            $updateVersion = $Version
            $channelPath = Join-Path $PSScriptRoot 'release-channel'
            if ($Dev -or (-not $versionSpecified -and (Test-Path -LiteralPath $channelPath) -and (Get-Content -LiteralPath $channelPath -Raw).Trim() -eq 'dev')) { $updateVersion = 'dev' }
            & "$PSScriptRoot\install.ps1" -Repository $repo -Version $updateVersion -Destination $PSScriptRoot
        }
        'credentials' { if (Test-Path -LiteralPath "$configDir\setup-credentials.txt") { Get-Content -LiteralPath "$configDir\setup-credentials.txt" } else { Write-Host 'Initial credentials removed. Use reset-admin if needed.' } }
        'settings' {
            $ip = Read-Host 'Management IP [127.0.0.1]'; if (-not $ip) { $ip = '127.0.0.1' }
            $port = Read-Host 'Management port [8780]'; if (-not $port) { $port = '8780' }
            Run-Core -admin-ip $ip -admin-port $port
            Invoke-CommandChoice 'restart'
        }
        'reset-admin' { Run-Core -reset-admin; Invoke-CommandChoice 'restart'; Write-Host 'Use credentials to read the new password.' }
        'management' { Run-Core -show-management }
        'url' { Run-Core -print-url }
        'check' { Run-Core -check }
        'version' { & $core -version }
        'run' { Run-Core }
        'uninstall' {
            if (-not $Yes -and (Read-Host 'Remove binaries/task, keep config/state? [y/N]') -ne 'y') { return }
            Stop-ScheduledTask -TaskName SubBridge -ErrorAction SilentlyContinue
            Unregister-ScheduledTask -TaskName SubBridge -Confirm:$false -ErrorAction SilentlyContinue
            Start-Sleep -Seconds 1
            Remove-Item -LiteralPath $core,"$PSScriptRoot\install.ps1","$PSScriptRoot\subbridge.ps1","$PSScriptRoot\repository" -Force
            Remove-Item -LiteralPath "$PSScriptRoot\release-channel" -Force -ErrorAction SilentlyContinue
            Write-Host 'Removed. Private config/state have been preserved.'
        }
    }
}
if ($Command -ne 'menu') { Invoke-CommandChoice $Command; exit }
while (Test-Path -LiteralPath $core) {
    Write-Host @'

  SubBridge
   1. Status
   2. Start
   3. Stop
   4. Restart
   5. Update
   6. Enable autostart
   7. Disable autostart
   8. Panel settings
   9. Reset password
  10. Credentials
  11. Subscription URL
  12. Uninstall
  13. Logs
   0. Exit
'@
    $choice = Read-Host '>'
    $commands = @{'1'='status';'2'='start';'3'='stop';'4'='restart';'5'='update';'6'='enable';'7'='disable';'8'='settings';'9'='reset-admin';'10'='credentials';'11'='url';'12'='uninstall';'13'='logs'}
    if ($choice -eq '0') { break }
    if ($commands.ContainsKey($choice)) { try { Invoke-CommandChoice $commands[$choice] } catch { Write-Host $_ -ForegroundColor Red } }
}
