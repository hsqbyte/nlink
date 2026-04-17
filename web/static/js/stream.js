// SSE 实时推送：连接 /api/v1/stream，收到数据时调用 applyStats
// 连接成功后，暂停定时轮询；SSE 断开时自动回退到轮询
(function () {
  let es = null;
  let reconnectTimer = null;

  function stopPolling() {
    if (typeof refreshTimer !== 'undefined' && refreshTimer) {
      clearInterval(refreshTimer);
      refreshTimer = null;
    }
  }
  function resumePolling() {
    if (typeof startRefreshTimer === 'function' &&
        (typeof refreshTimer === 'undefined' || !refreshTimer)) {
      startRefreshTimer();
    }
  }

  function connect() {
    if (!window.EventSource) return;
    try {
      es = new EventSource('/api/v1/stream');
    } catch (e) {
      console.warn('SSE 不可用，回退到轮询', e);
      return;
    }
    es.onopen = function () {
      console.log('[SSE] connected');
      stopPolling();
    };
    es.onmessage = function (ev) {
      try {
        const data = JSON.parse(ev.data);
        if (typeof applyStats === 'function') applyStats(data);
      } catch (e) { /* ignore */ }
    };
    es.onerror = function () {
      console.warn('[SSE] 断开，回退到轮询');
      try { es.close(); } catch (_) {}
      es = null;
      resumePolling();
      // 10s 后再尝试重连
      if (reconnectTimer) clearTimeout(reconnectTimer);
      reconnectTimer = setTimeout(connect, 10000);
    };
  }
  document.addEventListener('DOMContentLoaded', connect);
})();
