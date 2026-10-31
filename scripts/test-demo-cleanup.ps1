param()
$ErrorActionPreference='Stop'
$productRoot=Split-Path -Parent $PSScriptRoot
$testRoot=Join-Path $productRoot ('.local/orchestration-tests/'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfffZ'))
$shell=(Get-Process -Id $PID).Path
$cases=0
foreach ($failureStage in @('up','build')) {
  $caseRoot=Join-Path $testRoot $failureStage
  $scriptsRoot=Join-Path $caseRoot 'scripts'
  New-Item -ItemType Directory -Path $scriptsRoot -Force | Out-Null
  Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'demo.ps1') -Destination (Join-Path $scriptsRoot 'demo.ps1')
  $stub=@'
param()
$root=Split-Path -Parent $PSScriptRoot
$state=Join-Path $root '.local/fake-runtime-state'
New-Item -ItemType Directory -Path (Split-Path -Parent $state) -Force | Out-Null
$failStage='__STAGE__'
switch ($args[0]) {
  'up' { [IO.File]::WriteAllText($state,'running'); if ($failStage -eq 'up') { exit 1 }; exit 0 }
  'exec' { exit 1 }
  'stop' { [IO.File]::WriteAllText($state,'stopped'); exit 0 }
  'ps' { if ([IO.File]::ReadAllText($state) -eq 'running') { 'owned-runtime-id' }; exit 0 }
}
exit 2
'@
  [IO.File]::WriteAllText((Join-Path $scriptsRoot 'run.ps1'),$stub.Replace('__STAGE__',$failureStage),[Text.UTF8Encoding]::new($false))
  [IO.File]::WriteAllText((Join-Path $caseRoot '.gitignore'),".local/`n",[Text.UTF8Encoding]::new($false))
  git init -q -b main $caseRoot
  git -C $caseRoot config user.name 'Dmitry Lyubaev'
  git -C $caseRoot config user.email 'Dmitry.Lyubaev@gmail.com'
  git -C $caseRoot add -- .
  git -C $caseRoot commit -qm 'test: isolate preparation failure fixture'
  New-Item -ItemType Directory -Path (Join-Path $caseRoot '.local') -Force | Out-Null
  & $shell -NoProfile -File (Join-Path $scriptsRoot 'demo.ps1') *> (Join-Path $caseRoot '.local/command.log')
  $observedExit=$LASTEXITCODE
  if ($observedExit -eq 0) { throw "$failureStage failure was reported successful" }
  $state=[IO.File]::ReadAllText((Join-Path $caseRoot '.local/fake-runtime-state'))
  if ($state -ne 'stopped') { throw "$failureStage preparation failure left the owned runtime running" }
  $receipts=@(Get-ChildItem -LiteralPath (Join-Path $caseRoot '.local/preparation') -Recurse -Filter host-cleanup.json)
  if ($receipts.Count -ne 1) { throw "$failureStage failure did not retain a cleanup receipt" }
  $receipt=Get-Content -LiteralPath $receipts[0].FullName -Raw | ConvertFrom-Json
  if (-not $receipt.ContainersNotRunning -or $receipt.CanonicalStarted -or -not $receipt.Failure) { throw "$failureStage failure receipt is inaccurate" }
  $cases++
}
"PowerShell preparation-cleanup cases passed: $cases (controlled external-runtime fixture; no Docker action)."
exit 0
