// ---- Keyboard ----
document.addEventListener('keydown', e => {
  if (e.key === 'Escape' && $('confirm-dialog').classList.contains('show')) confirmResolve(false);
});

// ---- Auth ----
async function doLogout() {
  try { await fetch('/api/v1/logout', { method: 'POST' }); } catch (e) {}
  window.location.href = '/login';
}

// Global fetch interceptor for 401
const _origFetch = window.fetch;
window.fetch = async function() {
  const resp = await _origFetch.apply(this, arguments);
  if (resp.status === 401 && !arguments[0].toString().includes('/login')) {
    window.location.href = '/login';
  }
  return resp;
};

// ---- Init ----
refresh();
startRefreshTimer();
