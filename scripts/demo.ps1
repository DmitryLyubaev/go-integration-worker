param()
$ErrorActionPreference='Stop'
$productRoot=Split-Path -Parent $PSScriptRoot
$run=Join-Path $PSScriptRoot 'run.ps1'
Push-Location $productRoot
try {
  $dirty=git status --porcelain
  if ($LASTEXITCODE -ne 0 -or $dirty) { throw 'Commit all source/fixture changes before canonical execution' }
  $sourceCommit=(git rev-parse HEAD).Trim()
  $stamp=[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfffZ')
  $preparationRoot=Join-Path $productRoot ".local/preparation/$stamp"
  $acceptanceRoot=Join-Path $productRoot '.local/acceptance'
  $outputRoot=Join-Path $acceptanceRoot $stamp
  New-Item -ItemType Directory -Path $preparationRoot,$acceptanceRoot -Force | Out-Null
  $preparation=[Diagnostics.Stopwatch]::StartNew()
  & $run up -d
  if ($LASTEXITCODE -ne 0) { throw 'Owned runtime preparation failed' }
  & $run exec -T tooling go build -trimpath -o build/p27 ./cmd/p27
  if ($LASTEXITCODE -ne 0) { throw 'Worker build failed' }
  & $run exec -T tooling go build -trimpath -o build/p27-accept ./cmd/p27-accept
  if ($LASTEXITCODE -ne 0) { throw 'Acceptance build failed' }
  $files=@(git ls-files | ForEach-Object { [ordered]@{Path=$_;SHA256=(Get-FileHash -LiteralPath (Join-Path $productRoot $_) -Algorithm SHA256).Hash.ToLowerInvariant()} })
  $manifest=[ordered]@{SourceCommit=$sourceCommit;Files=$files}
  [IO.File]::WriteAllText((Join-Path $preparationRoot 'source-manifest.json'),($manifest | ConvertTo-Json -Depth 8),[Text.UTF8Encoding]::new($false))
  git archive --format=tar -o (Join-Path $preparationRoot 'source.tar') HEAD
  if ($LASTEXITCODE -ne 0) { throw 'Source archive failed' }
  if (git status --porcelain) { throw 'Source changed during preparation' }
  $preparation.Stop()
  $runtime=[Diagnostics.Stopwatch]::StartNew()
  $runtimeExit=1
  $cleaned=$false
  try {
    & $run exec -T tooling build/p27-accept -binary /workspace/build/p27 -fixture /workspace/fixtures/canonical.json -out "/workspace/.local/acceptance/$stamp" -source-commit $sourceCommit -source-root /workspace -source-manifest "/workspace/.local/preparation/$stamp/source-manifest.json" -source-archive "/workspace/.local/preparation/$stamp/source.tar" 2>&1 | Tee-Object -FilePath (Join-Path $preparationRoot 'canonical-command.log')
    $runtimeExit=$LASTEXITCODE
  } finally {
    & $run stop --timeout 3
    $stopExit=$LASTEXITCODE
    $running=@(& $run ps --status running -q)
    $cleaned=($stopExit -eq 0 -and $LASTEXITCODE -eq 0 -and $running.Count -eq 0)
    $runtime.Stop()
    $resultPath=Join-Path $outputRoot 'result.json'
    $resultHash=$null
    if (Test-Path -LiteralPath $resultPath) { $resultHash=(Get-FileHash -LiteralPath $resultPath -Algorithm SHA256).Hash.ToLowerInvariant() }
    $receipt=[ordered]@{SourceCommit=$sourceCommit;ResultSHA256=$resultHash;RuntimeExit=$runtimeExit;ContainersNotRunning=$cleaned;RuntimeAndContainerCleanupSeconds=$runtime.Elapsed.TotalSeconds;PreparationSeconds=$preparation.Elapsed.TotalSeconds;RetainedVolumes=@('p27-local_db','p27-local_gomod','p27-local_gocache');Output=$outputRoot;FinishedUTC=[DateTime]::UtcNow.ToString('o')}
    $receipt | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $preparationRoot 'host-cleanup.json') -Encoding utf8
    $receipt | ConvertTo-Json -Depth 4 -Compress
  }
  if ($runtimeExit -ne 0 -or -not $cleaned -or $runtime.Elapsed.TotalSeconds -gt 300) { throw "Canonical sample incomplete/failed; preserve $preparationRoot and $outputRoot" }
  if (git status --porcelain) { throw 'Source changed during canonical sample' }
} finally { Pop-Location }
