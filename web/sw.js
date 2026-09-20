// Invoke Service Worker for PWA
const CACHE_NAME = 'invoke-pwa-v1';
const STATIC_ASSETS = [
  '/web/favicon.png',
  '/web/favicon.ico',
  '/web/xterm.min.css',
  '/web/xterm.min.js',
  '/web/xterm-addon-fit.min.js',
  '/manifest.json'
];

self.addEventListener('install', event => {
  event.waitUntil(
    caches.open(CACHE_NAME).then(cache => {
      return cache.addAll(STATIC_ASSETS).catch(err => {
        console.warn('Invoke SW precache warning:', err);
      });
    }).then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', event => {
  event.waitUntil(
    caches.keys().then(keys => {
      return Promise.all(
        keys.filter(k => k !== CACHE_NAME).map(k => caches.delete(k))
      );
    }).then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', event => {
  const url = new URL(event.request.url);

  // Never cache or intercept WebSocket, tunnels, or POST/PUT/DELETE requests
  if (url.pathname.startsWith('/ws') || url.pathname.startsWith('/tunnel') || event.request.method !== 'GET') {
    return;
  }

  // Network-first for navigation and dynamic APIs to always provide fresh state
  if (event.request.mode === 'navigate' || url.pathname === '/' || url.pathname === '/remote-login') {
    event.respondWith(
      fetch(event.request).catch(() => caches.match(event.request))
    );
    return;
  }

  // Cache-first with network fallback for static files in /web/
  if (url.pathname.startsWith('/web/') || url.pathname === '/manifest.json') {
    event.respondWith(
      caches.match(event.request).then(cached => {
        if (cached) return cached;
        return fetch(event.request).then(resp => {
          if (resp && resp.status === 200) {
            const copy = resp.clone();
            caches.open(CACHE_NAME).then(cache => cache.put(event.request, copy));
          }
          return resp;
        });
      })
    );
    return;
  }

  event.respondWith(fetch(event.request));
});
