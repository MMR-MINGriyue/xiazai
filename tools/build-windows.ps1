# Windows 桌面构建脚本（本机）
# 前置：VS BuildTools C++ + ATL、开发者模式、Flutter、Go、nuget、llvm-mingw
# 用法：powershell -File tools\build-windows.ps1

$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent
if ((Split-Path $root -Leaf) -eq "tools") { $root = Split-Path $root -Parent }
# repo root is gopeed/
if (Test-Path "$root\ui\flutter") { $repo = $root } else { $repo = "D:\workspace\xiazai\gopeed" }

$flutter = "D:\flutter\flutter\bin\flutter.bat"
$go = "D:\tools\go\bin\go.exe"
$llvm = "D:\tools\llvm-mingw-20240606-ucrt-x86_64\bin"
$nugetDir = "D:\tools"

$env:PATH = "$nugetDir;$llvm;$env:PATH"
if (-not ${env:ProgramFiles(x86)}) { ${env:ProgramFiles(x86)} = "C:\Program Files (x86)" }

Write-Host "==> build libgopeed.dll"
$env:CGO_ENABLED = "1"
$env:CC = "gcc"
$env:CXX = "g++"
Push-Location $repo
& $go build -ldflags="-w -s" -buildmode=c-shared -tags nosqlite `
  -o ui\flutter\windows\libgopeed.dll .\bind\desktop
if ($LASTEXITCODE -ne 0) { throw "libgopeed build failed" }
Pop-Location

Write-Host "==> flutter build windows"
Set-Location "$repo\ui\flutter"
& $flutter build windows --debug
if ($LASTEXITCODE -ne 0) { throw "flutter build failed" }

$out = "$repo\ui\flutter\build\windows\x64\runner\Debug\gopeed.exe"
if (Test-Path $out) {
  Write-Host "OK: $out"
} else {
  throw "output missing"
}
