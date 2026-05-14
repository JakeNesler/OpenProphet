$d = Get-Date -Format yyyyMMdd
  Copy-Item data/reports/regime_gate.json "data/reports/regime_gate_$d.json"
  Invoke-RestMethod http://localhost:4534/api/v1/guard/status | ConvertTo-Json -Depth 10 | Out-File "data/reports/guard_status_$d.json"
  Write-Host "Snapshot saved for $d"