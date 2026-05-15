# nlink 运维入口 (Windows / PowerShell) —— 对标 Linux 的 admin.sh
#
# 用法（管理员 PowerShell）:
#   C:\nlink\admin.ps1 <command> [args]
#
# 命令:
#   start              启动服务
#   stop               停止服务
#   restart            重启服务
#   status             看运行状态
#   logs [-Follow]     看日志, -Follow 持续跟随
#   update             升级到最新版本
#   install-service    注册为 Windows 服务（开机自启）
#   uninstall-service  卸载 Windows 服务
#   version            打印 nlink 版本
#   config             打印配置文件路径

param(
    [Parameter(Position = 0)]
    [string]$Command = "help",
    [Parameter(Position = 1, ValueFromRemainingArguments = $true)]
    [string[]]$Args
)

$ErrorActionPreference = "Stop"

# 控制台 UTF-8
try {
    [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
    [Console]::InputEncoding  = [System.Text.Encoding]::UTF8
} catch {}

$InstallDir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { Split-Path -Parent $PSCommandPath }
if (-not (Test-Path $InstallDir)) { $InstallDir = "C:\nlink" }
$Bin        = Join-Path $InstallDir "nlink.exe"
$Nssm       = Join-Path $InstallDir "nssm.exe"
$ConfigFile = Join-Path $InstallDir "config\nlink.yaml"
$LogDir     = Join-Path $InstallDir "data\logs"
$StdoutLog  = Join-Path $LogDir "stdout.log"
$StderrLog  = Join-Path $LogDir "stderr.log"
$SvcName    = "nlink"

function Red    ($s) { Write-Host $s -ForegroundColor Red }
function Green  ($s) { Write-Host $s -ForegroundColor Green }
function Yellow ($s) { Write-Host $s -ForegroundColor Yellow }
function Info   ($s) { Write-Host "→ $s" }

function Require-Admin {
    $me = [Security.Principal.WindowsIdentity]::GetCurrent()
    $wp = New-Object Security.Principal.WindowsPrincipal $me
    if (-not $wp.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        Red "请用 [管理员] PowerShell 执行此命令"
        exit 1
    }
}

function Ensure-Bin {
    if (-not (Test-Path $Bin)) {
        Red "找不到二进制: $Bin"
        Red "先跑安装: [Console]::OutputEncoding=[Text.Encoding]::UTF8; iex (curl.exe -sL https://raw.githubusercontent.com/hsqbyte/nlink/master/deploy/install.ps1 | Out-String)"
        exit 1
    }
}

function Has-Nssm    { Test-Path $Nssm }
function Has-Service { $null -ne (Get-Service -Name $SvcName -ErrorAction SilentlyContinue) }
function Mode {
    if ((Has-Service) -and (Has-Nssm)) { return "service" } else { return "manual" }
}

function Cmd-Start {
    Require-Admin
    Ensure-Bin
    switch (Mode) {
        "service" {
            & $Nssm start $SvcName | Out-Null
            Start-Sleep -Seconds 1
            $s = Get-Service -Name $SvcName
            if ($s.Status -eq "Running") { Green "✓ 已启动" }
            else { Red "启动失败 (状态: $($s.Status))"; exit 1 }
        }
        "manual" {
            if (Get-Process -Name nlink -ErrorAction SilentlyContinue) {
                Yellow "已在运行"; return
            }
            if (-not (Test-Path $ConfigFile)) { Red "缺少配置: $ConfigFile"; exit 1 }
            $p = Start-Process -FilePath $Bin -ArgumentList "-c", "config\nlink.yaml" `
                               -WorkingDirectory $InstallDir -PassThru -WindowStyle Hidden
            Start-Sleep -Seconds 1
            if (-not $p.HasExited) { Green "✓ 已启动 (PID $($p.Id))" }
            else { Red "启动失败 (exit $($p.ExitCode))"; exit 1 }
        }
    }
}

function Cmd-Stop {
    Require-Admin
    switch (Mode) {
        "service" {
            & $Nssm stop $SvcName | Out-Null
            Green "✓ 已停止"
        }
        "manual" {
            $procs = Get-Process -Name nlink -ErrorAction SilentlyContinue
            if (-not $procs) { Yellow "进程未运行"; return }
            $procs | Stop-Process -Force
            Green "✓ 已停止"
        }
    }
}

function Cmd-Restart {
    Require-Admin
    Ensure-Bin
    switch (Mode) {
        "service" {
            & $Nssm restart $SvcName | Out-Null
            Start-Sleep -Seconds 1
            $s = Get-Service -Name $SvcName
            if ($s.Status -eq "Running") { Green "✓ 已重启" }
            else { Red "重启失败 (状态: $($s.Status))"; exit 1 }
        }
        "manual" {
            Cmd-Stop
            Cmd-Start
        }
    }
}

function Cmd-Status {
    switch (Mode) {
        "service" {
            $s = Get-Service -Name $SvcName
            if ($s.Status -eq "Running") {
                Green "● nlink 运行中（服务模式）"
                & $Nssm status $SvcName
            } else {
                Red "○ nlink 未运行（服务模式, 状态: $($s.Status)）"
                exit 3
            }
        }
        "manual" {
            $procs = Get-Process -Name nlink -ErrorAction SilentlyContinue
            if ($procs) {
                Green "● nlink 运行中（手动模式）"
                $procs | Select-Object Id, Name, StartTime, CPU | Format-Table
            } else {
                Red "○ nlink 未运行"
                exit 3
            }
        }
    }
}

function Cmd-Logs {
    $follow = $Args -contains "-Follow" -or $Args -contains "-f"
    $files = @($StdoutLog, $StderrLog) | Where-Object { Test-Path $_ }
    if (-not $files) {
        Yellow "日志文件不存在（$LogDir）。可能用了手动模式且没有重定向。"
        return
    }
    if ($follow) {
        Get-Content -Path $files -Tail 50 -Wait
    } else {
        foreach ($f in $files) {
            Write-Host "=== $f ===" -ForegroundColor Cyan
            Get-Content -Path $f -Tail 100
            Write-Host ""
        }
    }
}

function Cmd-InstallService {
    Require-Admin
    Ensure-Bin
    if (-not (Has-Nssm)) { Red "找不到 $Nssm，无法注册服务"; exit 1 }
    if (Has-Service) { Yellow "服务 $SvcName 已存在，先 uninstall-service 再装"; exit 1 }

    New-Item -ItemType Directory -Force -Path $LogDir | Out-Null
    Info "注册服务 $SvcName"
    & $Nssm install $SvcName $Bin "-c" "config\nlink.yaml" | Out-Null
    & $Nssm set $SvcName AppDirectory     $InstallDir          | Out-Null
    & $Nssm set $SvcName Description      "NLink P2P tunnel daemon" | Out-Null
    & $Nssm set $SvcName Start            SERVICE_AUTO_START   | Out-Null
    & $Nssm set $SvcName AppStdout        $StdoutLog           | Out-Null
    & $Nssm set $SvcName AppStderr        $StderrLog           | Out-Null
    & $Nssm set $SvcName AppRotateFiles   1                    | Out-Null
    & $Nssm set $SvcName AppRotateOnline  1                    | Out-Null
    & $Nssm set $SvcName AppRotateBytes   10485760             | Out-Null
    Green "✓ 服务已注册"
    Info "启动服务"
    & $Nssm start $SvcName | Out-Null
    Start-Sleep -Seconds 1
    $s = Get-Service -Name $SvcName
    if ($s.Status -eq "Running") { Green "✓ 服务运行中" }
    else { Red "服务启动失败 (状态: $($s.Status))"; exit 1 }
}

function Cmd-UninstallService {
    Require-Admin
    if (-not (Has-Service)) { Yellow "服务 $SvcName 不存在"; return }
    if (-not (Has-Nssm))    { Red "找不到 $Nssm"; exit 1 }
    Info "停止服务"
    & $Nssm stop $SvcName | Out-Null
    Start-Sleep -Seconds 1
    Info "移除服务"
    & $Nssm remove $SvcName confirm | Out-Null
    Green "✓ 服务已卸载"
}

function Cmd-Update {
    Require-Admin
    Info "重新执行 install.ps1（最新版本）"
    $cmd = "[Console]::OutputEncoding=[Text.Encoding]::UTF8; iex (curl.exe -sL https://raw.githubusercontent.com/hsqbyte/nlink/master/deploy/install.ps1 | Out-String)"
    Invoke-Expression $cmd
}

function Cmd-Version {
    Ensure-Bin
    & $Bin -v
}

function Cmd-Config {
    Write-Host $ConfigFile
    if (Test-Path $ConfigFile) { Green "  存在" } else { Red "  不存在" }
}

function Usage {
@"
用法: admin.ps1 <command> [args]

服务管理:
  start              启动 nlink
  stop               停止 nlink
  restart            重启 nlink
  status             查看状态
  install-service    注册为 Windows 服务（开机自启）
  uninstall-service  卸载 Windows 服务

观察:
  logs               看最近 100 行日志
  logs -Follow       持续跟随日志

维护:
  update             升级到最新版本（重跑 install.ps1）

信息:
  version            打印 nlink 版本
  config             打印配置路径

安装目录: $InstallDir
当前模式: $(Mode)
"@ | Write-Host
}

switch ($Command.ToLower()) {
    "start"             { Cmd-Start }
    "stop"              { Cmd-Stop }
    "restart"           { Cmd-Restart }
    "status"            { Cmd-Status }
    "logs"              { Cmd-Logs }
    "log"               { Cmd-Logs }
    "install-service"   { Cmd-InstallService }
    "uninstall-service" { Cmd-UninstallService }
    "update"            { Cmd-Update }
    "upgrade"           { Cmd-Update }
    "version"           { Cmd-Version }
    "-v"                { Cmd-Version }
    "--version"         { Cmd-Version }
    "config"            { Cmd-Config }
    "help"              { Usage }
    "-h"                { Usage }
    "--help"            { Usage }
    default {
        Red "未知命令: $Command"
        Usage
        exit 1
    }
}
