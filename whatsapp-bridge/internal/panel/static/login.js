// Sends the one-time link to the server and moves on to the panel. The token
// is read from the part after "#" (or from ?t= in older links) and removed
// from the address bar right away.
(function () {
  'use strict';
  var src = location.hash.length > 1 ? location.hash.slice(1) : location.search.slice(1);
  var q = new URLSearchParams(src);
  var t = q.get('t') || '', s = q.get('s') || '';
  history.replaceState(null, '', '/panel/login');
  function go(target) { location.replace(target || '/panel/#login-vencido'); }
  if (!t) { go(); return; }
  fetch('/panel/api/login-link', {
    method: 'POST', credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ t: t, s: s })
  }).then(function (r) { return r.json(); })
    .then(function (j) { go(j.target); })
    .catch(function () {
      document.getElementById('link-msg').textContent = 'No se pudo conectar con el servicio. Revisa "Estado" en el menú Inicio.';
    });
})();
