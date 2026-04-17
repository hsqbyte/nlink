// ---- Auto Refresh with Countdown ----
const INTERVAL = 5;
let countdown = INTERVAL;
let refreshTimer = null;
const arcLen = 2 * Math.PI * 8; // ~50.265

function updateArc() {
  const pct = countdown / INTERVAL;
  $('refresh-arc').style.strokeDashoffset = arcLen * (1 - pct);
  $('refresh-countdown').textContent = countdown;
}

function tickRefresh() {
  countdown--;
  if (countdown <= 0) { refresh(); countdown = INTERVAL; }
  updateArc();
}

function manualRefresh() {
  refresh(); countdown = INTERVAL; updateArc();
}

function startRefreshTimer() {
  if (refreshTimer) clearInterval(refreshTimer);
  countdown = INTERVAL; updateArc();
  refreshTimer = setInterval(tickRefresh, 1000);
}

// ---- Data Refresh ----
async function refresh() {
  try {
    const r = await fetch('/api/v1/stats');
    const j = await r.json();
    if (j.code !== 200) return;
    applyStats(j.data);
  } catch (e) { console.error(e); }
}

// applyStats 接受 /stats 或 SSE 推送过来的数据并更新 UI
function applyStats(data) {
  if (!data) return;
  const s = data.server, p = data.proxies || [];
  $('nav-uptime').textContent = fmtUp(s.uptime);
  $('s-uptime').textContent = fmtUp(s.uptime);
  $('s-clients').textContent = s.peer_count;
  $('s-proxies').textContent = s.proxy_count;
  $('s-active').textContent = s.active_conns;
  $('s-total').textContent = '共 ' + s.total_conns + ' 次';
  $('s-in').textContent = fmtB(s.bytes_in);
  $('s-out').textContent = fmtB(s.bytes_out);
  // VPN 信息
  const vpn = data.vpn;
  if (vpn && vpn.enabled) {
    $('vpn-card').style.display = '';
    $('s-vpn-ip').textContent = vpn.virtual_ip;
    let portInfo = 'UDP :' + vpn.listen_port;
    if (vpn.public_addr) portInfo += ' | 公网 ' + vpn.public_addr;
    if ($('s-vpn-port')) $('s-vpn-port').textContent = portInfo;
    // VPN 对端列表
    const peerEl = $('s-vpn-peers');
    if (peerEl && vpn.peers && vpn.peers.length > 0) {
      peerEl.innerHTML = vpn.peers.map(p => {
        const routes = (p.routes && p.routes.length) ? ' routes=' + p.routes.length : '';
        const rtt = (p.rtt_ms && p.rtt_ms > 0) ? ' rtt=' + p.rtt_ms.toFixed(1) + 'ms' : '';
        const rx = p.rx_bytes ? ' ↓' + fmtB(p.rx_bytes) : '';
        const tx = p.tx_bytes ? ' ↑' + fmtB(p.tx_bytes) : '';
        const safe = JSON.stringify(p).replace(/"/g,'&quot;');
        return '<div class="vpn-peer-row" style="display:flex;align-items:center;gap:8px;padding:4px 0">' +
               '<span style="flex:1">' + esc(p.virtual_ip) + ' <span style="opacity:.6">(' + esc(p.endpoint) + ')' + routes + rtt + rx + tx + '</span></span>' +
               '<button class="btn btn-ghost" style="font-size:11px;padding:2px 8px" onclick="vpnEditPeerPolicy(' + safe + ')">编辑策略</button>' +
               '</div>';
      }).join('');
      peerEl.style.display = '';
    } else if (peerEl) {
      peerEl.style.display = 'none';
    }
  } else {
    $('vpn-card').style.display = 'none';
  }
  renderOverviewTable(p);
  renderProxyMgmt(p);
}
window.applyStats = applyStats;

function renderOverviewTable(p) {
  const el = $('overview-table');
  if (!p.length) { el.innerHTML = '<div class="empty">暂无代理</div>'; return; }
  let h = '<table><thead><tr><th>名称</th><th>端口</th><th>活跃</th><th>总连接</th><th>连接池</th><th>按需</th><th>↓ 流量</th><th>↑ 流量</th></tr></thead><tbody>';
  p.forEach(x => {
    h += '<tr><td><strong>' + esc(x.name) + '</strong></td>';
    h += '<td><span class="badge badge-blue">:' + esc(x.remote_port) + '</span></td>';
    h += '<td>' + (x.active_conns ? '<span class="badge badge-green">' + esc(x.active_conns) + '</span>' : '0') + '</td>';
    h += '<td>' + esc(x.total_conns) + '</td><td>' + esc(x.pool_hits) + '</td><td>' + esc(x.on_demand_hits) + '</td>';
    h += '<td>' + fmtB(x.bytes_in) + '</td><td>' + fmtB(x.bytes_out) + '</td></tr>';
  });
  el.innerHTML = h + '</tbody></table>';
}

function renderProxyMgmt(p) {
  const el = $('proxy-mgmt');
  if (!p.length) { el.innerHTML = '<div class="empty">暂无代理</div>'; return; }
  let h = '<table><thead><tr><th>名称</th><th>端口</th><th>节点</th><th>状态</th><th>操作</th></tr></thead><tbody>';
  p.forEach(x => {
    const n = esc(x.name);
    h += '<tr><td><strong>' + n + '</strong></td>';
    h += '<td><span class="badge badge-blue">:' + esc(x.remote_port) + '</span></td>';
    h += '<td>' + esc(x.peer_name || x.peer_conn_id) + '</td>';
    h += '<td>' + (x.active_conns ? '<span class="badge badge-green">' + esc(x.active_conns) + ' 活跃</span>' : '<span class="badge">空闲</span>') + '</td>';
    h += '<td><button class="btn btn-red" onclick="removeProxy(this,\'' + n + '\')"><span class="spinner"></span><span class="btn-text">删除</span></button></td></tr>';
  });
  el.innerHTML = h + '</tbody></table>';
}

// ---- Proxy Management ----
async function removeProxy(btn, name) {
  const ok = await showConfirm('删除代理', '确定删除代理 "' + name + '" ？此操作将关闭该端口的所有连接。', true);
  if (!ok) return;
  btnLoading(btn, true);
  try {
    const r = await fetch('/api/v1/proxies/' + encodeURIComponent(name), { method: 'DELETE' });
    const j = await r.json(); toast(j.message, j.code === 200); refresh();
  } finally { btnLoading(btn, false); }
}

// 编辑 VPN 对端路由 / ACL 策略
async function vpnEditPeerPolicy(p) {
  const vip = p.virtual_ip;
  const curRoutes = (p.routes || []).join(',');
  const routes = prompt('对端 ' + vip + ' 的路由 CIDR（逗号分隔，留空清除）:', curRoutes);
  if (routes === null) return;
  const allow = prompt('允许 CIDR（逗号分隔，留空清除）:', '');
  if (allow === null) return;
  const deny = prompt('拒绝 CIDR（逗号分隔，留空清除）:', '');
  if (deny === null) return;
  const splitList = s => s ? s.split(',').map(x => x.trim()).filter(Boolean) : [];
  try {
    const r = await fetch('/api/v1/vpn/peers/' + encodeURIComponent(vip) + '/policy', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        endpoint: p.endpoint,
        routes: splitList(routes),
        allow_cidr: splitList(allow),
        deny_cidr: splitList(deny),
      }),
    });
    const j = await r.json();
    toast(j.message, j.code === 200);
    if (j.code === 200) refresh();
  } catch (e) { toast('操作失败: ' + e, false); }
}
window.vpnEditPeerPolicy = vpnEditPeerPolicy;
