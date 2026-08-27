$ErrorActionPreference = 'Stop'
$names = @('Health', 'PriceDaily', 'Weekly', 'Retry', 'Monthly', 'CatchUp')
foreach ($name in $names) {
    $taskName = "PCBuilderData-$name"
    if (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue) {
        Unregister-ScheduledTask -TaskName $taskName -Confirm:$false
        Write-Host "已移除 $taskName"
    }
}
