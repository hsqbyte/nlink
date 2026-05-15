# nlink 一键安装脚本 (Windows / PowerShell, Win10 1803+ 自带 curl.exe)
#
# 用法（PowerShell 管理员窗口）:
#   [Console]::OutputEncoding=[Text.Encoding]::UTF8; iex (curl.exe -sL https://raw.githubusercontent.com/hsqbyte/nlink/master/deploy/install.ps1 | Out-String)
#
# 自定义版本 / 路径:
#   $env:VERSION="v2.9.4"; $env:INSTALL_DIR="D:\nlink"
#   [Console]::OutputEncoding=[Text.Encoding]::UTF8; iex (curl.exe -sL https://raw.githubusercontent.com/hsqbyte/nlink/master/deploy/install.ps1 | Out-String)
#
# 不用 iwr 的原因: PS 5.1 的 Invoke-WebRequest 对 UTF-8 响应解码不可靠，中文会乱成 ???

$ErrorActionPreference = "Stop"

# ---- 控制台 UTF-8（避免中文乱码） ----
# PS 5.1 + 中文 Windows 下，chcp/[Console]::OutputEncoding 单设一个都不够：
# 必须 P/Invoke 改 console codepage，再同步 [Console] 输出编码。
try {
    $sig = '
        [DllImport("kernel32.dll")] public static extern bool SetConsoleOutputCP(uint id);
        [DllImport("kernel32.dll")] public static extern bool SetConsoleCP(uint id);
    '
    if (-not ('NLinkConsole.Win32' -as [type])) {
        Add-Type -Namespace NLinkConsole -Name Win32 -MemberDefinition $sig | Out-Null
    }
    [NLinkConsole.Win32]::SetConsoleOutputCP(65001) | Out-Null
    [NLinkConsole.Win32]::SetConsoleCP(65001) | Out-Null
} catch {}
try {
    [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
    [Console]::InputEncoding  = [System.Text.Encoding]::UTF8
    $OutputEncoding           = [System.Text.Encoding]::UTF8
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
    # wintun.dll 必须跟 nlink.exe 同目录（VPN 需要），不带 wintun 的旧 zip 跳过
    foreach ($f in @("wintun.dll", "README.md", "LICENSE", "INSTALL.txt")) {
        $src = Join-Path $Extracted $f
        if (Test-Path $src) {
            Copy-Item -Force $src (Join-Path $InstallDir $f)
        }
    }

    # ---- 配置向导 ----
    $ConfigFile = Join-Path $InstallDir "config\nlink.yaml"

    # 默认值（向导未跑时给完成提示用）
    $Dash = "n"; $DashUser = "admin"; $DashPort = 18080
    $NlinkStarted = $false

    # iex 拉取脚本时 stdin 仍是终端；SKIP_WIZARD=1 或 stdin 被重定向时退回写示例
    $Interactive = $true
    if ($env:SKIP_WIZARD -eq "1") { $Interactive = $false }
    try { if ([System.Console]::IsInputRedirected) { $Interactive = $false } } catch {}

    function Gen-Token {
        $b = New-Object byte[] 16
        $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
        try { $rng.GetBytes($b) } finally { $rng.Dispose() }
        -join ($b | ForEach-Object { $_.ToString("x2") })
    }
    # Read-Host 在 PS 5.1 走 PSReadLine，对 ! 触发历史展开 → 含 ! 的 token 报错
    # 用 [Console]::ReadLine() 直读 stdin，跳过 PSReadLine
    function Read-Line($prompt) {
        [Console]::Write($prompt)
        [Console]::ReadLine()
    }
    function Ask($prompt, $default) {
        $hint = if ($default) { "  $prompt [$default]: " } else { "  ${prompt}: " }
        $a = Read-Line $hint
        if ([string]::IsNullOrWhiteSpace($a)) { return $default } else { return $a }
    }
    function Ask-Required($prompt) {
        while ($true) {
            $a = Read-Line "  ${prompt}: "
            if (-not [string]::IsNullOrWhiteSpace($a)) { return $a }
            Write-Host "    必填" -ForegroundColor Red
        }
    }
    function Ask-YN($prompt, $default) {
        $hint = if ($default -eq "y") { "Y/n" } else { "y/N" }
        while ($true) {
            $a = Read-Line "  $prompt ($hint): "
            if ([string]::IsNullOrWhiteSpace($a)) { $a = $default }
            switch -Regex ($a) {
                '^(y|yes)$' { return "y" }
                '^(n|no)$'  { return "n" }
                default     { Write-Host "    请输入 y 或 n" -ForegroundColor Red }
            }
        }
    }
    function Write-Yaml($path, $text) {
        # 不带 BOM 的 UTF-8（Set-Content -Encoding UTF8 在 PS 5.1 下会写 BOM）
        [System.IO.File]::WriteAllText($path, $text, (New-Object System.Text.UTF8Encoding($false)))
    }

    if (Test-Path $ConfigFile) {
        Write-Host "已存在 $ConfigFile，保留不动（跳过向导）" -ForegroundColor Yellow
    }
    elseif (-not $Interactive) {
        Write-Host "非交互环境，写入示例配置 $ConfigFile（含占位符，需手动编辑）" -ForegroundColor Yellow
        Write-Yaml $ConfigFile @"
node:
  name: "nlink-server"
  token: "CHANGE-ME"
  listen:
    port: 7100
    pool_count: 5
  dashboard:
    enabled: true
    port: 18080
    username: "admin"
    password: "CHANGE-ME"
"@
    }
    else {
        Write-Host "────────────────────────────────────────────────────────────" -ForegroundColor Cyan
        Write-Host "首次配置向导" -ForegroundColor Green
        Write-Host "（按回车采用默认值；输错可以最后再 notepad $ConfigFile 改）"
        Write-Host "nlink 是对等的：每个节点都可以同时'被别人连'和'主动连别人'，按需勾选即可。"
        Write-Host ""

        $defaultName = "nlink-$([System.Environment]::MachineName)"
        if ($defaultName.Length -gt 30) { $defaultName = $defaultName.Substring(0, 30) }
        $NodeName = Ask "节点名（全网唯一）" $defaultName

        $autoToken = Gen-Token
        Write-Host "  通信 token（多个互连节点必须用同一个；回车=自动生成强随机）"
        Write-Host "  自动生成: $autoToken"
        $Token = Ask "token" $autoToken

        Write-Host ""
        $EnableListen = Ask-YN "接受其他节点连进来（开 TCP 监听端口）？" "y"
        if ($EnableListen -eq "y") {
            $ListenPort = Ask "TCP 监听端口" "7100"
        }

        Write-Host ""
        $EnableVPN = Ask-YN "启用 VPN 虚拟局域网（节点间 10.0.0.x 直接互通，需 Wintun 驱动）？" "n"
        if ($EnableVPN -eq "y") {
            Write-Host "  本节点的虚拟 IP（CIDR 形式）"
            Write-Host "  - 全网第一个节点用 10.0.0.1/24"
            Write-Host "  - 后续节点用 10.0.0.2/24、10.0.0.3/24 …（自己挑不冲突的）"
            Write-Host "  - 或填 auto，启动时向已有节点请求 DHCP 分配"
            $VpnVip  = Ask "本节点虚拟 IP" "10.0.0.1/24"
            $VpnPort = Ask "VPN UDP 端口" "7200"
        }

        Write-Host ""
        $EnablePeer = Ask-YN "现在要主动连接另一个 nlink 节点吗？" "n"
        if ($EnablePeer -eq "y") {
            Write-Host "  加入已有 mesh：从对端 nlink.yaml 里抄 node.token 过来。" -ForegroundColor Yellow
            Write-Host "  本机 node.token 会被覆盖为这个值（mesh 内所有节点必须共用同一个 token）。" -ForegroundColor Yellow
            $PeerToken = Ask-Required "对端 node.token"
            $Token = $PeerToken
            $PeerAddr = Ask-Required "对端公网 IP / 域名"
            $PeerPort = Ask "对端 TCP 端口" "7100"
            if ($EnableVPN -eq "y") {
                $PeerVpnPort = Ask "对端 VPN UDP 端口" "7200"
                $PeerVip     = Ask "对端虚拟 IP" "10.0.0.1"
            }
        }

        Write-Host ""
        $Dash = Ask-YN "启用 Dashboard（Web 管理面板）？" "y"
        if ($Dash -eq "y") {
            $DashPort = Ask "Dashboard HTTP 端口" "18080"
            $DashUser = Ask "Dashboard 用户名" "admin"
            $autoPass = (Gen-Token).Substring(0, 16)
            Write-Host "  Dashboard 密码 (回车=自动生成: $autoPass)"
            $DashPass = Ask "密码" $autoPass
        }

        # ---- 拼 yaml ----
        $sb = New-Object System.Text.StringBuilder
        [void]$sb.AppendLine("# nlink 配置 —— 由 install.ps1 向导生成 $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')")
        [void]$sb.AppendLine("")
        [void]$sb.AppendLine("node:")
        [void]$sb.AppendLine("  name: `"$NodeName`"")
        [void]$sb.AppendLine("  token: `"$Token`"")
        if ($EnableListen -eq "y") {
            [void]$sb.AppendLine("")
            [void]$sb.AppendLine("  listen:")
            [void]$sb.AppendLine("    port: $ListenPort")
            [void]$sb.AppendLine("    pool_count: 5")
        }
        if ($Dash -eq "y") {
            [void]$sb.AppendLine("")
            [void]$sb.AppendLine("  dashboard:")
            [void]$sb.AppendLine("    enabled: true")
            [void]$sb.AppendLine("    port: $DashPort")
            [void]$sb.AppendLine("    username: `"$DashUser`"")
            [void]$sb.AppendLine("    password: `"$DashPass`"")
        }
        if ($EnableVPN -eq "y") {
            [void]$sb.AppendLine("")
            [void]$sb.AppendLine("  vpn:")
            [void]$sb.AppendLine("    enabled: true")
            [void]$sb.AppendLine("    virtual_ip: `"$VpnVip`"")
            [void]$sb.AppendLine("    listen_port: $VpnPort")
            [void]$sb.AppendLine("    mtu: 1400")
        }
        if ($EnablePeer -eq "y") {
            [void]$sb.AppendLine("")
            [void]$sb.AppendLine("peers:")
            [void]$sb.AppendLine("  - addr: `"$PeerAddr`"")
            [void]$sb.AppendLine("    port: $PeerPort")
            [void]$sb.AppendLine("    token: `"$Token`"")
            [void]$sb.AppendLine("    pool_count: 2")
            if ($EnableVPN -eq "y") {
                [void]$sb.AppendLine("    vpn_port: $PeerVpnPort")
                [void]$sb.AppendLine("    virtual_ip: `"$PeerVip`"")
            }
        }
        Write-Yaml $ConfigFile $sb.ToString()
        Write-Host "→ 已写入 $ConfigFile"

        # ---- 启动选项 ----
        Write-Host ""
        $StartNow = Ask-YN "现在启动 nlink？" "y"
        if ($StartNow -eq "y") {
            try {
                $proc = Start-Process -FilePath $TargetExe `
                                      -ArgumentList "-c", "config\nlink.yaml" `
                                      -WorkingDirectory $InstallDir `
                                      -PassThru
                Start-Sleep -Seconds 1
                if (-not $proc.HasExited) {
                    Write-Host "✓ nlink 已启动 (PID $($proc.Id))" -ForegroundColor Green
                    $NlinkStarted = $true
                } else {
                    Write-Host "服务启动失败 (exit $($proc.ExitCode))" -ForegroundColor Red
                }
            } catch {
                Write-Host "启动失败: $_" -ForegroundColor Red
            }
        }
    }
}
finally {
    Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
}

# ---- 完成提示 ----
Write-Host "────────────────────────────────────────────────────────────" -ForegroundColor Cyan
Write-Host "✓ 安装完成: $TargetExe" -ForegroundColor Green
Write-Host ""
Write-Host "已安装版本:"
try { & $TargetExe -v } catch {}
Write-Host ""

if ($NlinkStarted) {
    Write-Host "✓ nlink 已运行" -ForegroundColor Green
    if ($Dash -eq "y") {
        Write-Host "  Dashboard:  http://localhost:$DashPort  (用户名: $DashUser)" -ForegroundColor Green
    }
    Write-Host ""
    Write-Host "运维命令:" -ForegroundColor Yellow
    Write-Host "  Stop-Process -Name nlink           停止"
    Write-Host "  cd $InstallDir; .\nlink.exe -c config\nlink.yaml   重新启动"
} else {
    Write-Host "当前状态：未启动" -ForegroundColor Yellow
    if (-not (Test-Path $ConfigFile)) {
        Write-Host "  配置文件还没有，先 notepad $ConfigFile 写好" -ForegroundColor Yellow
    } elseif ((Get-Content $ConfigFile -Raw -ErrorAction SilentlyContinue) -match "CHANGE-ME") {
        Write-Host "  ⚠ 配置 $ConfigFile 含占位符，先 notepad 改一下" -ForegroundColor Yellow
    }
    Write-Host ""
    Write-Host "启动命令:" -ForegroundColor Yellow
    Write-Host "  cd $InstallDir"
    Write-Host "  .\nlink.exe -c config\nlink.yaml"
}
Write-Host ""
Write-Host "升级：重跑同一条 install.ps1 安装命令即可。"
Write-Host "────────────────────────────────────────────────────────────" -ForegroundColor Cyan
