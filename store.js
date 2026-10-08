/**
 * PawSOS — Central Store & Persistence Layer
 * Bridges frontend UI with the production Go REST API backend.
 * Provides offline resilience, local caching, and instant reactive updates.
 */

const STORAGE_CASES_KEY = 'pawsos_cases_v2';
const STORAGE_MY_REPORTS_KEY = 'pawsos_my_report_ids';
const STORAGE_USER_KEY = 'pawsos_active_user_v2';

class PawStore {
  constructor() {
    this.listeners = [];
    this.cases = [];
    this.activeUser = this.loadActiveUser();
    this.isOnline = true;
    this.sanitizeStoredCache();

    // Listen for storage events across tabs
    window.addEventListener('storage', (e) => {
      if (e.key === STORAGE_CASES_KEY) {
        this.cases = this.loadLocalCache();
        this.notify();
      }
    });

    // Auto-sync with Go backend on startup
    if (typeof window !== 'undefined') {
      setTimeout(() => this.syncWithBackend(), 50);
      setInterval(() => {
        if (document.visibilityState === 'visible') this.syncWithBackend();
      }, 30000);
      document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'visible') this.syncWithBackend();
      });
    }
  }

  // Convert backend API report object to standard frontend model
  normalizeBackendReport(rep) {
    if (!rep) return null;

    const timeline = (rep.timeline || []).map(t => ({
      status: t.status || 'reported',
      time: t.created_at || new Date().toISOString(),
      author: t.author_name || '',
      note: t.note || ''
    }));

    let responder = null;
    if (rep.assigned_responder_name) {
      responder = {
        id: rep.assigned_responder_id || '',
        name: rep.assigned_responder_name,
        role: 'Authorized Responder',
        organization: '',
        etaText: rep.eta || ''
      };
    }

    return {
      id: rep.id,
      animalType: rep.animal_type || 'animal',
      animalName: rep.animal_name || (rep.animal_type || 'Animal'),
      photo: rep.photo_url || '',
      urgency: rep.urgency || 'moderate',
      condition: rep.condition || '',
      description: rep.description || '',
      location: {
        address: rep.address || '',
        area: rep.area || '',
        city: rep.city || '',
        lat: Number(rep.latitude),
        lng: Number(rep.longitude),
        accuracy: Number.isFinite(rep.location_accuracy) ? rep.location_accuracy : null,
        timestamp: rep.location_timestamp || null,
        source: rep.location_source || ''
      },
      reporter: {
        name: rep.reporter_name || '',
        phone: rep.reporter_phone || '',
        isPublicContact: false
      },
      status: rep.status || 'reported',
      eta: rep.eta || '',
      createdAt: rep.created_at || new Date().toISOString(),
      updatedAt: rep.updated_at || rep.created_at,
      responder: responder,
      evidence: rep.evidence || null,
      timeline
    };
  }

  loadLocalCache() {
    try {
      const data = localStorage.getItem(STORAGE_CASES_KEY);
      if (data) {
        const parsed = JSON.parse(data);
        if (Array.isArray(parsed) && parsed.length > 0) {
          return this.publicCacheProjection(parsed);
        }
      }
    } catch (e) {
      console.warn('Could not read cached cases', e);
    }
    return [];
  }

  publicCacheProjection(cases) {
    return cases.filter(item => item && typeof item === 'object' && typeof item.id === 'string').map(item => {
      const location = item.location && typeof item.location === 'object' ? item.location : null;
      const timeline = Array.isArray(item.timeline) ? item.timeline : [];
      return {
        ...item,
        location: location ? {
          ...location,
          address: '',
          area: '',
          city: '',
          accuracy: null,
          timestamp: null,
          source: '',
          lat: Number.isFinite(location.lat) ? Math.round(location.lat * 1000) / 1000 : null,
          lng: Number.isFinite(location.lng) ? Math.round(location.lng * 1000) / 1000 : null
        } : null,
        reporter: { name: '', phone: '', isPublicContact: false },
        responder: null,
        evidence: null,
        timeline: timeline.filter(entry => entry && typeof entry === 'object').map(entry => ({
          ...entry,
          author: '',
          note: ''
        }))
      };
    });
  }

  sanitizeStoredCache() {
    try {
      const stored = localStorage.getItem(STORAGE_CASES_KEY);
      if (!stored) return;
      const parsed = JSON.parse(stored);
      if (Array.isArray(parsed)) this.saveLocalCache(parsed);
    } catch (e) {
      console.warn('Could not sanitize the stored case cache', e);
    }
  }

  saveLocalCache(cases) {
    try {
      localStorage.setItem(STORAGE_CASES_KEY, JSON.stringify(this.publicCacheProjection(cases)));
    } catch (e) {
      console.error('Failed to cache cases locally', e);
    }
  }

  loadActiveUser() {
    try {
      if (window.pawAPI && window.pawAPI.getCurrentUser()) {
        return window.pawAPI.getCurrentUser();
      }
      const user = localStorage.getItem(STORAGE_USER_KEY);
      if (user) return JSON.parse(user);
    } catch (e) {}

    return {
      id: 'guest_citizen',
      name: 'Community Member',
      role: 'citizen',
      phone: ''
    };
  }

  setActiveUser(user) {
    this.activeUser = user;
    try {
      localStorage.setItem(STORAGE_USER_KEY, JSON.stringify(user));
    } catch (e) {}
    this.notify();
  }

  subscribe(callback) {
    this.listeners.push(callback);
    return () => {
      this.listeners = this.listeners.filter(cb => cb !== callback);
    };
  }

  notify() {
    this.listeners.forEach(cb => {
      try { cb(this.cases); } catch (err) { console.error('Store listener error:', err); }
    });
  }

  // Sync with Go backend database
  async syncWithBackend() {
    if (!window.pawAPI) return;
    try {
      const res = await window.pawAPI.getReports({ limit: 100 });
      if (res && Array.isArray(res.reports)) {
        this.cases = res.reports.map(r => this.normalizeBackendReport(r));
        this.saveLocalCache(this.cases);
        this.isOnline = true;
        this.notify();
      }
    } catch (err) {
      console.warn('Backend sync deferred (offline or starting up):', err.message);
      this.isOnline = false;
    }
  }

  // --- Query Methods ---

  getAllCases() {
    return this.cases;
  }

  getCaseById(id) {
    if (!id) return null;
    const cleanId = String(id).trim().toUpperCase();
    return this.cases.find(c => 
      c.id.toUpperCase() === cleanId || 
      c.id.replace('PAW-', '') === cleanId.replace('PAW-', '')
    ) || null;
  }

  async fetchFreshCaseById(id) {
    if (!id) return null;
    if (!window.pawAPI) throw new Error('The report service is unavailable.');
    const rep = await window.pawAPI.getReport(id);
    const norm = this.normalizeBackendReport(rep);
    const idx = this.cases.findIndex(c => c.id === norm.id);
    if (idx >= 0) this.cases[idx] = norm;
    else this.cases.unshift(norm);
    this.saveLocalCache(this.cases);
    return norm;
  }

  getMyReportIds() {
    try {
      const ids = localStorage.getItem(STORAGE_MY_REPORTS_KEY);
      const parsed = ids ? JSON.parse(ids) : [];
      return Array.isArray(parsed) ? parsed.filter(id => typeof id === 'string' && id.length <= 64) : [];
    } catch (e) {
      return [];
    }
  }

  getMyReports() {
    const myIds = this.getMyReportIds();
    return this.cases.filter(c => myIds.includes(c.id));
  }

  getSanitizedCase(caseItem, isResponder = false) {
    if (!caseItem) return null;
    const copy = JSON.parse(JSON.stringify(caseItem));
    if (!isResponder && copy.reporter && copy.reporter.phone) {
      const num = copy.reporter.phone.trim();
      if (num.length >= 7) {
        copy.reporter.phone = num.substring(0, 7) + '••••' + num.slice(-2);
      } else {
        copy.reporter.phone = '••••••';
      }
    }
    return copy;
  }

  async getNearbyCases(latitude, longitude, maxDistanceKm = 5) {
    if (typeof latitude !== 'number' || typeof longitude !== 'number') return [];
    const result = await window.pawAPI.getReports({
      status: 'active',
      latitude,
      longitude,
      radiusKm: maxDistanceKm,
      limit: 100
    });
    return (result.reports || []).map(report => {
      const normalized = this.normalizeBackendReport(report);
      normalized.distanceKm = Number.isFinite(report.distance_meters) ? report.distance_meters / 1000 : null;
      return normalized;
    });
  }

  getStats() {
    const total = this.cases.length;
    const active = this.cases.filter(c => c.status !== 'resolved' && c.status !== 'closed').length;
    const resolved = this.cases.filter(c => c.status === 'resolved').length;
    const critical = this.cases.filter(c => c.urgency === 'critical' && c.status !== 'resolved').length;
    return { total, active, resolved, critical };
  }

  // --- Mutation Methods (Persisted directly to Backend & DB) ---

  async createReport(formData, photoFile = null) {
    if (!window.pawAPI) throw new Error('PawSOS could not connect to the report service. Your report has not been submitted.');
    const payload = {
      animal_type: formData.animalType,
      animal_name: formData.animalName || '',
      urgency: formData.urgency,
      condition: formData.condition,
      description: formData.description || '',
      address: formData.address || '',
      area: formData.area || '',
      city: formData.city || '',
      latitude: formData.lat,
      longitude: formData.lng,
      location_accuracy: formData.locationAccuracy,
      location_timestamp: formData.locationTimestamp,
      location_source: formData.locationSource,
      photo_source: formData.photoSource,
      photo_captured_at: formData.photoCapturedAt,
      photo_location_accuracy: formData.photoLocationAccuracy,
      photo_latitude: formData.photoLatitude,
      photo_longitude: formData.photoLongitude,
      reporter_name: formData.reporterName,
      reporter_phone: formData.reporterPhone,
      confirm_duplicate: formData.confirmDuplicate === true
    };
    const res = await window.pawAPI.createReport(payload, photoFile);
    if (!res || !res.id) {
      throw new Error('PawSOS did not confirm this report. It has not been submitted.');
    }
    const createdRecord = this.normalizeBackendReport(res);

    this.cases.unshift(createdRecord);
    this.saveLocalCache(this.cases);

    // Track on user's device
    const myReports = this.getMyReportIds();
    myReports.unshift(createdRecord.id);
    try {
      localStorage.setItem(STORAGE_MY_REPORTS_KEY, JSON.stringify(myReports));
    } catch (e) {}

    this.notify();
    return createdRecord;
  }

  async assignResponder(caseId, responderInfo) {
    if (!window.pawAPI || !window.pawAPI.isAuthenticated()) throw new Error('Please sign in as a verified responder to accept a case.');
    const res = await window.pawAPI.assignReport(caseId, responderInfo);
    const norm = this.normalizeBackendReport(res);
    const idx = this.cases.findIndex(c => c.id === caseId);
    if (idx >= 0) this.cases[idx] = norm;
    else this.cases.unshift(norm);
    this.saveLocalCache(this.cases);
    this.notify();
    return norm;
  }

  async updateCaseStatus(caseId, newStatus, noteText = '', authorName = 'Responder') {
    if (!window.pawAPI || !window.pawAPI.isAuthenticated()) throw new Error('Please sign in as a verified responder to update a case.');
    const res = await window.pawAPI.updateStatus(caseId, newStatus, noteText);
    const norm = this.normalizeBackendReport(res);
    const idx = this.cases.findIndex(c => c.id === caseId);
    if (idx >= 0) this.cases[idx] = norm;
    else this.cases.unshift(norm);
    this.saveLocalCache(this.cases);
    this.notify();
    return norm;
  }

  async addCaseNote(caseId, noteText, authorName = 'Personnel') {
    if (!window.pawAPI || !window.pawAPI.isAuthenticated()) throw new Error('Please sign in as a verified responder to add a field note.');
    await window.pawAPI.addNote(caseId, noteText, authorName);
    return this.fetchFreshCaseById(caseId);
  }
}

window.pawEscapeHTML = function (value) {
  return String(value ?? '').replace(/[&<>"']/g, (character) => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;'
  })[character]);
};

window.pawStore = new PawStore();
