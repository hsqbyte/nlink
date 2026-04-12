// ---- Tab Switching ----
function switchTab(id) {
  document.querySelectorAll('.panel').forEach(p => p.classList.remove('active'));
  document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
  $(id).classList.add('active');
  event.target.classList.add('active');
  if (id === 'clients') { initNetwork(); }
  if (id === 'settings') loadConfig();
}

// ---- Toast Stack ----
let toastCounter = 0;

function toast(msg, type) {
  let cls = 'ok', icon = '✓';
  if (type === false || type === 'err') { cls = 'err'; icon = '✕'; }
  else if (type === 'warn') { cls = 'warn'; icon = '⚠'; }
  const id = 'toast-' + (++toastCounter);
  const el = document.createElement('div');
  el.id = id; el.className = 'toast-item ' + cls;
  el.innerHTML = '<span class="toast-icon">' + icon + '</span><span>' + esc(msg) + '</span><span class="toast-close" onclick="dismissToast(\'' + id + '\')">&times;</span>';
  $('toast-stack').appendChild(el);
  setTimeout(() => dismissToast(id), 3500);
}

function dismissToast(id) {
  const el = $(id); if (!el) return;
  el.classList.add('out');
  setTimeout(() => el.remove(), 250);
}

// ---- Confirm Dialog ----
let confirmResolveFn = null;

function showConfirm(title, desc, danger) {
  return new Promise(resolve => {
    confirmResolveFn = resolve;
    $('confirm-icon').textContent = danger ? '⚠️' : '❓';
    $('confirm-title').textContent = title;
    $('confirm-desc').textContent = desc;
    const okBtn = $('confirm-ok-btn');
    okBtn.className = danger ? 'btn btn-red' : 'btn btn-blue';
    $('confirm-dialog').classList.add('show');
  });
}

function confirmResolve(val) {
  $('confirm-dialog').classList.remove('show');
  if (confirmResolveFn) { confirmResolveFn(val); confirmResolveFn = null; }
}

// ---- Button Loading ----
function btnLoading(btn, on) {
  if (!btn) return;
  btn.disabled = on;
  if (on) btn.classList.add('loading'); else btn.classList.remove('loading');
}
