# nlink 一键安装脚本 (Windows / PowerShell)
#
# 用法（PowerShell 管理员窗口）:
#   iwr -useb https://raw.githubusercontent.com/hsqbyte/nlink/master/deploy/install.ps1 | iex
#
# 自定义版本 / 路径:
#   $env:VERSION="v2.9.4"; $env:INSTALL_DIR="D:\nlink"
#   iwr -useb https://raw.githubusercontent.com/hsqbyte/nlink/master/deploy/install.ps1 | iex

$ErrorActionPreference = "Stop"

# ---- 控制台 UTF-8（避免中文乱码） ----
try {
    [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
    $OutputEncoding = [System.Text.Encoding]::UTF8
    chcp 65001 | Out-Null
} catch {}

# ---- 参数 ----
$Version    = if ($env:VERSION)     { $env:VERSION }     else { "latest" }
$InstallDir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { "C:\nlink" }
$Repo       = "hsqbyte/nlink"

# ---- 管理员检查 ----
$me = [Security.Principal.WindowsIdentity]::GetCurrent()
$wp = New-Object Security.Principal.WindowsPrincipal $me
if (-not $wp.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Host "请用 [管理员] PowerShell 运行此脚本" -ForegroundColor Red
    exit 1
}

# ---- 架构 ----
$archMap = @{
    "AMD64" = "amd64"
    "ARM64" = "arm64"
    "x86"   = "386"
}
$procArch = $env:PROCESSOR_ARCHITECTURE
if (-not $archMap.ContainsKey($procArch)) {
    Write-Host "不支持的架构: $procArch" -ForegroundColor Red
    exit 1
}
$Arch = $archMap[$procArch]
$Asset = "nlink-windows-$Arch.zip"

# ---- 横幅 ----
Write-Host "────────────────────────────────────────────────────────────" -ForegroundColor Cyan
Write-Host "nlink 一键安装" -ForegroundColor Green
Write-Host "  版本:     $Version"
Write-Host "  平台:     windows/$Arch"
Write-Host "  安装到:   $InstallDir"
Write-Host "────────────────────────────────────────────────────────────" -ForegroundColor Cyan

# ---- 准备目录 ----
$null = New-Item -ItemType Directory -Force -Path $InstallDir
$null = New-Item -ItemType Directory -Force -Path (Join-Path $InstallDir "config")
$null = New-Item -ItemType Directory -Force -Path (Join-Path $InstallDir "data")

# ---- 下载 URL ----
if ($Version -eq "latest") {
    $Url = "https://github.com/$Repo/releases/latest/download/$Asset"
} else {
    $Url = "https://github.com/$Repo/releases/download/$Version/$Asset"
}

$Tmp = New-Item -ItemType Directory -Force -Path (Join-Path $env:TEMP "nlink-install-$([System.Guid]::NewGuid().ToString('N'))")
try {
    $Zip = Join-Path $Tmp "nlink.zip"

    Write-Host "→ 下载 $Url"
    Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $Zip

    Write-Host "→ 解包"
    Expand-Archive -LiteralPath $Zip -DestinationPath $Tmp -Force
    $Extracted = Join-Path $Tmp "nlink-windows-$Arch"
    if (-not (Test-Path $Extracted)) {
        Write-Host "压缩包结构异常" -ForegroundColor Red
        exit 1
    }

    # ---- 停旧进程 ----
    $running = Get-Process -Name "nlink" -ErrorAction SilentlyContinue
    if ($running) {
        Write-Host "→ 停止运行中的 nlink"
        $running | Stop-Process -Force
        Start-Sleep -Seconds 1
    }

    # ---- 备份 + 安装 ----
    $TargetExe = Join-Path $InstallDir "nlink.exe"
    if (Test-Path $TargetExe) {
        $stamp = Get-Date -Format "yyyyMMdd-HHmmss"
        Copy-Item -Force $TargetExe "$TargetExe.bak.$stamp"
        Write-Host "→ 已备份旧二进制 → nlink.exe.bak.$stamp"
    }

    Write-Host "→ 安装二进制 + 文档"
    Copy-Item -Force (Join-Path $Extracted "nlink.exe") $TargetExe
    foreach ($f in @("README.md", "LICENSE", "INSTALL.txt")) {
        $src = Join-Path $Extracted $f
        if (Test-Path $src) {
            Copy-Item -Force $src (Join-Path $InstallDir $f)
        }
    }

    # ---- 示例配置（首次安装才写） ----
    $ConfigFile = Join-Path $InstallDir "config\nlink.yaml"
    if (-not (Test-Path $ConfigFile)) {
        Write-Host "→ 写入示例配置 $ConfigFile"
        $yaml = @'
# nlink 配置示例 —— 编辑后再启动
# 这是一个"服务器节点"模板：监听 :7100 + 启用 Dashboard。
# 客户端节点请删 listen 块，加 peers 段。

node:
  name: "nlink-server"
  token: "CHANGE-ME-把这串改成强随机字符串"

  listen:
    port: 7100
    pool_count: 5

  dashboard:
    enabled: true
    port: 18080
    username: "admin"
    password: "CHANGE-ME-改成强密码"

  # VPN 组网（需要 Wintun 驱动）取消注释:
  # vpn:
  #   enabled: true
  #   virtual_ip: "10.0.0.1/24"
  #   listen_port: 7200
  #   mtu: 1400
'@
        Set-Content -Path $ConfigFile -Value $yaml -Encoding UTF8
    } else {
        Write-Host "已存在 $ConfigFile，保留不动" -ForegroundColor Yellow
    }
}
finally {
    Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
}

# ---- 完成提示 ----
Write-Host "────────────────────────────────────────────────────────────" -ForegroundColor Cyan
Write-Host "✓ 安装完成: $TargetExe" -ForegroundColor Green
Write-Host ""
try { & $TargetExe -v } catch {}
Write-Host ""
Write-Host "下一步:" -ForegroundColor Yellow
Write-Host "  1. 编辑配置:"
Write-Host "     notepad $ConfigFile"
Write-Host ""
Write-Host "  2. 启动:"
Write-Host "     cd $InstallDir"
Write-Host "     .\nlink.exe -c config\nlink.yaml"
Write-Host ""
Write-Host "  3. 以后升级，直接重跑这个 install.ps1 命令"
Write-Host ""
Write-Host "  Dashboard: http://localhost:18080  (默认 admin / CHANGE-ME, 用 yaml 里改后的密码)"
Write-Host "────────────────────────────────────────────────────────────" -ForegroundColor Cyan
