// Countries for the phone and country selectors: Spanish name, calling
// code, a typing mask (# = digit) and a small drawn flag (no internet needed).
(function () {
  function h(cols, w) { // horizontal stripes, optional weights
    w = w || cols.map(function () { return 1; });
    var tot = w.reduce(function (a, b) { return a + b; }, 0), y = 0, out = '';
    cols.forEach(function (c, i) { var hh = 20 * w[i] / tot; out += '<rect x="0" y="' + y + '" width="30" height="' + (hh + 0.1) + '" fill="' + c + '"/>'; y += hh; });
    return out;
  }
  function v(cols) {
    var ww = 30 / cols.length, out = '';
    cols.forEach(function (c, i) { out += '<rect x="' + (i * ww) + '" y="0" width="' + (ww + 0.1) + '" height="20" fill="' + c + '"/>'; });
    return out;
  }
  function dot(cx, cy, r, c) { return '<circle cx="' + cx + '" cy="' + cy + '" r="' + r + '" fill="' + c + '"/>'; }
  var F = {
    US: function () { var s = ''; for (var i = 0; i < 13; i++) s += '<rect x="0" y="' + (i * 20 / 13) + '" width="30" height="' + (20 / 13 + 0.05) + '" fill="' + (i % 2 ? '#fff' : '#b22234') + '"/>'; return s + '<rect width="13" height="10.8" fill="#3c3b6e"/>'; },
    MX: function () { return v(['#006847', '#fff', '#ce1126']) + dot(15, 10, 2.6, '#8c6b3a'); },
    CO: function () { return h(['#fcd116', '#003893', '#ce1126'], [2, 1, 1]); },
    ES: function () { return h(['#aa151b', '#f1bf00', '#aa151b'], [1, 2, 1]); },
    PA: function () { return '<rect width="30" height="20" fill="#fff"/><rect x="15" width="15" height="10" fill="#da121a"/><rect y="10" width="15" height="10" fill="#072357"/>' + dot(7.5, 5, 2, '#072357') + dot(22.5, 15, 2, '#da121a'); },
    VE: function () { return h(['#ffcc00', '#00247d', '#cf142b']); },
    PE: function () { return v(['#d91023', '#fff', '#d91023']); },
    EC: function () { return h(['#ffdd00', '#034ea2', '#ed1c24'], [2, 1, 1]) + dot(15, 9, 2.4, '#7a5c2e'); },
    CL: function () { return h(['#fff', '#d52b1e']) + '<rect width="10" height="10" fill="#0039a6"/>' + dot(5, 5, 1.8, '#fff'); },
    AR: function () { return h(['#74acdf', '#fff', '#74acdf']) + dot(15, 10, 2, '#f6b40e'); },
    BR: function () { return '<rect width="30" height="20" fill="#009c3b"/><polygon points="15,2.5 27.5,10 15,17.5 2.5,10" fill="#ffdf00"/>' + dot(15, 10, 4.2, '#002776'); },
    CR: function () { return h(['#002b7f', '#fff', '#ce1126', '#fff', '#002b7f'], [1, 1, 2, 1, 1]); },
    GT: function () { return v(['#4997d0', '#fff', '#4997d0']); },
    SV: function () { return h(['#0f47af', '#fff', '#0f47af']); },
    HN: function () { return h(['#0073cf', '#fff', '#0073cf']) + dot(15, 10, 1.2, '#0073cf'); },
    NI: function () { return h(['#0067c6', '#fff', '#0067c6']) + dot(15, 10, 1.6, '#c8a400'); },
    BO: function () { return h(['#d52b1e', '#f9e300', '#007934']); },
    PY: function () { return h(['#d52b1e', '#fff', '#0038a8']); },
    UY: function () { var s = '<rect width="30" height="20" fill="#fff"/>'; for (var i = 1; i < 9; i += 2) s += '<rect y="' + (i * 20 / 9) + '" width="30" height="' + (20 / 9) + '" fill="#0038a8"/>'; return s + '<rect width="11" height="11.1" fill="#fff"/>' + dot(5.5, 5.5, 2.6, '#fcd116'); },
    DO: function () { return '<rect width="30" height="20" fill="#fff"/><rect width="13" height="8" fill="#002d62"/><rect x="17" width="13" height="8" fill="#ce1126"/><rect y="12" width="13" height="8" fill="#ce1126"/><rect x="17" y="12" width="13" height="8" fill="#002d62"/>'; },
    CU: function () { return h(['#002a8f', '#fff', '#002a8f', '#fff', '#002a8f']) + '<polygon points="0,0 13,10 0,20" fill="#cf142b"/>'; },
    PR: function () { return h(['#ed0000', '#fff', '#ed0000', '#fff', '#ed0000']) + '<polygon points="0,0 13,10 0,20" fill="#0050f0"/>'; },
    CA: function () { return v(['#d80621', '#fff', '#fff', '#d80621']) + '<polygon points="15,5 17,9 19,8.5 17.5,12.5 15,12 12.5,12.5 11,8.5 13,9" fill="#d80621"/>'; },
    FR: function () { return v(['#0055a4', '#fff', '#ef4135']); },
    DE: function () { return h(['#000', '#dd0000', '#ffce00']); },
    IT: function () { return v(['#009246', '#fff', '#ce2b37']); },
    PT: function () { return '<rect width="12" height="20" fill="#006600"/><rect x="12" width="18" height="20" fill="#ff0000"/>' + dot(12, 10, 3, '#ffcc00'); },
    GB: function () { return '<rect width="30" height="20" fill="#012169"/><path d="M0 0L30 20M30 0L0 20" stroke="#fff" stroke-width="4"/><path d="M0 0L30 20M30 0L0 20" stroke="#c8102e" stroke-width="1.5"/><rect x="12" width="6" height="20" fill="#fff"/><rect y="7" width="30" height="6" fill="#fff"/><rect x="13.3" width="3.4" height="20" fill="#c8102e"/><rect y="8.3" width="30" height="3.4" fill="#c8102e"/>'; },
    NL: function () { return h(['#ae1c28', '#fff', '#21468b']); },
    CN: function () { return '<rect width="30" height="20" fill="#de2910"/>' + dot(6, 6, 2.6, '#ffde00'); },
    IN: function () { return h(['#ff9933', '#fff', '#138808']) + dot(15, 10, 2, '#000080'); },
    AE: function () { return h(['#00732f', '#fff', '#000']) + '<rect width="8" height="20" fill="#ff0000"/>'; },
    IL: function () { return '<rect width="30" height="20" fill="#fff"/><rect y="2.5" width="30" height="2.5" fill="#0038b8"/><rect y="15" width="30" height="2.5" fill="#0038b8"/>'; },
    JP: function () { return '<rect width="30" height="20" fill="#fff"/>' + dot(15, 10, 5, '#bc002d'); },
    KR: function () { return '<rect width="30" height="20" fill="#fff"/>' + dot(15, 10, 4.5, '#cd2e3a') + '<path d="M10.5 10a4.5 4.5 0 009 0z" fill="#0047a0"/>'; },
    XX: function () { return '<rect width="30" height="20" fill="#9aa5b1"/>' + dot(15, 10, 6, '#cfd6dd'); }
  };
  var list = [
    ['US', 'Estados Unidos', '1', '(###) ###-####'], ['MX', 'México', '52', '## #### ####'], ['CO', 'Colombia', '57', '### ### ####'],
    ['ES', 'España', '34', '### ## ## ##'], ['PA', 'Panamá', '507', '####-####'], ['VE', 'Venezuela', '58', '###-#######'],
    ['PE', 'Perú', '51', '### ### ###'], ['EC', 'Ecuador', '593', '## ### ####'], ['CL', 'Chile', '56', '# #### ####'],
    ['AR', 'Argentina', '54', '9 ## ####-####'], ['BR', 'Brasil', '55', '## #####-####'], ['CR', 'Costa Rica', '506', '####-####'],
    ['GT', 'Guatemala', '502', '#### ####'], ['SV', 'El Salvador', '503', '#### ####'], ['HN', 'Honduras', '504', '####-####'],
    ['NI', 'Nicaragua', '505', '#### ####'], ['BO', 'Bolivia', '591', '########'], ['PY', 'Paraguay', '595', '### ######'],
    ['UY', 'Uruguay', '598', '## ### ###'], ['DO', 'República Dominicana', '1', '(###) ###-####'], ['PR', 'Puerto Rico', '1', '(###) ###-####'],
    ['CU', 'Cuba', '53', '# ### ####'], ['CA', 'Canadá', '1', '(###) ###-####'], ['FR', 'Francia', '33', '# ## ## ## ##'],
    ['DE', 'Alemania', '49', '#### #######'], ['IT', 'Italia', '39', '### ### ####'], ['PT', 'Portugal', '351', '### ### ###'],
    ['GB', 'Reino Unido', '44', '#### ######'], ['NL', 'Países Bajos', '31', '# ########'], ['CN', 'China', '86', '### #### ####'],
    ['IN', 'India', '91', '##### #####'], ['AE', 'Emiratos Árabes Unidos', '971', '## ### ####'], ['IL', 'Israel', '972', '##-###-####'],
    ['JP', 'Japón', '81', '##-####-####'], ['KR', 'Corea del Sur', '82', '##-####-####']
  ];
  var C = list.map(function (r) { return { iso: r[0], name: r[1], dial: r[2], mask: r[3] }; });
  C.sort(function (a, b) { return a.name.localeCompare(b.name, 'es'); });
  window.COUNTRIES = C;
  window.countryByIso = function (iso) { for (var i = 0; i < C.length; i++) if (C[i].iso === iso) return C[i]; return null; };
  window.flagSVG = function (iso) {
    var f = F[iso] || F.XX;
    return '<span class="flag" title="' + iso + '"><svg viewBox="0 0 30 20" xmlns="http://www.w3.org/2000/svg">' + f() + '</svg></span>';
  };
})();
