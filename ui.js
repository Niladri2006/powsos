/**
 * PawSOS — Common UI Elements (Header, Navigation, Toast Notifications, Mobile Drawer)
 * Clean, production-grade presentation without gimmick overlays.
 */

document.addEventListener('DOMContentLoaded', () => {
  renderTopBanner();
  renderHeader();
  renderToastShelf();
  highlightActiveNav();
});

function renderTopBanner() {
  if (document.getElementById('top-emergency-banner')) return;

  const banner = document.createElement('div');
  banner.id = 'top-emergency-banner';
  banner.className = 'emergency-banner';
  banner.innerHTML = `
    <div class="emergency-banner-content">
      <span class="banner-tag">Notice</span>
      <span>Critical animal trauma or hit-and-run? Dial the official emergency helpline:</span>
      <a href="tel:1962" class="tel-link">1962 (Toll Free)</a>
    </div>
    <div style="font-size: 0.8rem; color: #7f1d1d;">
      Community-powered response for street animals
    </div>
  `;
  document.body.prepend(banner);
}

function renderHeader() {
  const mount = document.getElementById('navbar-mount');
  if (!mount) return;

  const myReportsCount = window.pawStore ? window.pawStore.getMyReportIds().length : 0;

  mount.innerHTML = `
    <header class="site-header">
      <div class="container header-inner">
        <a href="index.html" class="brand-link">
          <div class="brand-emblem">🐾</div>
          <div class="brand-text-wrap">
            <div class="brand-name">Paw<span>SOS</span></div>
            <div class="brand-tagline">Animal Emergency Network</div>
          </div>
        </a>

        <nav>
          <ul class="nav-menu" id="primary-nav-menu">
            <li><a href="index.html" class="nav-item-link" data-page="index">Home</a></li>
            <li><a href="map.html" class="nav-item-link" data-page="map">Active Reports</a></li>
            <li>
              <a href="track.html" class="nav-item-link" data-page="track">
                Track a Report
                ${myReportsCount > 0 ? `<span style="background: #e2e8f0; font-size: 0.72rem; padding: 1px 6px; border-radius: 9999px; margin-left: 4px; font-weight: 700;">${myReportsCount}</span>` : ''}
              </a>
            </li>
            <li><a href="about.html" class="nav-item-link" data-page="about">First Aid & Helplines</a></li>
            <li><a href="dashboard.html" class="nav-item-link" data-page="dashboard">Responder Portal</a></li>
          </ul>
        </nav>

        <div class="nav-actions">
          <a href="report.html" class="btn btn-emergency btn-sm">
            <span>🚨 Report Animal</span>
          </a>
          <button class="mobile-nav-toggle" id="mobile-nav-toggle" aria-label="Open Navigation Menu">
            ☰
          </button>
        </div>
      </div>
    </header>
  `;

  // Mobile menu toggle
  const toggleBtn = document.getElementById('mobile-nav-toggle');
  const menu = document.getElementById('primary-nav-menu');
  if (toggleBtn && menu) {
    toggleBtn.addEventListener('click', () => {
      menu.classList.toggle('open');
      toggleBtn.textContent = menu.classList.contains('open') ? '✕' : '☰';
    });
  }
}

function highlightActiveNav() {
  const currentPath = window.location.pathname;
  const page = currentPath.substring(currentPath.lastIndexOf('/') + 1) || 'index.html';
  const cleanPage = page.replace('.html', '');

  document.querySelectorAll('.nav-item-link').forEach(link => {
    const linkPage = link.getAttribute('data-page');
    if (linkPage === cleanPage || (cleanPage === '' && linkPage === 'index')) {
      link.classList.add('active');
    } else {
      link.classList.remove('active');
    }
  });
}

function renderToastShelf() {
  if (document.getElementById('toast-shelf')) return;
  const shelf = document.createElement('div');
  shelf.id = 'toast-shelf';
  shelf.className = 'toast-shelf';
  document.body.appendChild(shelf);
}

function showToast(message, type = 'info', duration = 4000) {
  const shelf = document.getElementById('toast-shelf');
  if (!shelf) return;

  const toast = document.createElement('div');
  toast.className = `toast-item ${type}`;

  let icon = 'ℹ️';
  if (type === 'success') icon = '✓';
  else if (type === 'error') icon = '⚠️';

  toast.innerHTML = `
    <span style="font-weight: 700; font-size: 1.05rem;">${icon}</span>
    <span style="flex: 1;">${message}</span>
    <button style="background: none; border: none; color: inherit; opacity: 0.7; cursor: pointer; font-size: 1rem; padding: 0 4px;" onclick="this.parentElement.remove()">✕</button>
  `;

  shelf.appendChild(toast);

  setTimeout(() => {
    toast.style.opacity = '0';
    toast.style.transform = 'translateY(8px)';
    toast.style.transition = 'all 0.2s ease';
    setTimeout(() => toast.remove(), 250);
  }, duration);
}
