// ---- Keyboard ----
document.addEventListener('keydown', e => {
  if (e.key === 'Escape' && $('confirm-dialog').classList.contains('show')) confirmResolve(false);
});

// ---- Init ----
refresh();
startRefreshTimer();
