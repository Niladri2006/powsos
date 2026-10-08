/**
 * PawSOS — Map Utilities & OpenStreetMap Helpers
 * Zero API keys required. Free, open, and reliable Leaflet integration.
 */

const PawMap = {
  defaultCenter: [20, 0],
  defaultZoom: 2,

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
    if (![lat1, lon1, lat2, lon2].every(Number.isFinite)) return '';
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
      if (onError) onError('Location is unavailable in this browser. Place the pin manually on the map.');
      return;
    }

    navigator.geolocation.getCurrentPosition(
      (pos) => {
        onSuccess({
          lat: pos.coords.latitude,
          lng: pos.coords.longitude,
          accuracy: pos.coords.accuracy,
          timestamp: new Date(pos.timestamp).toISOString()
        });
      },
      (err) => {
        let msg = 'Location could not be determined. Place the pin manually on the map.';
        if (err.code === 1) msg = 'Location permission was denied. You can place the pin manually on the map.';
        else if (err.code === 2) msg = 'Your device could not determine its location. Try again or place the pin manually.';
        else if (err.code === 3) msg = 'Location request timed out. Try again or place the pin manually.';
        if (onError) onError(msg);
      },
      { timeout: 9000, enableHighAccuracy: true }
    );
  }
};

window.pawMap = PawMap;
