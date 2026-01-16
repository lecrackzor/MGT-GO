# PowerShell script to compare two pprof profiles
# Usage: .\scripts\compare_pprof.ps1 before.pb.gz after.pb.gz

param(
    [Parameter(Mandatory=$true)]
    [string]$BeforeFile,
    
    [Parameter(Mandatory=$true)]
    [string]$AfterFile
)

if (-not (Test-Path $BeforeFile)) {
    Write-Host "❌ Before file not found: $BeforeFile"
    exit 1
}

if (-not (Test-Path $AfterFile)) {
    Write-Host "❌ After file not found: $AfterFile"
    exit 1
}

Write-Host "Comparing profiles:"
Write-Host "  Before: $BeforeFile"
Write-Host "  After:  $AfterFile"
Write-Host ""

$tempFile = "compare_output_$(Get-Date -Format 'yyyyMMdd_HHmmss').txt"

try {
    # Use go tool pprof to compare
    $command = "go tool pprof -base $BeforeFile $AfterFile -top"
    Write-Host "Running: $command"
    Write-Host ""
    
    Invoke-Expression $command | Out-File -FilePath $tempFile
    
    Write-Host "✅ Comparison saved to: $tempFile"
    Write-Host ""
    Write-Host "Top differences:"
    Get-Content $tempFile | Select-Object -First 30
} catch {
    Write-Host "❌ Error comparing profiles: $_"
    Write-Host ""
    Write-Host "Manual comparison:"
    Write-Host "  go tool pprof -base $BeforeFile $AfterFile"
    exit 1
}
