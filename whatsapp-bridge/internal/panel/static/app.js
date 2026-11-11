// WhatsApp MCP web panel. Plain JavaScript, no outside files (the server
// may have no internet), Spanish text.
(function () {
  'use strict';
  var $ = function (id) { return document.getElementById(id); };
  var CSRF = '', CATS = [], cur = '', timers = {};

  // ---------- helpers ----------
  function esc(s) { return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]; }); }
  function show(el, on) { if (typeof el === 'string') el = $(el); if (el) el.classList.toggle('hidden', !on); }
  function toast(msg) { var t = $('toast'); t.textContent = msg; show(t, true); clearTimeout(timers.toast); timers.toast = setTimeout(function () { show(t, false); }, 3200); }
  function api(path, body) {
    var opt = { credentials: 'same-origin', headers: {} };
    if (body !== undefined) { opt.method = 'POST'; opt.headers['Content-Type'] = 'application/json'; opt.headers['X-CSRF-Token'] = CSRF; opt.body = JSON.stringify(body); }
    return fetch('/panel/api/' + path, opt).then(function (r) {
      return r.json().catch(function () { return { ok: false, error: 'Respuesta no válida (' + r.status + ')' }; }).then(function (j) {
        if (r.status === 401 && j.error === 'login') { showLogin(); throw new Error('login'); }
        if (!j.ok) throw new Error(j.error || ('Error ' + r.status));
        return j;
      });
    });
  }
  var ES_ERR = [
    [/not connected/i, 'WhatsApp no está conectado ahora. Revisa Inicio.'],
    [/no stored messages in this chat/i, 'Este chat no tiene mensajes guardados desde donde pedir.'],
    [/not a group id/i, 'Eso no es un grupo.'],
    [/no groups chosen yet/i, 'Primero marca los grupos de los que quieres sacar contactos.'],
    [/forbidden|not-authorized|not in group|item-not-found/i, 'Ya no estás en este grupo o WhatsApp no deja ver sus miembros.']];
  function esErr(m) { m = String(m || ''); for (var i = 0; i < ES_ERR.length; i++) if (ES_ERR[i][0].test(m)) return ES_ERR[i][1]; return m; }
  function errTo(el) { return function (e) { if (e.message !== 'login') { var m = esErr(e.message); if (el) $(el).textContent = m; else toast(m); } }; }
  function pad(n) { return n < 10 ? '0' + n : '' + n; }
  function fmtDate(s) {
    if (!s) return '';
    var d = new Date(s); if (isNaN(d)) return '';
    var now = new Date(), t = pad(d.getHours()) + ':' + pad(d.getMinutes());
    if (d.toDateString() === now.toDateString()) return 'hoy ' + t;
    var y = new Date(now); y.setDate(now.getDate() - 1);
    if (d.toDateString() === y.toDateString()) return 'ayer ' + t;
    return pad(d.getDate()) + '/' + pad(d.getMonth() + 1) + '/' + d.getFullYear() + ' ' + t;
  }
  function waited(s) {
    var ms = Date.now() - new Date(s).getTime(); if (!(ms > 0)) return '';
    var m = Math.floor(ms / 60000), h = Math.floor(m / 60), d = Math.floor(h / 24);
    if (d >= 1) return d === 1 ? '1 día' : d + ' días';
    if (h >= 1) return h === 1 ? '1 hora' : h + ' horas';
    return m <= 1 ? '1 minuto' : m + ' minutos';
  }
  function titleCase(s) {
    var small = { de: 1, del: 1, la: 1, las: 1, los: 1, y: 1, e: 1, da: 1, do: 1, dos: 1, van: 1, von: 1 };
    return String(s).trim().replace(/\s+/g, ' ').toLowerCase().split(' ').map(function (w, i) {
      if (i > 0 && small[w]) return w;
      return w.split('-').map(function (p) { return p.charAt(0).toUpperCase() + p.slice(1); }).join('-');
    }).join(' ');
  }
  function initials(name) {
    var p = String(name || '?').replace(/[^\p{L}\p{N} ]/gu, '').trim().split(/\s+/);
    return ((p[0] || '?').charAt(0) + (p.length > 1 ? p[p.length - 1].charAt(0) : '')).toUpperCase() || '?';
  }
  function guessCountry(digits) { // longest calling code that matches
    var best = null;
    COUNTRIES.forEach(function (c) { if (digits.indexOf(c.dial) === 0 && (!best || c.dial.length > best.dial.length)) best = c; });
    return best;
  }
  function fmtPhone(digits) {
    digits = String(digits || '').replace(/\D/g, ''); if (!digits) return '';
    var c = guessCountry(digits); if (!c) return '+' + digits;
    return '+' + c.dial + ' ' + applyMask(digits.slice(c.dial.length), c.mask);
  }
  function applyMask(d, mask) {
    var out = '', i = 0;
    for (var k = 0; k < mask.length && i < d.length; k++) out += mask[k] === '#' ? d[i++] : mask[k];
    if (i < d.length) out += d.slice(i);
    return out;
  }

  // ---------- phone input with flag selector and mask ----------
  function phoneInput(wrap, opts) {
    opts = opts || {};
    var country = countryByIso(opts.country || 'US');
    wrap.innerHTML = '<button type="button" class="pc-btn"></button><input type="tel" inputmode="tel" placeholder="Número">' +
      '<div class="pc-menu hidden"><input type="search" placeholder="Buscar país"><div class="opts"></div></div>';
    var btn = wrap.querySelector('.pc-btn'), inp = wrap.querySelector('input[type=tel]'), menu = wrap.querySelector('.pc-menu'),
      search = menu.querySelector('input'), list = menu.querySelector('.opts');
    function paintBtn() { btn.innerHTML = flagSVG(country.iso) + '<span>+' + esc(country.dial) + '</span>'; inp.placeholder = country.mask.replace(/#/g, '0'); }
    function paintList() {
      var q = search.value.toLowerCase().normalize('NFD').replace(/[̀-ͯ]/g, '');
      list.innerHTML = COUNTRIES.filter(function (c) {
        return !q || c.name.toLowerCase().normalize('NFD').replace(/[̀-ͯ]/g, '').indexOf(q) >= 0 || c.dial.indexOf(q.replace('+', '')) === 0;
      }).map(function (c) { return '<div class="opt" data-iso="' + c.iso + '">' + flagSVG(c.iso) + '<span>' + esc(c.name) + '</span><span class="dial">+' + c.dial + '</span></div>'; }).join('');
    }
    function reformat() {
      var d = inp.value.replace(/\D/g, '');
      if (inp.value.trim().charAt(0) === '+') { // pasted a full international number
        var g = guessCountry(d); if (g) { country = g; d = d.slice(g.dial.length); paintBtn(); }
      }
      inp.value = applyMask(d, country.mask);
    }
    btn.addEventListener('click', function () { show(menu, menu.classList.contains('hidden')); search.value = ''; paintList(); search.focus(); });
    search.addEventListener('input', paintList);
    list.addEventListener('click', function (e) { var o = e.target.closest('.opt'); if (!o) return; country = countryByIso(o.dataset.iso); paintBtn(); show(menu, false); reformat(); inp.focus(); });
    document.addEventListener('click', function (e) { if (!wrap.contains(e.target)) show(menu, false); });
    inp.addEventListener('input', reformat);
    paintBtn();
    return {
      value: function () { var d = inp.value.replace(/\D/g, ''); return d ? country.dial + d : ''; },
      clear: function () { inp.value = ''; }
    };
  }

  // ---------- login and routing ----------
  function showLogin(msg) {
    show('app', false); show('login', true); stopTimers();
    if (msg) $('login-msg').textContent = msg;
    setTimeout(function () { $('login-key').focus(); }, 50);
  }
  function stopTimers() { clearInterval(timers.status); clearInterval(timers.pair); timers.status = timers.pair = null; }
  $('login-form').addEventListener('submit', function (e) {
    e.preventDefault(); $('login-err').textContent = '';
    fetch('/panel/api/login', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ key: $('login-key').value }) })
      .then(function (r) { return r.json(); }).then(function (j) { if (!j.ok) throw new Error(j.error || 'No se pudo entrar.'); $('login-key').value = ''; start(); })
      .catch(function (e) { $('login-err').textContent = e.message; });
  });
  $('logout').addEventListener('click', function (e) { e.preventDefault(); api('logout', {}).finally(function () { showLogin('Saliste del panel.'); }); });

  function start() {
    fetch('/panel/api/me', { credentials: 'same-origin' }).then(function (r) { return r.json(); }).then(function (j) {
      if (!j.ok) {
        showLogin(location.hash === '#login-vencido' ? 'El enlace ya se usó o venció. Ábrelo otra vez desde el menú Inicio del servidor.' : null);
        return;
      }
      CSRF = j.csrf; $('ver').textContent = 'v' + j.version;
      show('login', false); show('app', true);
      refreshStatus(); clearInterval(timers.status); timers.status = setInterval(refreshStatus, 5000);
      route();
    }).catch(function () { showLogin('No se pudo conectar con el servicio.'); });
  }
  var SECTIONS = ['inicio', 'vincular', 'conversaciones', 'contactos', 'lista', 'ajustes', 'registro', 'conectar', 'importar'];
  function route() {
    var s = location.hash.replace('#', '');
    if (SECTIONS.indexOf(s) < 0) s = 'inicio';
    cur = s;
    SECTIONS.forEach(function (x) { show('s-' + x, x === s); });
    document.querySelectorAll('#nav a').forEach(function (a) { a.classList.toggle('on', a.dataset.s === s); });
    if (s !== 'vincular') { clearInterval(timers.pair); timers.pair = null; }
    ({ inicio: refreshStatus, vincular: loadPair, conversaciones: function () { loadChats(true); }, contactos: loadContactsSection, lista: loadApproved,
      ajustes: function () { loadSettings(); loadTranscribe(); }, registro: loadAudit, conectar: loadConnection, importar: function () { } })[s]();
  }
  window.addEventListener('hashchange', function () { if (!$('app').classList.contains('hidden')) route(); });

  // ---------- status ----------
  var lastStatus = null;
  function refreshStatus() {
    api('status').then(function (j) {
      lastStatus = j;
      var b = $('banner'); b.className = 'banner ' + j.level;
      var w = j.whatsapp || {};
      $('banner-title').textContent = j.level === 'green' ? 'WhatsApp vinculado y conectado' : j.level === 'amber' ? 'WhatsApp vinculado, sin conexión ahora' : 'WhatsApp no vinculado';
      var sub = [];
      if (j.number) sub.push(fmtPhone(j.number) + (w.name ? ' (' + w.name + ')' : ''));
      if (w.last_connected) sub.push((j.level === 'green' ? 'conectado desde ' : 'última conexión ') + fmtDate(w.last_connected));
      if (j.level === 'red') sub.push('Ve a Vincular WhatsApp para escanear el código.');
      sub.push('Servicio funcionando');
      $('banner-sub').textContent = sub.join(' · ');
      $('st-chats').textContent = j.chats.toLocaleString('es');
      $('st-msgs').textContent = j.messages.toLocaleString('es');
      $('st-unans').textContent = j.unanswered_30d.toLocaleString('es');
      $('st-appr').textContent = j.approved.toLocaleString('es');
      var cloud = j.cloud || {};
      var rows = [
        ['WhatsApp', j.summary],
        ['Base de datos', j.db_ok ? 'Funcionando' : 'Sin conexión'],
        ['Envío de mensajes', j.sending_enabled ? 'Activado' : 'Apagado (solo lectura)'],
        ['Tailscale', (j.tailscale && j.tailscale.length) ? j.tailscale.join(', ') : 'No detectado'],
        ['Puerto', String(j.port)],
        ['Acceso desde la nube', cloud.enabled ? 'Activado' : 'Apagado'],
        ['Panel desde otros equipos', j.panel_remote ? 'Permitido (con clave)' : 'Solo en este servidor'],
        ['Versión', j.version]
      ];
      $('st-kv').innerHTML = rows.map(function (r) { return '<dt>' + esc(r[0]) + '</dt><dd>' + esc(r[1]) + '</dd>'; }).join('');
    }).catch(function (e) {
      if (e.message === 'login') return;
      $('banner').className = 'banner red';
      $('banner-title').textContent = 'No responde el servicio';
      $('banner-sub').textContent = 'Revisa que el servicio WhatsAppMCP esté encendido (menú Inicio > WhatsApp MCP > Estado).';
    });
  }
  $('go-unans').addEventListener('click', function () { $('c-unans').checked = true; });

  // ---------- linking ----------
  var pairPhone = phoneInput($('pair-phone-wrap'));
  function loadPair() {
    api('pair').then(paintPair).catch(errTo('pair-err'));
    clearInterval(timers.pair); timers.pair = setInterval(function () { api('pair').then(paintPair).catch(function () { }); }, 1500);
  }
  function paintPair(j) {
    $('pair-err').textContent = '';
    var running = j.state === 'starting' || j.state === 'qr' || j.state === 'code';
    show('pair-linked', j.paired && !running);
    show('pair-start', !j.paired && !running);
    show('pair-run', running);
    if (j.paired && !running) {
      $('pair-linked-text').textContent = j.summary;
    }
    if (!running && (j.state === 'error' || j.state === 'timeout')) {
      $('pair-err').textContent = j.state === 'timeout' ? 'Se acabó el tiempo. Pulsa otra vez para sacar un código nuevo.' : ('No se pudo vincular: ' + (j.error || ''));
    }
    if (j.state === 'linked' && !$('pair-linked').dataset.told) { $('pair-linked').dataset.told = '1'; toast('¡WhatsApp vinculado!'); refreshStatus(); }
    if (!running) return;
    if (j.state === 'starting') {
      $('pair-help').textContent = 'Preparando el código…'; show('pair-img', false); show('pair-code', false); $('pair-sub').textContent = '';
    } else if (j.state === 'qr') {
      $('pair-help').innerHTML = 'En el teléfono: WhatsApp &gt; <b>Ajustes</b> &gt; <b>Dispositivos vinculados</b> &gt; <b>Vincular un dispositivo</b>, y escanea este código.';
      if (j.qr_png) $('pair-img').src = 'data:image/png;base64,' + j.qr_png;
      show('pair-img', !!j.qr_png); show('pair-code', false);
      $('pair-sub').textContent = 'El código cambia cada pocos segundos; esta página se actualiza sola.';
    } else if (j.state === 'code') {
      $('pair-help').innerHTML = 'En el teléfono: WhatsApp &gt; <b>Dispositivos vinculados</b> &gt; <b>Vincular un dispositivo</b> &gt; <b>Vincular con el número de teléfono</b>, y escribe:';
      $('pair-code').textContent = j.code ? j.code.slice(0, 4) + '-' + j.code.slice(4) : '…';
      show('pair-img', false); show('pair-code', true);
      $('pair-sub').textContent = 'Te llega una notificación en el teléfono; ábrela y escribe el código.';
    }
  }
  $('pair-qr').addEventListener('click', function () { delete $('pair-linked').dataset.told; api('pair', { mode: 'qr' }).then(loadPair).catch(errTo('pair-err')); });
  $('pair-phone').addEventListener('click', function () {
    var p = pairPhone.value(); if (!p) { $('pair-err').textContent = 'Escribe el número que vas a vincular.'; return; }
    delete $('pair-linked').dataset.told; api('pair', { mode: 'phone', phone: p }).then(loadPair).catch(errTo('pair-err'));
  });
  $('pair-cancel').addEventListener('click', function () { api('pair/cancel', {}).then(loadPair).catch(errTo('pair-err')); });
  $('unlink').addEventListener('click', function () {
    if (!confirm('¿Desvincular este WhatsApp del servidor? Claude dejará de leer y enviar hasta que vincules otra vez. El historial guardado se queda.')) return;
    api('unlink', {}).then(function () { toast('Desvinculado.'); refreshStatus(); loadPair(); }).catch(errTo('pair-err'));
  });

  // ---------- conversations ----------
  var chatOffset = 0, chatTimer = null;
  function fillFilterSelects(cats, countries) {
    CATS = cats || [];
    var cs = $('c-cat'), v = cs.value;
    cs.innerHTML = '<option value="">Todas las categorías</option><option value="-">Sin categoría</option>' + CATS.map(function (c) { return '<option>' + esc(c) + '</option>'; }).join('');
    cs.value = v;
    var cc = $('c-country'), cv = cc.value;
    var opts = Object.keys(countries || {}).map(function (iso) { var c = countryByIso(iso); return { iso: iso, name: c ? c.name : iso, n: countries[iso] }; })
      .sort(function (a, b) { return a.name.localeCompare(b.name, 'es'); });
    cc.innerHTML = '<option value="">Todos los países</option>' + opts.map(function (o) { return '<option value="' + o.iso + '">' + esc(o.name) + ' (' + o.n + ')</option>'; }).join('');
    cc.value = cv;
  }
  function countryOptions(sel, suggested) {
    var out = '<option value="">' + (suggested ? 'País: ¿' + esc((countryByIso(suggested) || { name: suggested }).name) + '?' : 'País') + '</option>';
    COUNTRIES.forEach(function (c) { out += '<option value="' + c.iso + '"' + (c.iso === sel ? ' selected' : '') + '>' + esc(c.name) + '</option>'; });
    return out;
  }
  function categoryOptions(sel) {
    var out = '<option value="">Categoría</option>', found = false;
    CATS.forEach(function (c) { if (c === sel) found = true; out += '<option' + (c === sel ? ' selected' : '') + '>' + esc(c) + '</option>'; });
    if (sel && !found) out += '<option selected>' + esc(sel) + '</option>';
    return out;
  }
  function chatRow(c) {
    var name = c.name || (c.phone ? fmtPhone(c.phone) : c.jid);
    var sub = [];
    if (c.phone && c.name) sub.push(fmtPhone(c.phone));
    if (c.is_group) sub.push('Grupo');
    var when = c.last_time ? fmtDate(c.last_time) : '';
    var prev = c.last_text ? (c.last_from_me ? 'Tú: ' : (c.is_group && c.last_sender ? '' : '')) + c.last_text : '';
    var flagIso = c.country || c.country_suggested;
    return '<div class="item" data-jid="' + esc(c.jid) + '" data-send="' + esc(c.send_to) + '" data-name="' + esc(c.name || '') + '">' +
      '<div class="av' + (c.is_group ? ' g' : '') + '">' + esc(initials(name)) + '</div>' +
      '<div><div class="t">' + (flagIso ? flagSVG(flagIso) : '') + '<span>' + esc(name) + '</span>' +
      (c.is_group ? '<span class="tag grp">Grupo</span>' : '') +
      (c.category ? '<span class="tag">' + esc(c.category) + '</span>' : '') +
      (c.unanswered && !c.is_group && c.last_time ? '<span class="tag wait">Sin responder · ' + esc(waited(c.last_time)) + '</span>' : '') + '</div>' +
      '<div class="s">' + esc(sub.concat(when ? [when] : []).join(' · ')) + (prev ? ' — ' + esc(prev) : '') + '</div>' +
      '<div class="meta"><select class="m-cat" aria-label="Categoría">' + categoryOptions(c.category) + '</select>' +
      '<select class="m-country" aria-label="País">' + countryOptions(c.country, c.country ? '' : c.country_suggested) + '</select></div></div>' +
      '<div class="acts"><button class="btn sm ' + (c.approved ? 'on' : 'primary') + ' m-appr">' + (c.approved ? 'Aprobado ✓' : 'Aprobar') + '</button>' +
      '<button class="btn sm m-hist" title="Pide al teléfono los últimos 100 mensajes de este chat, por si falta alguno o un archivo">Pedir mensajes otra vez</button></div></div>';
  }
  function chatQuery() {
    var q = new URLSearchParams();
    if ($('c-q').value.trim()) q.set('q', $('c-q').value.trim());
    if ($('c-kind').value) q.set('kind', $('c-kind').value);
    if ($('c-cat').value) q.set('category', $('c-cat').value);
    if ($('c-country').value) q.set('country', $('c-country').value);
    if ($('c-unans').checked) q.set('unanswered', '1');
    if ($('c-appr').checked) q.set('approved', '1');
    q.set('limit', '60'); q.set('offset', String(chatOffset));
    return q.toString();
  }
  function loadChats(reset) {
    if (reset) chatOffset = 0;
    api('chats?' + chatQuery()).then(function (j) {
      fillFilterSelects(j.categories, j.countries);
      var html = j.chats.map(chatRow).join('');
      if (reset) $('c-list').innerHTML = html || '<p class="muted center">No hay chats con estos filtros.</p>';
      else $('c-list').insertAdjacentHTML('beforeend', html);
      show('c-more', j.more);
    }).catch(errTo());
  }
  ['c-kind', 'c-cat', 'c-country', 'c-unans', 'c-appr'].forEach(function (id) { $(id).addEventListener('change', function () { loadChats(true); }); });
  $('c-q').addEventListener('input', function () { clearTimeout(chatTimer); chatTimer = setTimeout(function () { loadChats(true); }, 300); });
  $('c-more').addEventListener('click', function () { chatOffset += 60; loadChats(false); });
  $('c-list').addEventListener('click', function (e) {
    var h = e.target.closest('.m-hist');
    if (h) {
      h.disabled = true;
      api('chats/history', { jid: h.closest('.item').dataset.jid, count: 100 }).then(function () {
        toast('Pedido al teléfono. Los mensajes que falten llegan en un minuto o dos.');
      }).catch(errTo()).finally(function () { h.disabled = false; });
      return;
    }
    var b = e.target.closest('.m-appr'); if (!b) return;
    var it = b.closest('.item'), on = b.classList.contains('on');
    b.disabled = true;
    var p = on ? api('approved/remove', { recipient: it.dataset.send }) : api('approved', { recipient: it.dataset.send, label: it.dataset.name ? titleCase(it.dataset.name) : '' });
    p.then(function () {
      b.classList.toggle('on', !on); b.classList.toggle('primary', on); b.textContent = on ? 'Aprobar' : 'Aprobado ✓';
      toast(on ? 'Quitado de la lista aprobada.' : 'Añadido a la lista aprobada.');
    }).catch(errTo()).finally(function () { b.disabled = false; });
  });
  $('c-list').addEventListener('change', function (e) {
    var s = e.target, it = s.closest('.item'); if (!it) return;
    var body = { jid: it.dataset.jid };
    if (s.classList.contains('m-cat')) body.category = s.value;
    else if (s.classList.contains('m-country')) body.country = s.value;
    else return;
    api('chats/meta', body).then(function () { toast('Guardado.'); }).catch(errTo());
  });

  // ---------- approved list ----------
  var addPhone = phoneInput($('a-phone-wrap'));
  $('a-name').addEventListener('blur', function () { if (this.value) this.value = titleCase(this.value); });
  function loadApproved() {
    api('approved').then(function (j) {
      var rs = j.approved || [];
      $('a-count').textContent = '(' + rs.length + ')';
      $('a-list').innerHTML = rs.length ? rs.map(function (r) {
        var num = r.jid.split('@')[0], isG = r.kind === 'group';
        var title = r.label || (isG ? 'Grupo' : fmtPhone(num));
        var cc = !isG ? guessCountry(num) : null;
        return '<div class="item"><div class="av' + (isG ? ' g' : '') + '">' + esc(initials(title)) + '</div><div><div class="t">' + (cc ? flagSVG(cc.iso) : '') +
          '<span>' + esc(title) + '</span>' + (isG ? '<span class="tag grp">Grupo</span>' : '') + '</div><div class="s">' +
          esc(isG ? r.jid : fmtPhone(num)) + ' · añadido ' + esc(fmtDate(r.added_at)) + ' por ' + esc(actorName(r.added_by)) + '</div></div>' +
          '<div class="acts"><button class="btn sm danger a-rm" data-jid="' + esc(r.jid) + '">Quitar</button></div></div>';
      }).join('') : '<p class="muted">Todavía no hay nadie. Apruébalos desde Conversaciones o añádelos arriba.</p>';
    }).catch(errTo('a-err'));
  }
  $('a-form').addEventListener('submit', function (e) {
    e.preventDefault(); $('a-err').textContent = '';
    var p = addPhone.value(); if (!p) { $('a-err').textContent = 'Escribe el número.'; return; }
    api('approved', { recipient: p, label: titleCase($('a-name').value) }).then(function () {
      addPhone.clear(); $('a-name').value = ''; toast('Añadido.'); loadApproved();
    }).catch(errTo('a-err'));
  });
  $('a-list').addEventListener('click', function (e) {
    var b = e.target.closest('.a-rm'); if (!b) return;
    if (!confirm('¿Quitar de la lista aprobada? Claude ya no podrá enviarle mensajes.')) return;
    api('approved/remove', { recipient: b.dataset.jid }).then(function () { toast('Quitado.'); loadApproved(); }).catch(errTo('a-err'));
  });
  function actorName(a) { return { panel: 'el panel', manual: 'el servidor', claude: 'Claude', cloud: 'Claude (nube)', rest: 'API', installer: 'el instalador', system: 'el sistema' }[a] || a; }

  // ---------- settings ----------
  var SET_TEXT = {
    sending_enabled: ['Envío de mensajes', 'Interruptor general para enviar mensajes y archivos. Apagado = solo lectura.'],
    send_only_to_approved: ['Enviar solo a la lista aprobada', 'Claude solo envía a los contactos y grupos aprobados.'],
    attachments_folder_only: ['Adjuntos solo desde una carpeta', 'Solo permite enviar archivos que estén dentro de la carpeta de adjuntos.'],
    attachments_folder: ['Carpeta de adjuntos', 'Carpeta de la que deben salir los archivos cuando la opción anterior está activada.'],
    max_attachment_mb: ['Tamaño máximo de un archivo (MB)', 'El archivo más grande que se puede enviar.'],
    max_sends_per_minute: ['Envíos por minuto como máximo', 'Protege el número de un bloqueo por envíos masivos.'],
    mcp_can_change_settings: ['Claude puede cambiar ajustes', 'Permite que Claude cambie ajustes, la lista aprobada, la categoría y el país de los chats cuando se lo pidas. Todo queda en el registro.'],
    audit_message_text: ['Guardar el texto enviado en el registro', 'Apagado = solo se guarda la longitud del mensaje.'],
    download_media_enabled: ['Descargar archivos recibidos', 'Permite bajar fotos, audios, videos y documentos recibidos.'],
    chat_categories: ['Categorías de chats', 'Separadas por comas. Son las que aparecen en Conversaciones.'],
    cloud_access_enabled: ['Acceso desde la nube (Tailscale Funnel)', 'Deja que Claude en claude.ai use tu WhatsApp con la clave de la nube. Apagado = solo equipos de tu Tailscale. Ve a Conectar Claude para los pasos.'],
    group_contacts_to_approved: ['Contactos de grupos a la lista aprobada', 'Al importar contactos de los grupos elegidos, también se añaden a la lista aprobada. Los que ya estaban no se tocan.'],
    read_media_enabled: ['Claude puede abrir archivos recibidos', 'Fotos, Excel, CSV, PDF, Word y notas de voz. Todo queda en el registro.'],
    read_media_max_mb: ['Tamaño máximo para abrir un archivo (MB)', 'Archivos más grandes no se abren para Claude.'],
    transcribe_audio_enabled: ['Transcribir notas de voz', 'Convierte las notas de voz en texto aquí en el servidor. Necesita la transcripción instalada (abajo).'],
    transcribe_language: ['Idioma de las notas de voz', 'Automático detecta español o inglés solo.'],
    panel_remote: ['Panel desde otros equipos', 'Permite abrir este panel desde otros equipos de tu Tailscale, entrando con la clave. Apagado = solo en este servidor.']
  };
  function loadSettings() {
    api('settings').then(function (j) {
      $('set-list').innerHTML = j.settings.map(function (s) {
        var t = SET_TEXT[s.key] || [s.key, s.description], ctl;
        if (s.kind === 'bool') ctl = '<label class="switch"><input type="checkbox" data-k="' + s.key + '"' + (s.value ? ' checked' : '') + '><span></span></label>';
        else if (s.key === 'transcribe_language') ctl = '<select data-k="' + s.key + '" class="s-sel">' + [['auto', 'Automático'], ['es', 'Español'], ['en', 'Inglés']].map(function (o) {
          return '<option value="' + o[0] + '"' + (s.value === o[0] ? ' selected' : '') + '>' + o[1] + '</option>'; }).join('') + '</select>';
        else if (s.kind === 'int') ctl = '<div class="row"><input type="number" min="' + (s.min || '') + '" max="' + (s.max || '') + '" value="' + esc(s.value) + '" data-k="' + s.key + '"><button class="btn sm s-save" data-k="' + s.key + '">Guardar</button></div>';
        else ctl = '<div class="row"><input class="grow" value="' + esc(s.value) + '" data-k="' + s.key + '"><button class="btn sm s-save" data-k="' + s.key + '">Guardar</button></div>';
        return '<div class="set"><div><div class="n">' + esc(t[0]) + (s.only_by_hand ? '<span class="lock">Solo a mano</span>' : '') + '</div><div class="muted small">' + esc(t[1]) + '</div></div>' + ctl + '</div>';
      }).join('');
    }).catch(errTo());
  }
  function saveSetting(k, v, el) {
    api('settings', { key: k, value: String(v) }).then(function () { toast('Guardado.'); refreshStatus(); if (k === 'chat_categories') CATS = []; })
      .catch(function (e) { errTo()(e); loadSettings(); });
  }
  $('set-list').addEventListener('change', function (e) {
    var i = e.target;
    if (i.classList.contains('s-sel')) { saveSetting(i.dataset.k, i.value, i); return; }
    if (i.type !== 'checkbox' || !i.dataset.k) return;
    if (i.dataset.k === 'send_only_to_approved' && !i.checked && !confirm('Si lo apagas, Claude podrá enviar a cualquier número. ¿Seguro?')) { i.checked = true; return; }
    if (i.dataset.k === 'cloud_access_enabled' && i.checked && !confirm('Claude en la nube podrá leer y enviar (a la lista aprobada) desde internet con la clave de la nube. ¿Activar?')) { i.checked = false; return; }
    saveSetting(i.dataset.k, i.checked, i);
  });
  $('set-list').addEventListener('click', function (e) {
    var b = e.target.closest('.s-save'); if (!b) return;
    var i = $('set-list').querySelector('input[data-k="' + b.dataset.k + '"]');
    saveSetting(b.dataset.k, i.value, i);
  });

  // ---------- contacts from groups ----------
  var GROUPS = [], k2Offset = 0, k2Timer = null;
  function loadContactsSection() { loadGroups(); loadContacts(true); }
  function loadGroups() {
    $('g-err').textContent = '';
    api('contacts/groups').then(function (j) {
      GROUPS = j.groups || [];
      $('g-appr').innerHTML = (j.add_to_approved ? 'Los contactos importados <b>también se añaden a la lista aprobada</b>.' : 'Los contactos importados <b>no</b> se añaden a la lista aprobada.') +
        ' Se cambia en <a href="#ajustes">Ajustes</a>.' + (j.live ? '' : ' <span class="hint">WhatsApp no está conectado: se muestran los grupos del historial.</span>');
      paintGroups();
      var sel = $('k2-group'), curG = sel.value;
      sel.innerHTML = '<option value="">Todos los grupos</option>' + GROUPS.filter(function (g) { return g.selected; }).map(function (g) {
        return '<option value="' + esc(g.jid) + '"' + (g.jid === curG ? ' selected' : '') + '>' + esc(g.name || g.jid) + '</option>'; }).join('');
    }).catch(errTo('g-err'));
  }
  function paintGroups() {
    var q = $('g-q').value.trim().toLowerCase(), only = $('g-only').checked;
    var rows = GROUPS.filter(function (g) { return (!only || g.selected) && (!q || (g.name || '').toLowerCase().indexOf(q) >= 0); });
    $('g-list').innerHTML = rows.map(function (g) {
      var sub = [];
      if (g.members >= 0) sub.push(g.members + ' miembros');
      if (g.last_import_at) sub.push('importado ' + fmtDate(g.last_import_at));
      return '<label class="grow-item" data-jid="' + esc(g.jid) + '" data-name="' + esc(g.name || '') + '"><input type="checkbox" class="g-sel"' + (g.selected ? ' checked' : '') + '>' +
        '<div class="av g">' + esc(initials(g.name || '#')) + '</div><span class="gn">' + esc(g.name || g.jid) + '</span><span class="gs">' + esc(sub.join(' · ')) + '</span></label>';
    }).join('') || '<p class="muted center">' + (GROUPS.length ? 'Ningún grupo coincide.' : 'No hay grupos.') + '</p>';
    var n = GROUPS.filter(function (g) { return g.selected; }).length;
    $('g-import').textContent = n ? 'Importar contactos de ' + (n === 1 ? '1 grupo elegido' : n + ' grupos elegidos') : 'Importar contactos de los grupos elegidos';
    $('g-import').disabled = !n;
  }
  $('g-q').addEventListener('input', paintGroups);
  $('g-only').addEventListener('change', paintGroups);
  $('g-list').addEventListener('change', function (e) {
    var c = e.target; if (!c.classList.contains('g-sel')) return;
    var it = c.closest('.grow-item');
    api('contacts/groups', { jid: it.dataset.jid, name: it.dataset.name, selected: c.checked }).then(function () {
      GROUPS.forEach(function (g) { if (g.jid === it.dataset.jid) g.selected = c.checked; });
      paintGroups(); toast(c.checked ? 'Grupo elegido.' : 'Grupo quitado.');
    }).catch(function (e) { c.checked = !c.checked; errTo('g-err')(e); });
  });
  $('g-import').addEventListener('click', function () {
    var b = this; b.disabled = true; $('g-err').textContent = ''; $('g-result').innerHTML = '<p class="muted">Leyendo los grupos…</p>';
    api('contacts/import', {}).then(function (j) {
      var s = j.summary;
      $('g-result').innerHTML = '<p class="ok-text">Listo: ' + s.new + ' contactos nuevos, ' + s.updated + ' actualizados' + (s.add_to_approved ? ', ' + s.approved + ' añadidos a la lista aprobada' : '') + '.</p>' +
        (s.no_phone ? '<p class="muted small">' + s.no_phone + ' tienen el número oculto por WhatsApp: se guardan con su código interno y se pueden aprobar igual.</p>' : '') +
        '<ul class="small">' + s.groups.map(function (g) {
          return '<li><b>' + esc(g.name || g.jid) + '</b>: ' + (g.error ? '<span class="err">' + esc(esErr(g.error)) + '</span>' : g.members + ' miembros, ' + g.new + ' nuevos' + (s.add_to_approved ? ', ' + g.approved + ' aprobados' : '')) + '</li>';
        }).join('') + '</ul>';
      loadGroups(); loadContacts(true);
    }).catch(function (e) { $('g-result').innerHTML = ''; errTo('g-err')(e); }).finally(function () { b.disabled = false; });
  });
  function contactRow(c) {
    var phone = c.phone ? fmtPhone(c.phone) : (c.phone_hint ? c.phone_hint + ' (número oculto)' : 'Número oculto');
    var iso = c.country;
    return '<div class="item" data-jid="' + esc(c.jid) + '"><div class="av">' + esc(initials(c.name || '?')) + '</div><div>' +
      '<div class="t">' + (iso ? flagSVG(iso) : '') + '<input class="k2-name" value="' + esc(c.name) + '" placeholder="Sin nombre" aria-label="Nombre">' +
      (c.approved ? '<span class="tag">Aprobado</span>' : '') + '</div>' +
      '<div class="s"><span class="' + (c.phone ? '' : 'hint') + '">' + esc(phone) + '</span>' +
      (c.groups && c.groups.length ? ' · ' + esc(c.groups.map(function (g) { return g.name || g.jid; }).join(', ')) : '') + '</div>' +
      '<div class="meta"><select class="k2-country" aria-label="País">' + countryOptions(c.country, '') + '</select></div></div>' +
      '<div class="acts"><button class="btn sm danger k2-del">Borrar</button></div></div>';
  }
  function loadContacts(reset) {
    if (reset) k2Offset = 0;
    var q = new URLSearchParams();
    if ($('k2-q').value.trim()) q.set('q', $('k2-q').value.trim());
    if ($('k2-group').value) q.set('group', $('k2-group').value);
    if ($('k2-country').value) q.set('country', $('k2-country').value);
    q.set('limit', '100'); q.set('offset', String(k2Offset));
    api('contacts?' + q.toString()).then(function (j) {
      if (!$('k2-country').options.length) $('k2-country').innerHTML = '<option value="">Todos los países</option><option value="-">Sin país</option>' +
        COUNTRIES.map(function (c) { return '<option value="' + c.iso + '">' + esc(c.name) + '</option>'; }).join('');
      $('k2-count').textContent = '(' + j.total + ')';
      var html = j.contacts.map(contactRow).join('');
      var filtered = q.has('q') || q.has('group') || q.has('country');
      if (reset) $('k2-list').innerHTML = html || '<p class="muted center">' + (filtered ? 'Ningún contacto coincide.' : 'Todavía no hay contactos. Elige grupos arriba y pulsa Importar.') + '</p>';
      else $('k2-list').insertAdjacentHTML('beforeend', html);
      show('k2-more', k2Offset + j.contacts.length < j.total);
    }).catch(errTo());
  }
  $('k2-q').addEventListener('input', function () { clearTimeout(k2Timer); k2Timer = setTimeout(function () { loadContacts(true); }, 300); });
  ['k2-group', 'k2-country'].forEach(function (id) { $(id).addEventListener('change', function () { loadContacts(true); }); });
  $('k2-more').addEventListener('click', function () { k2Offset += 100; loadContacts(false); });
  $('k2-list').addEventListener('change', function (e) {
    var el = e.target, it = el.closest('.item'); if (!it) return;
    var body = { jid: it.dataset.jid };
    if (el.classList.contains('k2-name')) { el.value = titleCase(el.value); if (!el.value) { toast('El nombre no puede quedar vacío.'); return; } body.name = el.value; }
    else if (el.classList.contains('k2-country')) body.country = el.value;
    else return;
    api('contacts/edit', body).then(function () {
      toast('Guardado.');
      if (body.country !== undefined) { var t = it.querySelector('.t'), f = t.querySelector('.flag'); if (f) f.remove(); if (body.country) t.insertAdjacentHTML('afterbegin', flagSVG(body.country)); }
    }).catch(errTo());
  });
  $('k2-list').addEventListener('click', function (e) {
    var b = e.target.closest('.k2-del'); if (!b) return;
    var it = b.closest('.item');
    if (!confirm('¿Borrar este contacto? No se quita de la lista aprobada.')) return;
    api('contacts/remove', { jid: it.dataset.jid }).then(function () { it.remove(); toast('Contacto borrado.'); }).catch(errTo());
  });

  // ---------- voice note transcription add-on ----------
  var trTimer = null;
  function loadTranscribe() {
    clearTimeout(trTimer);
    api('transcribe').then(function (j) {
      var t = j.state || {}, rows = [];
      if (!t.supported) {
        rows.push(['Estado', 'Solo se instala en el servidor Windows']);
        show('tr-install', false); show('tr-remove', false); show('tr-bar', false);
      } else {
        var state = t.installing ? 'Instalando… ' + t.percent + '%' : t.installed ? 'Instalada' : t.error ? 'No se pudo instalar' : 'No instalada';
        rows.push(['Estado', state]);
        if (t.installing && t.step) rows.push(['Paso', t.step]);
        if (t.installed) rows.push(['Funciona con', t.device === 'cuda' ? 'Tarjeta gráfica NVIDIA (rápido)' : 'Procesador (más lento)']);
        if (t.installed && t.device !== 'cuda' && t.detail) rows.push(['Por qué no la tarjeta', t.detail]);
        rows.push(['Modelo', t.model || '']);
        show('tr-bar', t.installing); $('tr-fill').style.width = (t.percent || 0) + '%';
        show('tr-install', !t.installing); $('tr-install').textContent = t.installed ? 'Instalar otra vez' : 'Instalar transcripción';
        show('tr-remove', t.installed && !t.installing);
        $('tr-err').textContent = t.error || '';
        if (t.installing && cur === 'ajustes') trTimer = setTimeout(loadTranscribe, 2000);
      }
      $('tr-kv').innerHTML = rows.map(function (r) { return '<dt>' + esc(r[0]) + '</dt><dd>' + esc(r[1]) + '</dd>'; }).join('');
    }).catch(errTo('tr-err'));
  }
  $('tr-install').addEventListener('click', function () {
    if (!confirm('Se descargan unos 3 GB en este servidor. Puede tardar un buen rato. ¿Empezar?')) return;
    api('transcribe/install', {}).then(function () { toast('Instalando. Puedes seguir usando el panel.'); loadTranscribe(); }).catch(errTo('tr-err'));
  });
  $('tr-remove').addEventListener('click', function () {
    if (!confirm('¿Quitar la transcripción? Se borran Python, las librerías y el modelo (unos 3 GB). Las notas ya transcritas se quedan.')) return;
    api('transcribe/remove', {}).then(function () { toast('Transcripción quitada.'); loadTranscribe(); }).catch(errTo('tr-err'));
  });

  // ---------- log ----------
  var ACTION_TEXT = {
    'message.send': 'Envío', 'media.download': 'Descarga de archivo', 'setting.change': 'Ajuste cambiado', 'approved.add': 'Añadido a la lista',
    'approved.remove': 'Quitado de la lista', 'session.logged_out': 'WhatsApp cerró la sesión', 'session.temporary_ban': 'Bloqueo temporal de WhatsApp', 'chat.meta': 'Categoría o país', 'session.paired': 'WhatsApp vinculado', 'session.unlinked': 'WhatsApp desvinculado',
    'panel.login': 'Entrada al panel', 'panel.show_key': 'Clave mostrada', 'history.import': 'Historial importado', 'cloud.request': 'Acceso desde la nube',
    'cloud.denied': 'Nube: clave incorrecta', 'contacts.import': 'Contactos importados', 'contacts.group_add': 'Grupo elegido para contactos',
    'contacts.group_remove': 'Grupo quitado de contactos', 'contact.edit': 'Contacto editado', 'contact.remove': 'Contacto borrado', 'group.members': 'Miembros de un grupo',
    'media.read': 'Archivo abierto por Claude', 'history.request': 'Mensajes pedidos otra vez', 'transcribe.install': 'Instalación de transcripción', 'transcribe.remove': 'Transcripción quitada', 'cloud.funnel_on': 'Funnel publicado', 'cloud.funnel_off': 'Funnel quitado', 'cloud.key_rotated': 'Clave de la nube cambiada', 'auth.denied': 'Acceso rechazado'
  };
  function loadAudit() {
    var f = $('r-filter').value;
    api('audit?limit=200' + (f ? '&action=' + encodeURIComponent(f) : '')).then(function (j) {
      $('r-body').innerHTML = (j.entries || []).map(function (a) {
        var det = '';
        if (a.details) {
          if (a.details.key) det = a.details.key + ': ' + JSON.stringify(a.details.old) + ' → ' + JSON.stringify(a.details.new);
          else if (a.details.category || a.details.country) det = ['category', 'country'].filter(function (k) { return a.details[k]; }).map(function (k) { return (k === 'category' ? 'categoría ' : 'país ') + (a.details[k].old || '—') + ' → ' + (a.details[k].new || '—'); }).join(', ');
          else if (a.details.new_name || a.details.new_country) det = [a.details.new_name ? 'nombre ' + (a.details.old_name || '—') + ' → ' + a.details.new_name : '', a.details.new_country !== undefined ? 'país ' + (a.details.old_country || '—') + ' → ' + (a.details.new_country || '—') : ''].filter(Boolean).join(', ');
          else if (a.action === 'contacts.import') det = a.details.groups + ' grupos: ' + a.details.new + ' nuevos, ' + a.details.updated + ' actualizados, ' + a.details.approved + ' a la lista aprobada';
          else if (a.details.error) det = a.details.error;
        }
        return '<tr><td>' + esc(fmtDate(a.at)) + '</td><td>' + esc(actorName(a.actor)) + '</td><td>' + esc(ACTION_TEXT[a.action] || a.action) + (det ? '<div class="muted small">' + esc(det) + '</div>' : '') +
          '</td><td>' + esc(a.target) + '</td><td class="o-' + esc(a.outcome) + '">' + esc({ ok: 'Bien', denied: 'Rechazado', error: 'Error' }[a.outcome] || a.outcome) + '</td></tr>';
      }).join('') || '<tr><td colspan="5" class="muted">Sin registros.</td></tr>';
    }).catch(errTo());
  }
  $('r-filter').addEventListener('change', loadAudit);

  // ---------- connect ----------
  var token = '', tokenShown = false;
  function loadConnection() {
    api('connection').then(function (j) {
      token = j.token || ''; tokenShown = false;
      var url = (j.urls && j.urls[0]) || '';
      $('k-kv').innerHTML = '<dt>Dirección</dt><dd>' + esc((j.urls || []).join('\n') || 'Tailscale no detectado') + '</dd><dt>Clave</dt><dd id="k-tok">••••••••••••</dd>';
      $('k-cmd').textContent = 'claude mcp add --transport http whatsapp ' + (url || 'http://IP-DE-TAILSCALE:8765/mcp') + ' --header "Authorization: Bearer CLAVE"';
      paintCloud(j.cloud);
    }).catch(errTo());
  }
  var cloudURL = '', cloudShown = false, cloudFunnel = false, nodeTimer = null;
  var nodeStates = {
    off: 'Apagado', needs_key: 'Falta la clave de Tailscale', starting: 'Conectando…',
    needs_approval: 'Esperando aprobación en la página de Tailscale (Machines > Approve)',
    running: 'Publicado', error: 'Con problema'
  };
  function paintCloud(c) {
    c = c || {}; cloudURL = c.url || ''; cloudShown = false; cloudFunnel = !!c.funnel;
    var masked = cloudURL ? cloudURL.replace(/\/k\/.*/, '/k/••••••••') : '';
    var nrows = [['Estado', nodeStates[c.node_state] || c.node_state || 'Apagado']];
    if (c.node_dns) nrows.push(['Nombre en Tailscale', c.node_dns]);
    nrows.push(['Unido a tu Tailscale', c.node_joined ? 'Sí (no hace falta clave para volver a encender)' : 'Todavía no']);
    if (c.node_error) nrows.push(['Detalle', c.node_error]);
    $('nd-kv').innerHTML = nrows.map(function (r) { return '<dt>' + esc(r[0]) + '</dt><dd>' + esc(r[1]) + '</dd>'; }).join('');
    if (document.activeElement !== $('nd-host')) $('nd-host').value = c.node_hostname || 'whatsapp-mcp';
    $('nd-connect').textContent = c.node_joined ? 'Conectar / Encender' : 'Conectar';
    show('nd-off', !!c.node_enabled); show('nd-forget', !!c.node_joined);

    var rows = [['Acceso desde la nube', c.enabled ? 'Encendido' : 'Apagado (enciéndelo en Ajustes)']];
    if (cloudURL) rows.push(['Dirección para Claude', '<span id="cl-url">' + esc(masked) + '</span>']);
    if (c.note) rows.push(['Nota', c.note]);
    $('cl-kv').innerHTML = rows.map(function (r) { return '<dt>' + esc(r[0]) + '</dt><dd>' + (r[0] === 'Dirección para Claude' ? r[1] : esc(r[1])) + '</dd>'; }).join('');
    show('cl-url-show', !!cloudURL); show('cl-url-copy', !!cloudURL);
    $('cl-url-show').textContent = 'Mostrar dirección';

    var orows = [
      ['Funnel :10000', !c.funnel_known ? 'No se pudo consultar Tailscale' : (c.funnel ? 'Publicado en internet' : 'No publicado')],
      ['Puerto local', (c.local_addr || '') + (c.listening ? '' : ' (sin escuchar)')],
      ['Comando de Funnel', c.funnel_cmd || '']
    ];
    if (c.node_enabled) orows.push(['Nota', 'Con la dirección propia encendida, este método no hace falta; el Funnel :10000 se quita solo al publicar.']);
    $('cl-old-kv').innerHTML = orows.map(function (r) { return '<dt>' + esc(r[0]) + '</dt><dd>' + esc(r[1]) + '</dd>'; }).join('');
    $('cl-funnel').textContent = c.funnel ? 'Quitar de Funnel :10000' : 'Publicar en Funnel :10000';
    show('cl-funnel', !c.node_enabled || !!c.funnel);

    clearTimeout(nodeTimer);
    if (c.node_enabled && c.node_state !== 'running' && c.node_state !== 'needs_key') {
      nodeTimer = setTimeout(function () { if (!$('s-conectar') || !$('s-conectar').classList.contains('hidden')) refreshCloud(); }, 3000);
    }
  }
  function refreshCloud() { api('connection').then(function (j) { paintCloud(j.cloud); }).catch(function () {}); }
  function nodeConnect() {
    var key = $('nd-key').value.trim(), host = $('nd-host').value.trim().toLowerCase();
    $('cl-err').textContent = '';
    if (key && key.indexOf('tskey-') !== 0) { $('cl-err').textContent = 'Eso no parece una clave de Tailscale (empieza por tskey-auth-).'; return; }
    if (host && !/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/.test(host)) { $('cl-err').textContent = 'El nombre solo puede tener letras minúsculas, números y guiones.'; return; }
    if (key && !confirm('WhatsApp MCP entra a tu Tailscale como "' + (host || 'whatsapp-mcp') + '" y se publica en internet en el puerto 443, con una clave de la nube NUEVA (la dirección anterior deja de funcionar). El Funnel :10000 se quita solo. ¿Seguir?')) return;
    var b = $('nd-connect'); b.disabled = true;
    api('cloud/node', { auth_key: key, hostname: host }).then(function (j) {
      $('nd-key').value = ''; paintCloud(j.cloud); toast(key ? 'Conectando con Tailscale. La clave de la nube es nueva.' : 'Encendiendo.');
    }).catch(errTo('cl-err')).finally(function () { b.disabled = false; });
  }
  $('nd-form').addEventListener('submit', function (e) { e.preventDefault(); nodeConnect(); });
  $('nd-connect').addEventListener('click', nodeConnect);
  $('nd-host').addEventListener('input', function () { var p = this.selectionStart; this.value = this.value.toLowerCase().replace(/[^a-z0-9-]/g, ''); try { this.setSelectionRange(p, p); } catch (e) {} });
  $('nd-off').addEventListener('click', function () {
    if (!confirm('La dirección propia deja de responder hasta que pulses Conectar otra vez. ¿Apagar?')) return;
    api('cloud/node/off', {}).then(function (j) { paintCloud(j.cloud); toast('Apagado.'); }).catch(errTo('cl-err'));
  });
  $('nd-forget').addEventListener('click', function () {
    if (!confirm('El equipo sale de tu Tailscale y se borra su sesión. Para volver hará falta una clave nueva. Después bórralo también en la página de Tailscale (Machines). ¿Olvidar?')) return;
    var b = this; b.disabled = true;
    api('cloud/node/forget', {}).then(function (j) { paintCloud(j.cloud); toast('Equipo olvidado.'); }).catch(errTo('cl-err')).finally(function () { b.disabled = false; });
  });
  $('cl-url-show').addEventListener('click', function () {
    cloudShown = !cloudShown; var el = $('cl-url'); if (!el) return;
    el.textContent = cloudShown ? cloudURL : cloudURL.replace(/\/k\/.*/, '/k/••••••••'); this.textContent = cloudShown ? 'Ocultar dirección' : 'Mostrar dirección';
  });
  $('cl-url-copy').addEventListener('click', function () {
    if (navigator.clipboard && cloudURL) navigator.clipboard.writeText(cloudURL).then(function () { toast('Dirección copiada. Pégala en claude.ai.'); }, function () { toast('No se pudo copiar; usa Mostrar dirección.'); });
  });
  $('cl-funnel').addEventListener('click', function () {
    var on = !cloudFunnel; $('cl-err').textContent = '';
    if (on && !confirm('Esto publica el conector en internet (con la clave de la nube). ¿Publicar con Tailscale Funnel?')) return;
    var b = this; b.disabled = true;
    api('cloud/funnel', { on: on }).then(function (j) { paintCloud(j.cloud); toast(on ? 'Publicado en Funnel.' : 'Quitado de Funnel.'); })
      .catch(errTo('cl-err')).finally(function () { b.disabled = false; });
  });
  $('cl-rotate').addEventListener('click', function () {
    if (!confirm('Se crea una clave nueva y la dirección actual deja de funcionar. Tendrás que pegar la nueva en claude.ai. ¿Seguir?')) return;
    api('cloud/rotate', {}).then(function (j) { paintCloud(j.cloud); toast('Clave de la nube cambiada.'); }).catch(errTo('cl-err'));
  });
  $('k-show').addEventListener('click', function () { tokenShown = !tokenShown; $('k-tok').textContent = tokenShown ? token : '••••••••••••'; this.textContent = tokenShown ? 'Ocultar clave' : 'Mostrar clave'; });
  $('k-copy').addEventListener('click', function () {
    if (navigator.clipboard && token) navigator.clipboard.writeText(token).then(function () { toast('Clave copiada.'); }, function () { toast('No se pudo copiar; usa Mostrar clave.'); });
  });

  // ---------- import ----------
  $('i-form').addEventListener('submit', function (e) {
    e.preventDefault(); var m = $('i-msg'); m.className = ''; m.textContent = 'Importando… puede tardar unos minutos.';
    api('import', { path: $('i-path').value }).then(function (j) {
      m.textContent = 'Listo: ' + j.result.ChatsImported + ' chats y ' + j.result.MessagesImported + ' mensajes.'; refreshStatus();
    }).catch(function (e) { m.className = 'err'; m.textContent = e.message; });
  });

  start();
})();
