param([ValidateSet('All','Unit')][string]$Profile='All')
$ErrorActionPreference='Stop'
$productRoot=Split-Path -Parent $PSScriptRoot
$run=Join-Path $PSScriptRoot 'run.ps1'
$outputRoot=Join-Path $productRoot ('.local/checks/'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfffZ'))
New-Item -ItemType Directory -Path $outputRoot -Force | Out-Null
function Invoke-Check([string]$Name,[string[]]$CommandArguments) {
  $logPath=Join-Path $outputRoot ($Name+'.jsonl')
  [IO.File]::WriteAllText($logPath,'')
  & $run exec -T tooling @CommandArguments 2>&1 | Tee-Object -FilePath $logPath | Out-Null
  if ($LASTEXITCODE -ne 0) { Get-Content -LiteralPath $logPath -Tail 30; throw "$Name failed; retained $logPath" }
  return $logPath
}
$formatLog=Invoke-Check 'format' @('gofmt','-l','cmd','internal','migrations')
if ((Get-Content -LiteralPath $formatLog -Raw)) { throw "Formatting required: $formatLog" }
$null=Invoke-Check 'modules' @('go','mod','verify')
$null=Invoke-Check 'vet' @('go','vet','./...')
$null=Invoke-Check 'build' @('go','build','-trimpath','-o','build/p27','./cmd/p27')
$testArguments=@('go','test','-json','-p','1','-timeout','60s','-count=1')
if ($Profile -eq 'Unit') { $testArguments+='./internal/domain' } else { $testArguments+='./...' }
$tests=Invoke-Check 'tests' $testArguments
$events=@(Get-Content -LiteralPath $tests | Where-Object { $_.Trim().StartsWith('{') } | ForEach-Object { $_ | ConvertFrom-Json })
$pass=@($events | Where-Object { $_.Action -eq 'pass' -and $_.Test }).Count
$fail=@($events | Where-Object { $_.Action -eq 'fail' -and $_.Test }).Count
$skip=@($events | Where-Object { $_.Action -eq 'skip' -and $_.Test }).Count
if ($pass -lt 1 -or $fail -ne 0 -or $skip -ne 0) { throw 'Missing/failed/skipped required test events' }
if ($Profile -eq 'All') { $null=Invoke-Check 'race' @('go','test','-race','-p','1','-timeout','90s','-count=1','./...') }
$summary=[ordered]@{Profile=$Profile;PassedTestEvents=$pass;FailedTestEvents=$fail;SkippedTestEvents=$skip;Output=$outputRoot;SoftwareOnly=$true}
$summary | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $outputRoot 'summary.json') -Encoding utf8
$summary | ConvertTo-Json -Compress
