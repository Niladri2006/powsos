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
    this.cases = this.loadLocalCache();
    this.activeUser = this.loadActiveUser();
    this.isOnline = true;

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
    }
  }

  // Convert backend API report object to standard frontend model
  normalizeBackendReport(rep) {
    if (!rep) return null;

    const timeline = (rep.timeline || []).map(t => ({
      status: t.status || 'reported',
      time: t.created_at || new Date().toISOString(),
      author: t.author_name || 'System',
      note: t.note || ''
    }));

    let responder = null;
    if (rep.assigned_responder_name) {
      responder = {
        id: rep.assigned_responder_id || '',
        name: rep.assigned_responder_name,
        role: 'Authorized Responder',
        organization: 'PawSOS Rescue Network',
        etaText: rep.status === 'resolved' ? 'Completed' : 'On Scene / In Transit'
      };
    }

    return {
      id: rep.id,
      animalType: rep.animal_type || 'dog',
      animalName: rep.animal_name || `${(rep.animal_type || 'Animal').toUpperCase()} in distress`,
      photo: rep.photo_url || (rep.animal_type === 'cat' ? 'assets/cat-sample.jpg' : 'assets/dog-sample.jpg'),
      urgency: rep.urgency || 'urgent',
      condition: rep.condition || '',
      description: rep.description || '',
      location: {
        address: rep.address || '',
        area: rep.area || '',
        city: rep.city || '',
        lat: rep.latitude || 37.7749,
        lng: rep.longitude || -122.4194
      },
      reporter: {
        name: rep.reporter_name || 'Citizen',
        phone: rep.reporter_phone || '',
        isPublicContact: false
      },
      status: rep.status || 'reported',
      createdAt: rep.created_at || new Date().toISOString(),
      updatedAt: rep.updated_at || rep.created_at,
      responder: responder,
      timeline: timeline.length > 0 ? timeline : [
        {
          status: rep.status || 'reported',
          time: rep.created_at || new Date().toISOString(),
          author: rep.reporter_name || 'Citizen',
          note: 'Case submitted to PawSOS dispatch.'
        }
      ]
    };
  }

  loadLocalCache() {
    try {
      const data = localStorage.getItem(STORAGE_CASES_KEY);
      if (data) {
        const parsed = JSON.parse(data);
        if (Array.isArray(parsed) && parsed.length > 0) {
          return parsed;
        }
      }
    } catch (e) {
      console.warn('Could not read cached cases', e);
    }
    return [];
  }

  saveLocalCache(cases) {
    try {
      localStorage.setItem(STORAGE_CASES_KEY, JSON.stringify(cases));
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
    if (window.pawAPI) {
      try {
        const rep = await window.pawAPI.getReport(id);
        const norm = this.normalizeBackendReport(rep);
        // update local list
        const idx = this.cases.findIndex(c => c.id === norm.id);
        if (idx >= 0) {
          this.cases[idx] = norm;
        } else {
          this.cases.unshift(norm);
        }
        this.saveLocalCache(this.cases);
        this.notify();
        return norm;
      } catch (e) {
        console.warn('Could not fetch fresh case from API, using cached:', e.message);
      }
    }
    return this.getCaseById(id);
  }

  getMyReportIds() {
    try {
      const ids = localStorage.getItem(STORAGE_MY_REPORTS_KEY);
      return ids ? JSON.parse(ids) : [];
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

  getStats() {
    const total = this.cases.length;
    const active = this.cases.filter(c => c.status !== 'resolved' && c.status !== 'closed').length;
    const resolved = this.cases.filter(c => c.status === 'resolved').length;
    const critical = this.cases.filter(c => c.urgency === 'critical' && c.status !== 'resolved').length;
    return { total, active, resolved, critical };
  }

  // --- Mutation Methods (Persisted directly to Backend & DB) ---

  async createReport(formData, photoFile = null) {
    let createdRecord = null;

    if (window.pawAPI) {
      try {
        const payload = {
          animal_type: formData.animalType || 'dog',
          animal_name: formData.animalName || '',
          urgency: formData.urgency || 'urgent',
          condition: formData.condition || 'Distress reported',
          description: formData.description || '',
          address: formData.address || '',
          area: formData.area || '',
          city: formData.city || 'Metropolis',
          latitude: formData.lat || 37.7749,
          longitude: formData.lng || -122.4194,
          reporter_name: formData.reporterName || 'Citizen',
          reporter_phone: formData.reporterPhone || ''
        };

        const res = await window.pawAPI.createReport(payload, photoFile);
        if (res && res.id) {
          createdRecord = this.normalizeBackendReport(res);
        }
      } catch (err) {
        console.warn('API report creation error, creating local fallback:', err.message);
      }
    }

    // Local fallback if API failed or offline
    if (!createdRecord) {
      const randomDigits = Math.floor(1000 + Math.random() * 9000);
      createdRecord = {
        id: `PAW-${randomDigits}`,
        animalType: formData.animalType || 'dog',
        animalName: formData.animalName || `${formData.animalType || 'Animal'} in distress`,
        photo: formData.photo || 'assets/dog-sample.jpg',
        urgency: formData.urgency || 'urgent',
        condition: formData.condition || 'Animal in distress reported',
        description: formData.description || '',
        location: {
          address: formData.address || 'Address provided',
          area: formData.area || '',
          city: formData.city || 'Metropolis',
          lat: formData.lat || 37.7749,
          lng: formData.lng || -122.4194
        },
        reporter: {
          name: formData.reporterName || 'Citizen',
          phone: formData.reporterPhone || '',
          isPublicContact: false
        },
        status: 'reported',
        createdAt: new Date().toISOString(),
        responder: null,
        timeline: [
          {
            status: 'reported',
            time: new Date().toISOString(),
            author: formData.reporterName || 'Citizen',
            note: 'Report registered.'
          }
        ]
      };
    }

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
    if (window.pawAPI && window.pawAPI.isAuthenticated()) {
      try {
        const res = await window.pawAPI.assignReport(caseId, responderInfo);
        const norm = this.normalizeBackendReport(res);
        const idx = this.cases.findIndex(c => c.id === caseId);
        if (idx >= 0) this.cases[idx] = norm;
        this.saveLocalCache(this.cases);
        this.notify();
        return norm;
      } catch (err) {
        console.warn('API assign failed:', err.message);
      }
    }

    // Local update
    const target = this.getCaseById(caseId);
    if (!target) return null;

    target.status = 'assigned';
    target.responder = {
      name: responderInfo.name || 'Volunteer Responder',
      role: responderInfo.role || 'Volunteer Rescuer',
      organization: responderInfo.organization || 'PawSOS Unit',
      etaText: responderInfo.eta || responderInfo.etaText || '15-20 mins'
    };
    target.timeline.push({
      status: 'assigned',
      time: new Date().toISOString(),
      author: target.responder.name,
      note: `Responder ${target.responder.name} assigned. ETA: ${target.responder.etaText}.`
    });

    this.saveLocalCache(this.cases);
    this.notify();
    return target;
  }

  async updateCaseStatus(caseId, newStatus, noteText = '', authorName = 'Responder') {
    if (window.pawAPI && window.pawAPI.isAuthenticated()) {
      try {
        const res = await window.pawAPI.updateStatus(caseId, newStatus, noteText);
        const norm = this.normalizeBackendReport(res);
        const idx = this.cases.findIndex(c => c.id === caseId);
        if (idx >= 0) this.cases[idx] = norm;
        this.saveLocalCache(this.cases);
        this.notify();
        return norm;
      } catch (err) {
        console.warn('API status update failed:', err.message);
      }
    }

    const target = this.getCaseById(caseId);
    if (!target) return null;

    target.status = newStatus;
    target.timeline.push({
      status: newStatus,
      time: new Date().toISOString(),
      author: authorName,
      note: noteText || `Case marked as ${newStatus.replace('_', ' ').toUpperCase()}.`
    });

    this.saveLocalCache(this.cases);
    this.notify();
    return target;
  }

  async addCaseNote(caseId, noteText, authorName = 'Personnel') {
    if (window.pawAPI && window.pawAPI.isAuthenticated()) {
      try {
        await window.pawAPI.addNote(caseId, noteText, authorName);
        return this.fetchFreshCaseById(caseId);
      } catch (err) {
        console.warn('API add note failed:', err.message);
      }
    }

    const target = this.getCaseById(caseId);
    if (!target || !noteText) return null;

    target.timeline.push({
      status: target.status,
      time: new Date().toISOString(),
      author: authorName,
      note: noteText.trim()
    });

    this.saveLocalCache(this.cases);
    this.notify();
    return target;
  }
}

window.pawStore = new PawStore();
