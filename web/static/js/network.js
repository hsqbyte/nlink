// ---- Network State ----
let netNodes = null;       // vis.DataSet
let netEdges = null;       // vis.DataSet
let netGraph = null;       // vis.Network instance
let netNodeData = {};      // id → { type, info, proxies, gateway, path }
let netSelectedId = null;

const NET_COLORS = {
  self:   { background: '#0071E3', border: '#005BB5', font: '#fff', highlight: { background: '#339AF0', border: '#0071E3' } },
  peer:   { background: '#34C759', border: '#28A746', font: '#fff', highlight: { background: '#5BD778', border: '#34C759' } },
  peerOff:{ background: '#FF3B30', border: '#D32F2F', font: '#fff', highlight: { background: '#FF6B60', border: '#FF3B30' } },
  remote: { background: '#8E8E93', border: '#6D6D72', font: '#fff', highlight: { background: '#A8A8AD', border: '#8E8E93' } },
  upstream:    { background: '#34C759', border: '#28A746', font: '#fff', highlight: { background: '#5BD778', border: '#34C759' } },
  upstreamOff: { background: '#FF3B30', border: '#D32F2F', font: '#fff', highlight: { background: '#FF6B60', border: '#FF3B30' } }
};

// ---- Init ----
async function initNetwork() {
  netCloseDetail();
  netSelectedId = null;
  try {
    const r = await fetch('/api/v1/stats');
    const j = await r.json();
    if (j.code !== 200) { toast('加载失败', false); return; }
    const s = j.data.server;
    const nodeName = j.data.node_name || 'Node';
    const peers = j.data.peers || [];
    const upstream = j.data.upstream || [];

    // Build node/edge data
    const nodes = [];
    const edges = [];
    netNodeData = {};

    // Self node
    const selfProxies = j.data.proxies || [];
    nodes.push({
      id: 'self', label: nodeName, shape: 'circle', size: 40,
      color: NET_COLORS.self, font: { color: '#fff', size: 14 }
    });
    netNodeData['self'] = {
      type: 'server', info: {
        uptime: s.uptime, peer_count: s.peer_count, proxy_count: s.proxy_count,
        active_conns: s.active_conns, total_conns: s.total_conns, bytes_in: s.bytes_in, bytes_out: s.bytes_out
      },
      proxies: selfProxies, gateway: null, path: [], label: nodeName
    };

    // Upstream peers (nodes this instance connects to)
    upstream.forEach(u => {
      const uid = 'upstream-' + u.addr + ':' + u.port;
      const label = u.name || (u.addr + ':' + u.port);
      nodes.push({
        id: uid, label: label, shape: 'circle', size: 35,
        color: u.connected ? NET_COLORS.upstream : NET_COLORS.upstreamOff,
        font: { color: '#fff', size: 13 }
      });
      const edgeLabel = (u.proxies || []).map(p => p).join(', ');
      edges.push({
        from: 'self', to: uid, width: 2, arrows: { to: { enabled: true, scaleFactor: 0.6 } },
        color: { color: u.connected ? '#34C759' : '#FF3B30', highlight: '#34C759' },
        label: edgeLabel,
        font: { size: 10, color: '#8E8E93', strokeWidth: 3, strokeColor: '#fff', align: 'middle' },
        dashes: u.connected ? false : [5, 5]
      });
      netNodeData[uid] = {
        type: 'upstream', info: { addr: u.addr, port: u.port, connected: u.connected, proxies_names: u.proxies || [] },
        proxies: [], gateway: null, path: [], label: label
      };
    });

    // Direct peers
    peers.forEach(c => {
      const pid = c.name || c.conn_id;
      const label = c.name || '#' + c.conn_id;
      const proxyNames = c.proxies || [];
      const isConnected = c.connected !== false;
      nodes.push({
        id: pid, label: label, shape: 'circle', size: 32,
        color: isConnected ? NET_COLORS.peer : NET_COLORS.peerOff,
        font: { color: '#fff', size: 13 }
      });
      // Edge label shows proxy ports
      const edgeLabel = netEdgeLabel(selfProxies, pid);
      edges.push({
        from: 'self', to: pid, width: 2,
        color: { color: isConnected ? '#34C759' : '#FF3B30', highlight: '#0071E3' },
        label: edgeLabel,
        font: { size: 10, color: '#8E8E93', strokeWidth: 3, strokeColor: '#fff', align: 'middle', multi: false },
        dashes: isConnected ? false : [5, 5]
      });
      netNodeData[pid] = {
        type: 'peer', info: { proxies_names: proxyNames, connected: isConnected }, proxies: [],
        gateway: null, path: [], label: label
      };
    });

    if (!netNodes) {
      // First init
      netNodes = new vis.DataSet(nodes);
      netEdges = new vis.DataSet(edges);
      const container = $('net-graph');
      netGraph = new vis.Network(container, { nodes: netNodes, edges: netEdges }, {
        physics: {
          solver: 'forceAtlas2Based',
          forceAtlas2Based: { gravitationalConstant: -100, centralGravity: 0.01, springLength: 160, springConstant: 0.06 },
          stabilization: { iterations: 100, fit: true }
        },
        interaction: { hover: true, tooltipDelay: 300, zoomView: true, dragView: true },
        nodes: { borderWidth: 2, shadow: { enabled: true, color: 'rgba(0,0,0,0.1)', size: 6, x: 0, y: 2 } },
        edges: { smooth: { type: 'continuous' } }
      });
      netGraph.on('click', function(params) {
        if (params.nodes.length > 0) {
          selectNetNode(params.nodes[0]);
        }
      });
      netGraph.on('doubleClick', function(params) {
        if (params.nodes.length > 0) {
          toggleNetNode(params.nodes[0]);
        }
      });
    } else {
      // Refresh: update datasets
      netNodes.clear();
      netEdges.clear();
      netNodes.add(nodes);
      netEdges.add(edges);
      netGraph.fit({ animation: { duration: 300 } });
    }
  } catch (e) { toast('加载失败: ' + (e.message || e), false); }
}

function refreshNetwork() { initNetwork(); }

// ---- Graph Interaction ----
async function toggleNetNode(id) {
  const nd = netNodeData[id];
  if (!nd || nd.type === 'server' || nd.type === 'upstream') return;

  // Check if already expanded (has children edges)
  const connEdges = netEdges.get({ filter: e => e.from === id });
  if (connEdges.length > 0) {
    // Collapse: remove children nodes & edges
    connEdges.forEach(e => {
      removeSubtree(e.to);
      netEdges.remove(e.id);
    });
    return;
  }

  // Load children
  try {
    let children = [];
    if (!nd.gateway) {
      const r = await fetch('/api/v1/peers/' + encodeURIComponent(id) + '/peers');
      const j = await r.json();
      if (j.code === 200 && j.data) children = j.data;
    } else {
      const j = await netForwardAPI(nd.gateway, id, nd.path, 'get_peers');
      if (j.code === 200 && j.data) children = j.data;
    }
    if (!children.length) { toast('该节点无下游节点', 'warn'); return; }

    children.forEach(c => {
      const cid = c.name || c.conn_id;
      const clabel = c.name || '#' + c.conn_id;
      const cProxies = c.proxies || [];
      if (!netNodeData[cid]) {
        netNodes.add({
          id: cid, label: clabel, shape: 'circle', size: 26,
          color: NET_COLORS.remote, font: { color: '#fff', size: 11 }
        });
        netNodeData[cid] = {
          type: 'peer', info: { proxies_names: cProxies }, proxies: [],
          gateway: nd.gateway || id,
          path: nd.gateway ? [...nd.path, id] : [],
          label: clabel
        };
      }
      const edgeLabel = cProxies.length ? cProxies.join(', ') : '';
      netEdges.add({
        from: id, to: cid, width: 1.5, dashes: true,
        color: { color: '#D1D1D6', highlight: '#34C759' },
        label: edgeLabel,
        font: { size: 9, color: '#A0A0A5', strokeWidth: 2, strokeColor: '#fff', align: 'middle' }
      });
    });
  } catch (e) { toast('加载子节点失败', false); }
}

function removeSubtree(nodeId) {
  const childEdges = netEdges.get({ filter: e => e.from === nodeId });
  childEdges.forEach(e => {
    removeSubtree(e.to);
    netEdges.remove(e.id);
  });
  netNodes.remove(nodeId);
  delete netNodeData[nodeId];
}

function selectNetNode(id) {
  netSelectedId = id;
  loadNetDetail(id);
}

function netCloseDetail() {
  const card = $('net-detail');
  card.classList.remove('show');
  card.innerHTML = '';
  netSelectedId = null;
}

function netShowDetail(html) {
  const card = $('net-detail');
  card.innerHTML = '<button class="net-detail-close" onclick="netCloseDetail()">×</button>' + html;
  card.classList.add('show');
}

function netGetDepth(id) {
  const nd = netNodeData[id];
  if (!nd) return -1;
  if (nd.type === 'server') return 0;
  if (!nd.gateway) return 1;
  return nd.path.length + 2;
}

async function netForwardAPI(gateway, targetId, path, cmd, data) {
  const body = { target_id: targetId, cmd: cmd };
  if (path && path.length) body.path = path;
  if (data !== undefined) body.data = data;
  const r = await fetch('/api/v1/peers/' + encodeURIComponent(gateway) + '/forward',
    { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  return r.json();
}

// ---- Detail Panel ----
async function loadNetDetail(id) {
  const nd = netNodeData[id];
  if (!nd) return;
  const detail = $('net-detail');

  if (nd.type === 'server') {
    renderServerDetail(nd);
    return;
  }

  if (nd.type === 'upstream') {
    renderUpstreamDetail(id, nd);
    return;
  }

  // 断开的节点直接渲染，不尝试获取配置
  if (nd.info.connected === false) {
    renderPeerDetail(id, nd);
    return;
  }

  detail.innerHTML = '<div class="net-detail-empty">加载中...</div>';
  detail.classList.add('show');
  try {
    let cfg = null;
    if (!nd.gateway) {
      const r = await fetch('/api/v1/peers/' + encodeURIComponent(id) + '/config');
      const j = await r.json();
      if (j.code === 200) cfg = j.data;
    } else {
      const j = await netForwardAPI(nd.gateway, id, nd.path, 'get_config');
      if (j.code === 200) cfg = j.data;
    }
    if (cfg) {
      nd.info.peer_addr = cfg.peer_addr;
      nd.info.peer_port = cfg.peer_port;
      nd.info.pool_count = cfg.pool_count;
      nd.proxies = cfg.proxies || [];
    }
    renderPeerDetail(id, nd);
  } catch (e) {
    netShowDetail('<div class="net-detail-empty">加载失败</div>');
    toast('加载节点信息失败', false);
  }
}

function renderServerDetail(nd) {
  const i = nd.info;
  let h = '<div class="net-detail-header"><h2>' + esc(nd.label) + '</h2><span class="badge badge-green">运行中</span></div>';
  h += '<div class="net-info-grid">' +
    netInfoItem('运行时间', fmtUp(i.uptime)) +
    netInfoItem('在线节点', i.peer_count) +
    netInfoItem('活跃代理', i.proxy_count) +
    netInfoItem('活跃连接', i.active_conns) +
    netInfoItem('↓ 流量', fmtB(i.bytes_in)) +
    netInfoItem('↑ 流量', fmtB(i.bytes_out)) +
  '</div>';
  h += '<div class="net-sep"></div>';
  h += '<div class="net-section"><div class="net-section-header"><h3>代理列表 (' + (nd.proxies || []).length + ')</h3></div>';
  h += renderServerProxies(nd.proxies);
  h += '</div>';
  netShowDetail(h);
}

function renderUpstreamDetail(id, nd) {
  const i = nd.info;
  const statusBadge = i.connected
    ? '<span class="badge badge-green">已连接</span>'
    : '<span class="badge badge-red">已断开</span>';
  let h = '<div class="net-detail-header"><h2>上游 ' + esc(nd.label) + '</h2>' + statusBadge + '</div>';
  h += '<div class="net-info-grid">';
  h += netInfoItem('地址', i.addr + ':' + i.port);
  h += netInfoItem('状态', i.connected ? '已连接' : '已断开');
  h += netInfoItem('代理数', (i.proxies_names || []).length);
  h += '</div>';
  if (i.proxies_names && i.proxies_names.length) {
    h += '<div class="net-sep"></div>';
    h += '<div class="net-section"><div class="net-section-header"><h3>注册代理 (' + i.proxies_names.length + ')</h3></div>';
    h += '<div class="net-proxy-list">';
    i.proxies_names.forEach(function(n) {
      h += '<div class="net-proxy-card"><div><div class="net-proxy-name">' + esc(n) + '</div></div></div>';
    });
    h += '</div></div>';
  }
  netShowDetail(h);
}

function renderPeerDetail(id, nd) {
  const i = nd.info;
  const depth = netGetDepth(id);
  const isConnected = i.connected !== false;
  const statusBadge = isConnected
    ? '<span class="badge badge-green">在线</span>'
    : '<span class="badge badge-red">已断开</span>';

  let h = '<div class="net-detail-header"><h2>节点 ' + esc(nd.label) + '</h2>' + statusBadge + '</div>';
  h += '<div class="net-info-grid">';
  h += netInfoItem('节点名称', id);
  if (i.peer_addr) h += netInfoItem('上游地址', i.peer_addr + ':' + i.peer_port);
  if (i.pool_count !== undefined) h += netInfoItem('连接池', i.pool_count);
  h += netInfoItem('代理数', nd.proxies ? nd.proxies.length : 0);
  h += '</div>';
  h += '<div class="net-sep"></div>';

  h += '<div class="net-section"><div class="net-section-header"><h3>代理 (' + (nd.proxies || []).length + ')</h3>' +
    '<button class="btn btn-blue" style="padding:4px 12px;font-size:12px" onclick="netToggleAddProxy()"><span class="btn-text">+ 添加</span></button>' +
    '</div>';
  h += renderPeerProxies(nd.proxies);
  h += renderNetAddForm();
  h += '</div>';

  h += '<div class="net-actions">';
  h += '<button class="btn btn-ghost" onclick="netUpdatePool(this)"><span class="spinner"></span><span class="btn-text">修改连接池</span></button>';
  if (depth === 1) h += '<button class="btn btn-red" onclick="netKickNode(this)"><span class="spinner"></span><span class="btn-text">断开节点</span></button>';
  h += '</div>';

  netShowDetail(h);
}

function netInfoItem(label, value) {
  return '<div class="net-info-item"><span class="label">' + esc(String(label)) + '</span><span class="value">' + esc(String(value)) + '</span></div>';
}

function renderServerProxies(proxies) {
  if (!proxies || !proxies.length) return '<div class="empty" style="padding:16px;font-size:13px">暂无代理</div>';
  let h = '<div class="net-proxy-list">';
  proxies.forEach(p => {
    h += '<div class="net-proxy-card"><div>' +
      '<div class="net-proxy-name">' + esc(p.name) + '</div>' +
      '<div class="net-proxy-addr">:' + p.remote_port + '</div>' +
      '<div class="net-proxy-stats">' + (p.active_conns || 0) + ' 活跃 · ' + p.total_conns + ' 总连接 · ↓' + fmtB(p.bytes_in) + ' ↑' + fmtB(p.bytes_out) + '</div>' +
    '</div></div>';
  });
  return h + '</div>';
}

function renderPeerProxies(proxies) {
  if (!proxies || !proxies.length) return '<div class="empty" style="padding:16px;font-size:13px">暂无代理</div>';
  let h = '<div class="net-proxy-list">';
  proxies.forEach(p => {
    const n = esc(p.name);
    h += '<div class="net-proxy-card"><div>' +
      '<div class="net-proxy-name">' + n + '</div>' +
      '<div class="net-proxy-addr">:' + p.remote_port + ' → ' + esc(p.local_ip || '127.0.0.1') + ':' + p.local_port + '</div>' +
    '</div>' +
    '<button class="net-proxy-del" onclick="netRemoveProxy(\'' + n + '\')" title="删除">×</button>' +
    '</div>';
  });
  return h + '</div>';
}

function renderNetAddForm() {
  return '<div class="net-add-form" id="net-add-form">' +
    '<div class="form-grid">' +
      '<div class="form-item"><label>名称</label><input id="net-ap-name" type="text" placeholder="my-proxy"></div>' +
      '<div class="form-item"><label>远程端口</label><input id="net-ap-rp" type="number" placeholder="9080"></div>' +
      '<div class="form-item"><label>本地IP</label><input id="net-ap-lip" type="text" value="127.0.0.1"></div>' +
      '<div class="form-item"><label>本地端口</label><input id="net-ap-lp" type="number" placeholder="8080"></div>' +
    '</div>' +
    '<div class="net-add-form-actions">' +
      '<button class="btn btn-ghost" onclick="netToggleAddProxy()"><span class="btn-text">取消</span></button>' +
      '<button class="btn btn-blue" onclick="netSubmitAddProxy(this)"><span class="spinner"></span><span class="btn-text">确定</span></button>' +
    '</div></div>';
}

// ---- Edge Label Builder ----
function netEdgeLabel(proxies, peerId) {
  if (!proxies || !proxies.length) return '';
  // Filter proxies belonging to this peer
  const matched = proxies.filter(p => (p.peer_name || p.peer_conn_id) === peerId);
  if (!matched.length) return '';
  return matched.map(p => p.name + ' :' + p.remote_port).join('\n');
}

// ---- Actions ----
function netToggleAddProxy() {
  const f = $('net-add-form');
  if (f) f.classList.toggle('show');
}

async function netSubmitAddProxy(btn) {
  const name = $('net-ap-name').value.trim();
  const rp = parseInt($('net-ap-rp').value);
  const lip = $('net-ap-lip').value.trim() || '127.0.0.1';
  const lp = parseInt($('net-ap-lp').value);
  if (!name || !rp || !lp) { toast('请填写完整', 'warn'); return; }

  const nd = netNodeData[netSelectedId];
  if (!nd) return;
  btnLoading(btn, true);
  try {
    let j;
    const data = { name, type: 'tcp', remote_port: rp, local_ip: lip, local_port: lp };
    if (!nd.gateway) {
      const r = await fetch('/api/v1/peers/' + encodeURIComponent(netSelectedId) + '/proxies',
        { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(data) });
      j = await r.json();
    } else {
      j = await netForwardAPI(nd.gateway, netSelectedId, nd.path, 'add_proxy', data);
    }
    toast(j.message, j.code === 200);
    if (j.code === 200) { await loadNetDetail(netSelectedId); refresh(); }
  } finally { btnLoading(btn, false); }
}

async function netRemoveProxy(name) {
  const nd = netNodeData[netSelectedId];
  if (!nd) return;
  const ok = await showConfirm('删除代理', '确定删除代理 "' + name + '" ？', true);
  if (!ok) return;
  try {
    let j;
    if (!nd.gateway) {
      const r = await fetch('/api/v1/peers/' + encodeURIComponent(netSelectedId) + '/proxies/' + encodeURIComponent(name), { method: 'DELETE' });
      j = await r.json();
    } else {
      j = await netForwardAPI(nd.gateway, netSelectedId, nd.path, 'remove_proxy', { name });
    }
    toast(j.message, j.code === 200);
    if (j.code === 200) { await loadNetDetail(netSelectedId); refresh(); }
  } catch (e) { toast('操作失败', false); }
}

async function netUpdatePool(btn) {
  const nd = netNodeData[netSelectedId];
  if (!nd) return;
  const val = prompt('设置连接池大小 (0=禁用):');
  if (val === null) return;
  const pc = parseInt(val);
  if (isNaN(pc) || pc < 0) { toast('请输入有效数字', 'warn'); return; }
  btnLoading(btn, true);
  try {
    let j;
    if (!nd.gateway) {
      const r = await fetch('/api/v1/peers/' + encodeURIComponent(netSelectedId) + '/pool',
        { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ pool_count: pc }) });
      j = await r.json();
    } else {
      j = await netForwardAPI(nd.gateway, netSelectedId, nd.path, 'update_pool', { pool_count: pc });
    }
    toast(j.message, j.code === 200);
    if (j.code === 200) await loadNetDetail(netSelectedId);
  } finally { btnLoading(btn, false); }
}

async function netKickNode(btn) {
  const nd = netNodeData[netSelectedId];
  if (!nd) return;
  const ok = await showConfirm('断开节点', '确定断开 ' + nd.label + ' ？所有代理将被移除。', true);
  if (!ok) return;
  btnLoading(btn, true);
  try {
    const r = await fetch('/api/v1/peers/' + encodeURIComponent(netSelectedId), { method: 'DELETE' });
    const j = await r.json();
    toast(j.message, j.code === 200);
    if (j.code === 200) { netSelectedId = null; initNetwork(); refresh(); }
  } finally { btnLoading(btn, false); }
}
