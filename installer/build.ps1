param([string]$ISCC, [switch]$BinariesOnly)
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$output = Join-Path $root 'dist'
$staging = Join-Path $root '_probe\package'
New-Item -ItemType Directory -Force $output,$staging | Out-Null
if (-not $BinariesOnly -and -not $ISCC) {
  $candidates = @(
    (Join-Path ${env:ProgramFiles(x86)} 'Inno Setup 6\ISCC.exe'),
    (Join-Path $env:LOCALAPPDATA 'Programs\Inno Setup 6\ISCC.exe'),
    (Join-Path $root '_probe\tools\InnoSetup\ISCC.exe')
  )
  $ISCC = $candidates | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
  if (-not $ISCC) { throw '请先安装 Inno Setup 6，或通过 -ISCC 指定 ISCC.exe。' }
}
$oldOS=$env:GOOS; $oldArch=$env:GOARCH; $oldCGO=$env:CGO_ENABLED
Push-Location (Join-Path $root 'src')
try {
  $env:GOOS='windows'; $env:CGO_ENABLED='0'
  foreach ($arch in @('x64','arm64')) {
    $env:GOARCH=if($arch -eq 'x64'){'amd64'}else{'arm64'}
    $target=Join-Path $staging $arch
    New-Item -ItemType Directory -Force $target | Out-Null
    & go build -trimpath -buildvcs=false -ldflags '-s -w -H=windowsgui' -o (Join-Path $target 'collector.exe') .
    if($LASTEXITCODE -ne 0){throw "$arch 图形程序构建失败"}
    & go build -trimpath -buildvcs=false -ldflags '-s -w' -o (Join-Path $target 'collector-cli.exe') .
    if($LASTEXITCODE -ne 0){throw "$arch 命令行程序构建失败"}
    if(-not $BinariesOnly){
      & $ISCC "/DTargetArch=$arch" "/DBuildRoot=$target" "/DOutputRoot=$output" (Join-Path $PSScriptRoot 'ScreenTimeObserver.iss')
      if($LASTEXITCODE -ne 0){throw "$arch 安装包构建失败"}
    }
  }
  if(-not $BinariesOnly){
    Get-ChildItem $output -Filter '*-setup.exe' | ForEach-Object {
      $hash=Get-FileHash $_.FullName -Algorithm SHA256
      "$($hash.Hash)  $($_.Name)"
    } | Set-Content (Join-Path $output 'SHA256SUMS.txt') -Encoding utf8
  }
} finally {
  $env:GOOS=$oldOS; $env:GOARCH=$oldArch; $env:CGO_ENABLED=$oldCGO
  Pop-Location
}
