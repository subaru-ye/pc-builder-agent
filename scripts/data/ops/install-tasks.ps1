param(
    [switch]$SkipBootstrap
)

$ErrorActionPreference = 'Stop'
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = (Resolve-Path (Join-Path $scriptDir '..\..\..')).Path
$runner = Join-Path $scriptDir 'run-scheduled.ps1'
$catchup = Join-Path $scriptDir 'run-catchup.ps1'
$prefix = 'PCBuilderData'

if (-not $SkipBootstrap) {
    Set-Location $repoRoot
    docker compose up -d postgres redis
    if ($LASTEXITCODE -ne 0) { throw '启动 PostgreSQL/Redis 失败' }
    go run ./cmd/migrate up
    if ($LASTEXITCODE -ne 0) { throw '数据库迁移失败' }
    uv run --project scripts/data pcdata bootstrap
    if ($LASTEXITCODE -ne 0) { throw '初始 last-known-good 建立失败' }
}

$principal = New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet `
    -StartWhenAvailable `
    -MultipleInstances IgnoreNew `
    -ExecutionTimeLimit (New-TimeSpan -Minutes 20) `
    -RestartCount 2 `
    -RestartInterval (New-TimeSpan -Minutes 10)

function Register-PCBuilderTask {
    param(
        [string]$Name,
        [string]$Script,
        [string]$Arguments,
        [CimInstance]$Trigger
    )
    $quotedScript = '"' + $Script + '"'
    $actionArgs = "-NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File $quotedScript $Arguments"
    $action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument $actionArgs -WorkingDirectory $repoRoot
    Register-ScheduledTask `
        -TaskName "$prefix-$Name" `
        -Action $action `
        -Trigger $Trigger `
        -Principal $principal `
        -Settings $settings `
        -Description "PC Builder P11/P12 数据任务：$Name" `
        -Force | Out-Null
}

Register-PCBuilderTask -Name 'Health' -Script $runner -Arguments '-Profile health' `
    -Trigger (New-ScheduledTaskTrigger -Daily -At '03:30')
$installedCount = 5
$priceConfig = Get-Content -LiteralPath (Join-Path $repoRoot 'scripts\data\serpapi-baidu-products.json') -Raw | ConvertFrom-Json
if ($priceConfig.activation_status -eq 'active') {
    Register-PCBuilderTask -Name 'PriceDaily' -Script $runner -Arguments '-Profile price-daily' `
        -Trigger (New-ScheduledTaskTrigger -Daily -At '03:45')
    $installedCount = 6
}
else {
    Write-Host 'SerpApi canary 尚未批准；未安装 PCBuilderData-PriceDaily。'
}
Register-PCBuilderTask -Name 'Weekly' -Script $runner -Arguments '-Profile weekly' `
    -Trigger (New-ScheduledTaskTrigger -Weekly -DaysOfWeek Monday -At '04:00')
Register-PCBuilderTask -Name 'Retry' -Script $runner -Arguments '-Profile retry' `
    -Trigger (New-ScheduledTaskTrigger -Weekly -DaysOfWeek Wednesday -At '04:00')
# Windows cmdlet 没有稳定的“每月 1 日”触发构造器；这里每日唤醒，pcdata
# 将 monthly 归一到当月 1 日 04:30，同一幂等键只会实际执行一次。
Register-PCBuilderTask -Name 'Monthly' -Script $runner -Arguments '-Profile monthly' `
    -Trigger (New-ScheduledTaskTrigger -Daily -At '04:30')

$startupTrigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
$startupTrigger.Delay = 'PT5M'
Register-PCBuilderTask -Name 'CatchUp' -Script $catchup -Arguments '' -Trigger $startupTrigger

Write-Host "已安装 $installedCount 个 $prefix-* 本机任务。日志目录：$repoRoot\var\data\scheduler\logs"
