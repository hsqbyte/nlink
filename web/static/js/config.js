// ---- Node Config ----
async function loadConfig() {
  const r = await fetch('/api/v1/node/config');
  const j = await r.json();
  if (j.code !== 200) return;
  const d = j.data;
  $('cfg-token').value = d.token || '';
  $('cfg-heartbeat').value = d.heartbeat_timeout || '';
  $('cfg-max-proxies').value = d.max_proxies_per_peer || '';
  $('cfg-work-timeout').value = d.work_conn_timeout || '';
  $('cfg-pool-count').value = d.pool_count != null ? d.pool_count : 5;
  $('cfg-port').value = d.dashboard_port || '';
  $('cfg-tcp-port').value = d.listen_port || '';
  $('cfg-msg-size').value = d.max_message_size || '';
  $('cfg-shutdown').value = d.shutdown_timeout || '';
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
