/**
 * PawSOS — Map Utilities & OpenStreetMap Helpers
 * Zero API keys required. Free, open, and reliable Leaflet integration.
 */

const PawMap = {
  defaultCenter: [26.8928, 75.7873], // City center coordinates (Jaipur)
  defaultZoom: 13,

  createIcon(urgency = 'urgent', label = '🐾') {
    if (typeof L === 'undefined') return null;

    let pinClass = 'clean-pin urgent';
    if (urgency === 'critical') pinClass = 'clean-pin emergency';
    else if (urgency === 'resolved') pinClass = 'clean-pin resolved';
    else if (urgency === 'rescuer') pinClass = 'clean-pin rescuer';

    return L.divIcon({
      className: 'leaflet-clean-marker',
      html: `<div class="${pinClass}">${label}</div>`,
      iconSize: [32, 32],
      iconAnchor: [16, 16],
      popupAnchor: [0, -18]
    });
  },

  calculateDistance(lat1, lon1, lat2, lon2) {
    if (!lat1 || !lon1 || !lat2 || !lon2) return '';
    const R = 6371; // Earth radius in km
    const dLat = (lat2 - lat1) * Math.PI / 180;
    const dLon = (lon2 - lon1) * Math.PI / 180;
    const a =
      Math.sin(dLat / 2) * Math.sin(dLat / 2) +
      Math.cos(lat1 * Math.PI / 180) * Math.cos(lat2 * Math.PI / 180) *
      Math.sin(dLon / 2) * Math.sin(dLon / 2);
    const c = 2 * Math.atan2(Math.sqrt(a), Math.sqrt(1 - a));
    const d = R * c;
    return d < 1 ? `${Math.round(d * 1000)} m` : `${d.toFixed(1)} km`;
  },

  getCurrentLocation(onSuccess, onError) {
    if (!navigator.geolocation) {
      if (onError) onError('Geolocation is not supported by your browser.');
      return;
    }

    navigator.geolocation.getCurrentPosition(
      (pos) => {
        onSuccess({
          lat: pos.coords.latitude,
          lng: pos.coords.longitude,
          accuracy: pos.coords.accuracy
        });
      },
      (err) => {
        let msg = 'Unable to retrieve location.';
        if (err.code === 1) msg = 'Location permission denied by user.';
        else if (err.code === 2) msg = 'Location position unavailable.';
        else if (err.code === 3) msg = 'Location request timed out.';
        if (onError) onError(msg);
      },
      { timeout: 9000, enableHighAccuracy: true }
    );
  }
};

window.pawMap = PawMap;
