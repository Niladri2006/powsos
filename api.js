/**
 * PawSOS — Production API Client
 * Clean REST client communicating with the Go backend server.
 * Handles token management, multipart uploads, error parsing, and network retry.
 */

(function () {
  const API_BASE = '/api/v1';
  const TOKEN_KEY = 'pawsos_jwt_token';
  const USER_KEY = 'pawsos_auth_user';

  class PawAPI {
    constructor() {
      this.token = localStorage.getItem(TOKEN_KEY) || null;
      this.currentUser = null;
      try {
        const u = localStorage.getItem(USER_KEY);
        if (u) this.currentUser = JSON.parse(u);
      } catch (e) {}
    }

    getToken() {
      return this.token;
    }

    getCurrentUser() {
      return this.currentUser;
    }

    isAuthenticated() {
      return !!this.token;
    }

    isResponder() {
      return this.currentUser && (this.currentUser.role === 'responder' || this.currentUser.role === 'admin');
    }

    isAdmin() {
      return this.currentUser && this.currentUser.role === 'admin';
    }

    setSession(token, user) {
      this.token = token;
      this.currentUser = user;
      localStorage.setItem(TOKEN_KEY, token);
      localStorage.setItem(USER_KEY, JSON.stringify(user));
    }

    clearSession() {
      this.token = null;
      this.currentUser = null;
      localStorage.removeItem(TOKEN_KEY);
      localStorage.removeItem(USER_KEY);
    }

    // Common request dispatcher
    async request(path, options = {}) {
      const url = path.startsWith('/api') || path.startsWith('/health') || path.startsWith('/ready')
        ? path
        : `${API_BASE}${path}`;

      const headers = options.headers || {};
      if (this.token && !headers['Authorization']) {
        headers['Authorization'] = `Bearer ${this.token}`;
      }

      // If body is not FormData, default to application/json
      if (options.body && !(options.body instanceof FormData) && !headers['Content-Type']) {
        headers['Content-Type'] = 'application/json';
      }

      try {
        const response = await fetch(url, {
          ...options,
          headers
        });

        if (response.status === 401) {
          // If token expired or unauthorized, clear session
          if (this.token && path.includes('/auth/me')) {
            this.clearSession();
          }
        }

        const contentType = response.headers.get('content-type') || '';
        let data = null;
        if (contentType.includes('application/json')) {
          data = await response.json();
        } else {
          data = await response.text();
        }

        if (!response.ok) {
          const errObj = (data && data.error) ? data.error : { message: `Request failed with status ${response.status}` };
          const err = new Error(errObj.message || 'API request failed');
          err.code = errObj.code || 'HTTP_ERROR';
          err.status = response.status;
          throw err;
        }

        return data;
      } catch (err) {
        if (err.name === 'TypeError' && err.message.includes('fetch')) {
          const netErr = new Error('PawSOS server connection unavailable. Please check your internet or local server.');
          netErr.code = 'NETWORK_ERROR';
          throw netErr;
        }
        throw err;
      }
    }

    // Health
    async checkHealth() {
      return this.request('/health', { method: 'GET' });
    }

    async checkReady() {
      return this.request('/ready', { method: 'GET' });
    }

    // Authentication
    async login(email, password) {
      const res = await this.request('/auth/login', {
        method: 'POST',
        body: JSON.stringify({ email, password })
      });
      if (res && res.token) {
        this.setSession(res.token, res.user);
      }
      return res;
    }

    async register(userData) {
      const res = await this.request('/auth/register', {
        method: 'POST',
        body: JSON.stringify(userData)
      });
      if (res && res.token) {
        this.setSession(res.token, res.user);
      }
      return res;
    }

    async getMe() {
      if (!this.token) return null;
      try {
        const user = await this.request('/auth/me', { method: 'GET' });
        this.currentUser = user;
        localStorage.setItem(USER_KEY, JSON.stringify(user));
        return user;
      } catch (err) {
        return null;
      }
    }

    logout() {
      this.clearSession();
    }

    // Reports
    async getReports(params = {}) {
      const q = new URLSearchParams();
      if (params.status && params.status !== 'all') q.set('status', params.status);
      if (params.urgency && params.urgency !== 'all') q.set('urgency', params.urgency);
      if (params.search) q.set('search', params.search);
      if (params.limit) q.set('limit', params.limit);
      if (params.offset) q.set('offset', params.offset);

      const qs = q.toString() ? `?${q.toString()}` : '';
      return this.request(`/reports${qs}`, { method: 'GET' });
    }

    async getReport(id) {
      return this.request(`/reports/${encodeURIComponent(id)}`, { method: 'GET' });
    }

    async createReport(data, photoFile = null) {
      if (photoFile instanceof File || photoFile instanceof Blob) {
        const formData = new FormData();
        for (const [key, value] of Object.entries(data)) {
          if (value !== undefined && value !== null) {
            formData.append(key, value);
          }
        }
        formData.append('photo', photoFile);
        return this.request('/reports', {
          method: 'POST',
          body: formData
        });
      } else {
        return this.request('/reports', {
          method: 'POST',
          body: JSON.stringify(data)
        });
      }
    }

    async assignReport(id, responderInfo) {
      return this.request(`/reports/${encodeURIComponent(id)}/assign`, {
        method: 'POST',
        body: JSON.stringify({
          responder_name: responderInfo.name || responderInfo.responder_name,
          responder_id: responderInfo.id || responderInfo.responder_id || '',
          eta: responderInfo.eta || responderInfo.etaText || '15-20 minutes',
          vehicle: responderInfo.vehicle || ''
        })
      });
    }

    async updateStatus(id, status, note = '') {
      return this.request(`/reports/${encodeURIComponent(id)}/status`, {
        method: 'POST',
        body: JSON.stringify({ status, note })
      });
    }

    async addNote(id, note, authorName = '') {
      return this.request(`/reports/${encodeURIComponent(id)}/notes`, {
        method: 'POST',
        body: JSON.stringify({ note, author_name: authorName })
      });
    }

    async getStats() {
      return this.request('/stats', { method: 'GET' });
    }

    getExportUrl() {
      return `${API_BASE}/reports/export`;
    }
  }

  window.pawAPI = new PawAPI();
})();
