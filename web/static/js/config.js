// ---- Node Config ----
// 缓存真实 token（仅在内存中，不写入 DOM.value，避免被扩展/截图泄漏）
let _currentToken = '';
let _tokenRevealed = false;

async function loadConfig() {
  const r = await fetch('/api/v1/node/config');
  const j = await r.json();
  if (j.code !== 200) return;
  const d = j.data;
  _currentToken = d.token || '';
  _tokenRevealed = false;
  // token 输入框默认空 + placeholder，不把明文写入 DOM.value
  const tokenEl = $('cfg-token');
  if (tokenEl) {
    tokenEl.value = '';
    tokenEl.placeholder = _currentToken ? mask(_currentToken) + '（如需修改请输入新 token）' : '未配置';
  }
  $('cfg-heartbeat').value = d.heartbeat_timeout || '';
  $('cfg-max-proxies').value = d.max_proxies_per_peer || '';
  $('cfg-work-timeout').value = d.work_conn_timeout || '';
  $('cfg-pool-count').value = d.pool_count != null ? d.pool_count : 5;
  $('cfg-port').value = d.dashboard_port || '';
  $('cfg-tcp-port').value = d.listen_port || '';
  $('cfg-msg-size').value = d.max_message_size || '';
  $('cfg-shutdown').value = d.shutdown_timeout || '';
  renderConnConfig(d);
}

function renderConnConfig(d) {
  const addr = window.location.hostname;
  const port = d.listen_port || 7100;
  // 默认显示掩码；用户点击 "显示 Token" 才渲染真实值
  const token = _tokenRevealed ? (_currentToken || 'my-secret-token') : (_currentToken ? mask(_currentToken) : 'my-secret-token');
  const pool = d.pool_count != null ? d.pool_count : 5;
  const yaml = `peers:
  - addr: "${addr}"
    port: ${port}
    token: "${token}"
    pool_count: ${pool}
    proxies:
      - name: "example"
        type: "tcp"
        local_ip: "127.0.0.1"
        local_port: 8080
        remote_port: 9080`;
  // textContent 安全，无 XSS 风险
  $('conn-config-preview').textContent = yaml;
}

// 切换预览中 token 的显示/掩码
function toggleTokenReveal() {
  _tokenRevealed = !_tokenRevealed;
  // 用最近一次 loadConfig 的配置重渲染（从缓存构造最小对象即可）
  const d = {
    listen_port: parseInt($('cfg-tcp-port').value) || 7100,
    pool_count: parseInt($('cfg-pool-count').value),
  };
  renderConnConfig(d);
}

async function copyConnConfig() {
  // 复制时使用真实 token（掩码版本复制出去对客户端无用）
  const addr = window.location.hostname;
  const port = parseInt($('cfg-tcp-port').value) || 7100;
  const pool = parseInt($('cfg-pool-count').value);
  const tok = _currentToken || 'my-secret-token';
  const text = `peers:
  - addr: "${addr}"
    port: ${port}
    token: "${tok}"
    pool_count: ${isNaN(pool) ? 5 : pool}
    proxies:
      - name: "example"
        type: "tcp"
        local_ip: "127.0.0.1"
        local_port: 8080
        remote_port: 9080`;
  try {
    await navigator.clipboard.writeText(text);
    toast('已复制到剪贴板（含真实 token）', true);
  } catch (e) {
    toast('复制失败', false);
  }
}

async function saveConfig() {
  const btn = event.target.closest('.btn');
  btnLoading(btn, true);
  try {
    const body = {};
    const t = $('cfg-token').value; if (t) body.token = t;
    const h = parseInt($('cfg-heartbeat').value); if (h) body.heartbeat_timeout = h;
    const m = parseInt($('cfg-max-proxies').value); if (m) body.max_proxies_per_peer = m;
    const w = parseInt($('cfg-work-timeout').value); if (w) body.work_conn_timeout = w;
    const pc = parseInt($('cfg-pool-count').value); if (!isNaN(pc)) body.pool_count = pc;
    const r = await fetch('/api/v1/node/config', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    const j = await r.json(); toast(j.message, j.code === 200);
  } finally { btnLoading(btn, false); }
}
