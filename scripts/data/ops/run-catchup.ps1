$ErrorActionPreference = 'Stop'
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$runner = Join-Path $scriptDir 'run-scheduled.ps1'

# 每个 profile 的 scheduled_for 由 pcdata 归一到最近计划时刻，因此每次开机调用
# 仍然幂等：只补最近一次，不会追跑全部历史周期。
$firstFailure = 0
$profiles = @('health', 'weekly', 'monthly')
$repoRoot = (Resolve-Path (Join-Path $scriptDir '..\..\..')).Path
$priceConfig = Get-Content -LiteralPath (Join-Path $repoRoot 'scripts\data\serpapi-baidu-products.json') -Raw | ConvertFrom-Json
if ($priceConfig.activation_status -eq 'active') {
    $profiles = @('health', 'price-daily', 'weekly', 'monthly')
}
foreach ($profile in $profiles) {
    & $runner -Profile $profile -CatchUp
    if ($LASTEXITCODE -ne 0 -and $firstFailure -eq 0) {
        $firstFailure = $LASTEXITCODE
    }
}

exit $firstFailure
