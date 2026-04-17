// 主题切换（浅色/深色）
// - localStorage 持久化
// - 默认跟随系统
// - 支持 nav 中的切换按钮
(function () {
  const KEY = 'nlink.theme';
  function apply(theme) {
    const root = document.documentElement;
    if (theme === 'dark') {
      root.setAttribute('data-theme', 'dark');
    } else {
      root.removeAttribute('data-theme');
    }
    const btn = document.querySelector('.theme-toggle');
    if (btn) btn.textContent = theme === 'dark' ? '☀️' : '🌙';
  }
  function current() {
    const saved = localStorage.getItem(KEY);
    if (saved === 'light' || saved === 'dark') return saved;
    return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }
  function toggle() {
    const next = current() === 'dark' ? 'light' : 'dark';
    localStorage.setItem(KEY, next);
    apply(next);
  }
  // 尽早应用主题，避免闪烁
  apply(current());
  document.addEventListener('DOMContentLoaded', function () {
    apply(current());
    const btn = document.querySelector('.theme-toggle');
    if (btn) btn.addEventListener('click', toggle);
  });
  // 系统偏好变化时，仅当用户未手动选择时跟随
  if (window.matchMedia) {
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', function (e) {
      if (!localStorage.getItem(KEY)) apply(e.matches ? 'dark' : 'light');
    });
  }
  // 对外暴露（如需手动调用）
  window.NLinkTheme = { toggle: toggle, current: current, apply: apply };
})();
