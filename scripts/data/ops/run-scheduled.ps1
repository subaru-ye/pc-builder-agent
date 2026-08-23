param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('health', 'weekly', 'monthly', 'retry')]
    [string]$Profile,

    [switch]$CatchUp
)

$ErrorActionPreference = 'Stop'
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = (Resolve-Path (Join-Path $scriptDir '..\..\..')).Path
$logDir = Join-Path $repoRoot 'var\data\scheduler\logs'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$logPath = Join-Path $logDir "$Profile-$stamp.log"

function Write-DataLog {
    param([string]$Message)
    $line = "$(Get-Date -Format o) $Message"
    [IO.File]::AppendAllText($logPath, $line + "`n", [Text.UTF8Encoding]::new($false))
}

try {
    Set-Location $repoRoot
    Write-DataLog "start profile=$Profile catch_up=$($CatchUp.IsPresent)"

    docker info *> $null
    if ($LASTEXITCODE -ne 0) {
        throw 'Docker daemon 不可用；请确认 Docker Desktop 已启动'
    }
    docker compose up -d postgres redis *>> $logPath
    if ($LASTEXITCODE -ne 0) {
        throw 'docker compose up 失败'
    }

    $ready = $false
    for ($attempt = 1; $attempt -le 12; $attempt++) {
        docker compose exec -T postgres pg_isready -U pcbuilder -d pcbuilder *> $null
        $pgReady = $LASTEXITCODE -eq 0
        docker compose exec -T redis redis-cli ping *> $null
        $redisReady = $LASTEXITCODE -eq 0
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
        throw 'PostgreSQL 或 Redis 在 60 秒内未就绪'
    }

    go run ./cmd/migrate up *>> $logPath
    if ($LASTEXITCODE -ne 0) {
        throw '数据库迁移失败'
    }

    $arguments = @('run', '--project', 'scripts/data', 'pcdata', 'scheduled-run', '--profile', $Profile)
    if ($CatchUp) {
        $arguments += '--catch-up'
    }
    & uv @arguments *>> $logPath
    if ($LASTEXITCODE -ne 0) {
        throw "pcdata 退出码 $LASTEXITCODE"
    }
    Write-DataLog 'completed'
    exit 0
}
catch {
    Write-DataLog "failed: $($_.Exception.Message)"
    exit 1
}
