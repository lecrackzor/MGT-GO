# PowerShell script to capture pprof heap profiles
# Usage: .\scripts\capture_pprof.ps1 [output_file]

param(
    [string]$OutputFile = "heap_profile_$(Get-Date -Format 'yyyyMMdd_HHmmss').pb.gz"
)

$pprofUrl = "http://localhost:6060/debug/pprof/heap"

Write-Host "Capturing heap profile from $pprofUrl..."
Write-Host "Output: $OutputFile"

try {
    $response = Invoke-WebRequest -Uri $pprofUrl -Method Get -OutFile $OutputFile
    Write-Host "✅ Profile captured successfully: $OutputFile"
    Write-Host ""
    Write-Host "To analyze:"
    Write-Host "  go tool pprof $OutputFile"
    Write-Host ""
    Write-Host "In pprof prompt:"
    Write-Host "  top        - Show top allocations"
    Write-Host "  top10      - Show top 10 allocations"
    Write-Host "  list GetChartData - Show allocations in GetChartData"
    Write-Host "  list json.Marshal - Show allocations in JSON marshaling"
    Write-Host "  web        - Generate visualization (requires Graphviz)"
} catch {
    Write-Host "❌ Error capturing profile: $_"
    Write-Host "Make sure the application is running and pprof is accessible at $pprofUrl"
    exit 1
}
