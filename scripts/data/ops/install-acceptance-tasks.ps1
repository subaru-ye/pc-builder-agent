param(
    [datetime]$FirstRunAt = (Get-Date).AddMinutes(2),
    [datetime]$SecondRunAt = (Get-Date).AddMinutes(7)
)

$ErrorActionPreference = 'Stop'
if ($SecondRunAt -le $FirstRunAt) {
    throw 'SecondRunAt 必须晚于 FirstRunAt'
}

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = (Resolve-Path (Join-Path $scriptDir '..\..\..')).Path
$runner = Join-Path $scriptDir 'run-acceptance.ps1'
$principal = New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet `
    -StartWhenAvailable `
    -MultipleInstances IgnoreNew `
    -ExecutionTimeLimit (New-TimeSpan -Minutes 20)

function Register-AcceptanceTask {
    param(
        [string]$Name,
        [datetime]$RunAt,
        [string]$ScheduledFor
    )

    $quotedRunner = '"' + $runner + '"'
    $arguments = "-NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File $quotedRunner -TaskName $Name -ScheduledFor $ScheduledFor"
    $action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument $arguments -WorkingDirectory $repoRoot
    Register-ScheduledTask `
        -TaskName $Name `
        -Action $action `
        -Trigger (New-ScheduledTaskTrigger -Once -At $RunAt) `
        -Principal $principal `
        -Settings $settings `
        -Description 'PC Builder P11 一次性真实调度验收；执行后自动删除' `
        -Force | Out-Null
}

$firstName = 'PCBuilderData-Acceptance-1'
$secondName = 'PCBuilderData-Acceptance-2'
$firstScheduledFor = $FirstRunAt.ToUniversalTime().ToString('o')
$secondScheduledFor = $SecondRunAt.ToUniversalTime().ToString('o')

Register-AcceptanceTask -Name $firstName -RunAt $FirstRunAt -ScheduledFor $firstScheduledFor
Register-AcceptanceTask -Name $secondName -RunAt $SecondRunAt -ScheduledFor $secondScheduledFor

Write-Host "已安装两项一次性验收任务：$firstName、$secondName；完成后自动删除。"
