param()
$ErrorActionPreference = 'Stop'
$ToolArguments = $args
$productRoot = Split-Path -Parent $PSScriptRoot
if ($IsWindows) {
  $linuxRoot = (& C:\Windows\System32\wsl.exe -d Ubuntu -- wslpath -a $productRoot.Replace('\','/')).Trim()
  & C:\Windows\System32\wsl.exe -d Ubuntu --cd $linuxRoot --exec docker compose @ToolArguments
} else {
  Push-Location $productRoot
  try { & docker compose @ToolArguments } finally { Pop-Location }
}
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
