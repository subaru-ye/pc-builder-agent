param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('health', 'price-daily', 'weekly', 'monthly', 'retry')]
    [string]$Profile,

    [switch]$CatchUp,

    [ValidatePattern('^\d{4}-\d{2}-\d{2}T')]
    [string]$ScheduledFor
)

$ErrorActionPreference = 'Stop'
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = (Resolve-Path (Join-Path $scriptDir '..\..\..')).Path
$logDir = Join-Path $repoRoot 'var\data\scheduler\logs'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$cutoff = (Get-Date).AddDays(-30)
$logFiles = @(Get-ChildItem -LiteralPath $logDir -File -Filter '*.log' | Sort-Object LastWriteTimeUtc -Descending)
foreach ($file in $logFiles) {
    if ($file.LastWriteTime -lt $cutoff) {
        Remove-Item -LiteralPath $file.FullName -Force
    }
}
$logFiles = @(Get-ChildItem -LiteralPath $logDir -File -Filter '*.log' | Sort-Object LastWriteTimeUtc -Descending)
if ($logFiles.Count -gt 199) {
    foreach ($file in $logFiles[199..($logFiles.Count - 1)]) {
        Remove-Item -LiteralPath $file.FullName -Force
    }
}
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$logPath = Join-Path $logDir "$Profile-$stamp.log"

function Write-DataLog {
    param([string]$Message)
    $line = "$(Get-Date -Format o) $Message"
    [IO.File]::AppendAllText($logPath, $line + "`n", [Text.UTF8Encoding]::new($false))
}

function Invoke-NativeCommand {
    param(
        [Parameter(Mandatory = $true)]
        [string]$FilePath,

        [string[]]$ArgumentList = @(),

        [switch]$DiscardOutput
    )

    # Windows PowerShell 5.1 会把原生命令 stderr（包括 Docker 的普通 warning）
    # 包装为 ErrorRecord；局部降为 Continue，最终仍只信任进程退出码。
    $previousPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        if ($DiscardOutput) {
            & $FilePath @ArgumentList *> $null
        }
        else {
            $output = @(& $FilePath @ArgumentList 2>&1)
            foreach ($item in $output) {
                $text = ([string]$item).Replace("`r`n", "`n").Replace("`r", "`n")
                [IO.File]::AppendAllText($logPath, $text + "`n", [Text.UTF8Encoding]::new($false))
            }
        }
        return $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousPreference
    }
}

try {
    Set-Location $repoRoot
    Write-DataLog "start profile=$Profile catch_up=$($CatchUp.IsPresent)"

    if ((Invoke-NativeCommand -FilePath 'docker' -ArgumentList @('info') -DiscardOutput) -ne 0) {
        throw 'Docker daemon 不可用；请确认 Docker Desktop 已启动'
    }
    if ((Invoke-NativeCommand -FilePath 'docker' -ArgumentList @('compose', 'up', '-d', 'postgres', 'redis')) -ne 0) {
        throw 'docker compose up 失败'
    }

    $ready = $false
    for ($attempt = 1; $attempt -le 12; $attempt++) {
        $pgReady = (Invoke-NativeCommand -FilePath 'docker' -ArgumentList @('compose', 'exec', '-T', 'postgres', 'pg_isready', '-U', 'pcbuilder', '-d', 'pcbuilder') -DiscardOutput) -eq 0
        $redisReady = (Invoke-NativeCommand -FilePath 'docker' -ArgumentList @('compose', 'exec', '-T', 'redis', 'redis-cli', 'ping') -DiscardOutput) -eq 0
        if ($pgReady) {
            $ready = $true
            if (-not $redisReady) {
                Write-DataLog 'warning: Redis 不可用；继续执行不依赖 Redis 的数据发布主链'
            }
            break
        }
        Start-Sleep -Seconds 5
    }
    if (-not $ready) {
        throw 'PostgreSQL 在 60 秒内未就绪；Redis 仅影响缓存，不阻断数据发布'
    }

    if ((Invoke-NativeCommand -FilePath 'go' -ArgumentList @('run', './cmd/migrate', 'up')) -ne 0) {
        throw '数据库迁移失败'
    }

    $arguments = @('run', '--project', 'scripts/data', 'pcdata', 'scheduled-run', '--profile', $Profile)
    if ($CatchUp) {
        $arguments += '--catch-up'
    }
    if ($ScheduledFor) {
        $arguments += @('--scheduled-for', $ScheduledFor)
    }
    $pcdataExitCode = Invoke-NativeCommand -FilePath 'uv' -ArgumentList $arguments
    if ($pcdataExitCode -ne 0) {
        throw "pcdata 退出码 $pcdataExitCode"
    }
    Write-DataLog 'completed'
    exit 0
}
catch {
    Write-DataLog "failed: $($_.Exception.Message)"
    exit 1
}
