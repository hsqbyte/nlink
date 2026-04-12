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
    const s = j.data.server, p = j.data.proxies || [];
    $('nav-uptime').textContent = fmtUp(s.uptime);
    $('s-uptime').textContent = fmtUp(s.uptime);
    $('s-clients').textContent = s.peer_count;
    $('s-proxies').textContent = s.proxy_count;
    $('s-active').textContent = s.active_conns;
    $('s-total').textContent = '共 ' + s.total_conns + ' 次';
    $('s-in').textContent = fmtB(s.bytes_in);
    $('s-out').textContent = fmtB(s.bytes_out);
    renderOverviewTable(p);
    renderProxyMgmt(p);
  } catch (e) { console.error(e); }
}

function renderOverviewTable(p) {
  const el = $('overview-table');
  if (!p.length) { el.innerHTML = '<div class="empty">暂无代理</div>'; return; }
  let h = '<table><thead><tr><th>名称</th><th>端口</th><th>活跃</th><th>总连接</th><th>连接池</th><th>按需</th><th>↓ 流量</th><th>↑ 流量</th></tr></thead><tbody>';
  p.forEach(x => {
    h += '<tr><td><strong>' + esc(x.name) + '</strong></td>';
    h += '<td><span class="badge badge-blue">:' + x.remote_port + '</span></td>';
    h += '<td>' + (x.active_conns ? '<span class="badge badge-green">' + x.active_conns + '</span>' : '0') + '</td>';
    h += '<td>' + x.total_conns + '</td><td>' + x.pool_hits + '</td><td>' + x.on_demand_hits + '</td>';
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
    h += '<td><span class="badge badge-blue">:' + x.remote_port + '</span></td>';
    h += '<td>' + esc(x.peer_name || x.peer_conn_id) + '</td>';
    h += '<td>' + (x.active_conns ? '<span class="badge badge-green">' + x.active_conns + ' 活跃</span>' : '<span class="badge">空闲</span>') + '</td>';
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
