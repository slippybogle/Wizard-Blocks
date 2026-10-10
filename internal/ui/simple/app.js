// Wizard-Blocks simple UI: one stats page over /api/state, refreshed every
// 5 seconds, with a faint binary rain behind it. No dependencies.
'use strict';

const $ = (id) => document.getElementById(id);
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);

// ---------- formatting ----------
function fmtHash(h) {
  if (!(h > 0)) return '0 H/s';
  const u = ['H/s', 'kH/s', 'MH/s', 'GH/s', 'TH/s', 'PH/s', 'EH/s'];
  let i = 0;
  while (h >= 1000 && i < u.length - 1) { h /= 1000; i++; }
  return `${h >= 100 ? h.toFixed(1) : h.toFixed(2)} ${u[i]}`;
}
function fmtDiff(d) {
  if (!(d > 0)) return '—';
  if (d < 1) return d.toExponential(2); // regtest / tiny test difficulties
  const u = ['', 'K', 'M', 'G', 'T', 'P', 'E'];
  let i = 0;
  while (d >= 1000 && i < u.length - 1) { d /= 1000; i++; }
  return d.toFixed(d >= 100 ? 1 : 2) + u[i];
}
const fmtInt = (n) => (n ?? 0).toLocaleString('en-US');
function ago(iso) {
  const t = Date.parse(iso || '');
  if (!t) return '—';
  const s = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (s < 120) return `${s} s ago`;
  if (s < 7200) return `${Math.round(s / 60)} min ago`;
  if (s < 172800) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} d ago`;
}
function set(id, text, bad = false) { const el = $(id); el.textContent = text; el.classList.toggle('bad', bad); }

// ---------- live state ----------
async function refresh() {
  try {
    const res = await fetch('api/state', { cache: 'no-store' });
    if (!res.ok) throw new Error('HTTP ' + res.status);
    render(await res.json());
    set('status', 'live · ' + new Date().toLocaleTimeString());
  } catch (e) {
    set('status', 'engine unreachable: ' + e.message, true);
  }
}

function fmtSecs(v) {
  if (!(v > 0)) return '—';
  if (v < 120) return `${v.toFixed(v < 10 ? 1 : 0)} s`;
  if (v < 7200) return `${(v / 60).toFixed(0)} min`;
  if (v < 172800) return `${(v / 3600).toFixed(1)} h`;
  if (v < 63072000) return `${(v / 86400).toFixed(1)} d`;
  return `${(v / 31536000).toFixed(1)} y`;
}
const pct = (v) => (v == null ? '—' : v < 0.01 ? (v * 100).toExponential(1) + ' %' : (v * 100).toFixed(2) + ' %');

// Banner shown while an aux chain (Dogecoin) is not being merge-mined.
function bannerText(name, ticker, reason) {
  switch (reason) {
    case 'no address': return `Mining ${name} only. Add a ${ticker} payout address in Settings to also mine ${ticker === 'DOGE' ? 'Dogecoin' : ticker}.`;
    case 'node down': return `Mining ${name} only: the ${ticker === 'DOGE' ? 'Dogecoin' : ticker} node is down. ${ticker === 'DOGE' ? 'Dogecoin' : ticker} mining resumes on its own.`;
    case 'node syncing': return `Mining ${name} only: the ${ticker === 'DOGE' ? 'Dogecoin' : ticker} node is syncing. ${ticker === 'DOGE' ? 'Dogecoin' : ticker} mining resumes on its own.`;
    default: return `Mining ${name} only: ${ticker} merged mining is unavailable (${reason || 'unknown'}).`;
  }
}

function renderChain(prefix, title, v, isAux) {
  set(prefix + '-status', isAux ? (v.merged ? 'merged' : 'not merged: ' + (v.reason || '—')) : (!v.connected ? 'no node connection' : v.synced ? 'synced' : 'syncing'), isAux ? !v.merged : !v.connected || !v.synced);
  $(prefix === 'cp' ? 'cp-title' : 'ca-title').textContent = title;
  set(prefix + '-height', v.height ? fmtInt(v.height) : '—');
  set(prefix + '-diff', fmtDiff(v.network_difficulty));
  set(prefix + '-effort', v.effort_pct == null ? '—' : v.effort_pct.toFixed(4) + ' %');
  set(prefix + '-exp', fmtSecs(v.expected_block_s));
  set(prefix + '-odds', `${pct(v.odds_day)} / ${pct(v.odds_week)}`);
  set(prefix + '-blocks', `${fmtInt(v.blocks_found)}${v.blocks_pending ? ` (+${v.blocks_pending} pending)` : ''}`);
}

let lastState = null;
function render(st) {
  lastState = st;
  const n = st.node || {}, p = st.pool || {}, t = st.template || {}, d = st.derived || {};
  const coin = st.coin || {}, ticker = coin.ticker || '';
  $('title').textContent = 'WIZARD-BLOCKS' + (ticker ? '-' + ticker : '');
  document.title = 'Wizard-Blocks' + (ticker ? '-' + ticker : '');
  const hint = (coin.address_hint || {})[st.chain] || '';
  $('s-addr').placeholder = hint;
  $('s-addr-label').textContent = `${ticker || 'Payout'} payout address${hint ? ' (' + hint + ')' : ''}`;

  const chains = st.chains || null;
  const auxName = chains ? Object.keys(chains).find((k) => k !== coin.coin) : null;
  $('chains').hidden = !auxName;
  $('s-doge-wrap').hidden = !auxName;
  document.querySelectorAll('.chaincol').forEach((el) => { el.hidden = !auxName; });
  if (auxName) {
    const aux = chains[auxName], auxCoin = (st.aux_coins || {})[auxName] || { ticker: auxName.toUpperCase() };
    renderChain('cp', ticker, chains[coin.coin] || {}, false);
    renderChain('ca', `${auxCoin.ticker} (MERGED)`, aux, true);
    $('banner').hidden = !!aux.merged;
    if (!aux.merged) $('banner').textContent = bannerText(coin.name || ticker, auxCoin.ticker, aux.reason);
    if (document.activeElement !== $('s-doge') && !$('s-doge').dataset.dirty) $('s-doge').value = aux.payout_address || '';
  } else {
    $('banner').hidden = true;
  }
  set('n-synced', !n.connected ? 'no node connection' : n.synced ? 'yes' : `syncing ${fmtInt(n.height)} / ${fmtInt(n.headers)}`, !n.connected || !n.synced);
  set('n-height', fmtInt(n.height));
  set('n-zmq', !n.zmq_enabled ? 'off (polling)' : n.zmq_connected ? 'connected' : 'not connected', n.zmq_enabled && !n.zmq_connected);
  set('n-chain', n.chain || st.chain || '—');

  set('p-hr1', fmtHash(p.hashrate_60s));
  set('p-hr5', fmtHash(p.hashrate_5m));
  set('p-hr60', fmtHash(p.hashrate_1h));
  set('p-workers', `${p.workers_online ?? 0} online / ${(st.workers || []).length}`);
  set('p-acc', fmtInt(p.shares_accepted));
  const rej = Object.entries(p.rejects || {}).filter(([, v]) => v > 0).map(([k, v]) => `${k} ${v}`).join(', ');
  set('p-rej', fmtInt(p.shares_rejected) + (rej ? ` (${rej})` : ''), p.shares_rejected > 0);
  set('p-bad', fmtInt(p.bad_messages));
  set('p-best', p.best_share_difficulty ? `${fmtDiff(p.best_share_difficulty)}${p.best_share_worker ? ' · ' + p.best_share_worker : ''}` : '—');
  set('p-netdiff', fmtDiff(t.network_difficulty));
  set('p-effort', d.luck_since_last_block_pct == null ? '—' : d.luck_since_last_block_pct.toFixed(4) + ' %');
  set('p-blocks', `${fmtInt(p.blocks_found)}${p.blocks_pending ? ` (+${p.blocks_pending} pending)` : ''}`);

  const sx = st.stratum || {};
  const host = location.hostname || 'umbrel.local';
  $('s-stratum').textContent = `Stratum: stratum+tcp://${host}:${sx.port || '—'} · user: any name · password: x`;
  if (!sx.payout_set) set('s-msg', 'No payout address yet: set it below. Miners get no work until then.', true);
  else if ($('s-msg').textContent.startsWith('No payout')) set('s-msg', '');
  if (document.activeElement !== $('s-addr') && !$('s-addr').dataset.dirty) $('s-addr').value = sx.payout_address || '';

  const workers = (st.workers || []).slice().sort((a, b) => (b.hashrate_60s || 0) - (a.hashrate_60s || 0));
  $('workers').innerHTML = workers.length ? workers.map((w) => `<tr>
    <td>${esc(w.name)}${w.connections > 0 ? '' : ' <span class="dim">(offline)</span>'}</td>
    <td class="num">${esc(fmtHash(w.hashrate_60s))}</td>
    <td class="num">${esc(fmtDiff(w.difficulty))}</td>
    <td class="num">${esc(fmtInt(w.shares_accepted))} / ${esc(fmtInt(w.shares_rejected))}</td>
    <td class="num">${esc(fmtDiff(w.best_share_difficulty))}</td>
    <td class="num">${esc(ago(w.last_share_at))}</td></tr>`).join('') : '<tr><td colspan="6" class="dim">No miners yet.</td></tr>';

  // Real blocks only (newest first): candidates that never made the chain
  // (stale, rejected) are left out.
  const real = (b) => b.chain_status !== 'stale' && b.status !== 'stale' && b.status !== 'rejected';
  let blocks = (st.blocks || []).filter(real).map((b) => ({ ...b, chainTicker: ticker }));
  if (auxName) {
    const auxCoin = (st.aux_coins || {})[auxName] || {};
    const tmpl = (auxCoin.explorer || {})[st.chain];
    blocks = blocks.concat((st.aux_blocks || []).filter(real).map((b) => ({
      ...b, chainTicker: auxCoin.ticker || auxName.toUpperCase(),
      explorer_url: tmpl ? tmpl.replace('{hash}', b.hash) : '', confirmations: null, chain_status: b.status,
    })));
    blocks.sort((a, b) => Date.parse(b.time) - Date.parse(a.time));
  }
  blocks = blocks.slice(0, 50);
  const cols = auxName ? 7 : 6;
  $('blocks').innerHTML = blocks.length ? blocks.map((b) => {
    const hash = esc(b.hash || ''), short = hash.slice(0, 12) + '…' + hash.slice(-8);
    const link = b.explorer_url ? `<a href="${esc(b.explorer_url)}" target="_blank" rel="noopener noreferrer">${short}</a>` : short;
    return `<tr>${auxName ? `<td>${esc(b.chainTicker)}</td>` : ''}<td class="num">${esc(fmtInt(b.height))}</td><td>${link}</td><td>${esc(b.worker)}</td>
      <td>${esc(new Date(b.time).toLocaleString())}</td><td class="num">${b.confirmations == null ? '—' : esc(fmtInt(b.confirmations))}</td><td>${esc(b.chain_status || b.status)}</td></tr>`;
  }).join('') : `<tr><td colspan="${cols}" class="dim">No blocks found yet.</td></tr>`;
  updateCalc();
}

// ---------- settings: payout address and optional password ----------
async function api(path, method, body) {
  const res = await fetch(path, {
    method, headers: { 'X-WB-Admin': '1', ...(body ? { 'Content-Type': 'application/json' } : {}) }, // X-WB-Admin: CSRF guard
    body: body ? JSON.stringify(body) : undefined, credentials: 'same-origin',
  });
  const j = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(j.error || 'HTTP ' + res.status);
  return j;
}
function msg(text, bad = false) { set('s-msg', text, bad); }

async function syncSession() {
  try {
    const s = await api('api/admin/session', 'GET');
    const locked = !s.enabled || (s.password_required && !s.authed);
    $('s-login').hidden = !(s.enabled && s.password_required && !s.authed);
    $('s-body').hidden = locked;
    $('diff').hidden = locked;
    if (!locked) loadDiff();
    $('s-rmpw').hidden = !s.password_set || s.env_password;
    $('s-setpw').textContent = s.password_set ? 'Change' : 'Set';
    if (!s.enabled) msg('Settings are disabled on this engine.', true);
  } catch (e) { msg(e.message, true); }
}

$('s-addr').addEventListener('input', () => { $('s-addr').dataset.dirty = '1'; });
$('s-save').addEventListener('click', async () => {
  msg('Checking with your node…');
  try {
    const r = await api('api/admin/payout', 'PUT', { address: $('s-addr').value.trim() });
    delete $('s-addr').dataset.dirty;
    msg('Saved: ' + ((r.payout && r.payout.address) || 'ok'));
    refresh();
  } catch (e) { msg(e.message, true); }
});
$('s-loginbtn').addEventListener('click', async () => {
  try { await api('api/admin/login', 'POST', { password: $('s-loginpw').value }); $('s-loginpw').value = ''; msg('Unlocked.'); syncSession(); } catch (e) { msg(e.message, true); }
});
$('s-setpw').addEventListener('click', async () => {
  try { await api('api/admin/password', 'POST', { password: $('s-pw').value }); $('s-pw').value = ''; msg('Settings password set.'); syncSession(); } catch (e) { msg(e.message, true); }
});
$('s-rmpw').addEventListener('click', async () => {
  try { await api('api/admin/password', 'DELETE'); msg('Settings password removed.'); syncSession(); } catch (e) { msg(e.message, true); }
});

$('s-doge').addEventListener('input', () => { $('s-doge').dataset.dirty = '1'; });
$('s-doge-save').addEventListener('click', async () => {
  msg('Checking with your Dogecoin node…');
  try {
    const r = await api('api/admin/doge-payout', 'PUT', { address: $('s-doge').value.trim() });
    delete $('s-doge').dataset.dirty;
    msg(r.doge_payout_address ? 'DOGE payout saved: ' + r.doge_payout_address : 'DOGE payout cleared: mining Litecoin only.');
    refresh();
  } catch (e) { msg(e.message, true); }
});

// ---------- difficulty settings ----------
function dmsg(text, bad = false) { set('d-msg', text, bad); }
function fillDiff(d) {
  $('d-mode').value = d.fixed_diff > 0 ? 'fixed' : 'vardiff';
  $('d-start').value = d.start_diff || '';
  $('d-min').value = d.vardiff_min;
  $('d-max').value = d.vardiff_max;
  $('d-target').value = d.vardiff_target_seconds;
  $('d-fixed').value = d.fixed_diff || '';
  $('d-over').value = Object.entries(d.worker_overrides || {}).map(([k, v]) => `${k} = ${v}`).join('\n');
  $('d-pw').checked = !d.ignore_password_diff;
  $('d-sg').checked = !d.ignore_suggest_difficulty;
  updateCalc();
}
async function loadDiff() {
  try { const r = await api('api/admin/settings', 'GET'); fillDiff(r.settings); } catch (e) { dmsg(e.message, true); }
}
function readDiff() {
  const num = (id) => { const v = $(id).value.trim(); return v === '' ? 0 : Number(v); };
  const over = {};
  for (const line of $('d-over').value.split('\n')) {
    if (!line.trim()) continue;
    const i = line.lastIndexOf('=');
    if (i < 1) throw new Error(`override line "${line.trim()}" must look like name = difficulty`);
    over[line.slice(0, i).trim()] = Number(line.slice(i + 1).trim());
  }
  const fixed = $('d-mode').value === 'fixed' ? num('d-fixed') : 0;
  if ($('d-mode').value === 'fixed' && !(fixed > 0)) throw new Error('fixed mode needs a fixed difficulty');
  return {
    vardiff_min: num('d-min'), vardiff_max: num('d-max'), vardiff_target_seconds: num('d-target'),
    fixed_diff: fixed, worker_overrides: over, start_diff: num('d-start'),
    ignore_password_diff: !$('d-pw').checked, ignore_suggest_difficulty: !$('d-sg').checked,
  };
}
$('d-save').addEventListener('click', async () => {
  try { const r = await api('api/admin/settings', 'PUT', readDiff()); fillDiff(r.settings); dmsg('Saved. Connected miners get the new difficulty now.'); } catch (e) { dmsg(e.message, true); }
});
$('d-reset').addEventListener('click', async () => {
  try { await api('api/admin/settings/reset', 'POST'); await loadDiff(); dmsg('Back to the defaults.'); } catch (e) { dmsg(e.message, true); }
});
// Seconds per share at the start difficulty for a typed-in hashrate.
function updateCalc() {
  const start = Number($('d-start').value) || ((lastState && lastState.stratum && lastState.stratum.difficulty) || {}).start_diff || 0;
  const gh = Number($('d-hr').value);
  const diff1 = (lastState && lastState.coin && lastState.coin.coin === 'ltc') ? 65536 : 4294967296;
  set('d-calc', start > 0 && gh > 0 ? `one share every ${fmtSecs(start * diff1 / (gh * 1e9))} at difficulty ${fmtDiff(start)}` : '—');
}
['d-start', 'd-hr'].forEach((id) => $(id).addEventListener('input', updateCalc));

// ---------- binary rain: evenly spaced columns, alternating up/down ----------
const rain = (() => {
  const cv = $('rain'), ctx = cv.getContext('2d');
  const STEP = 1000 / 30; // ~30 fps cap
  const FONT = 14, LINE = 16, GAP = 22;
  let cols = [], raf = 0, last = 0, w = 0, h = 0;
  const still = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  function resize() {
    const dpr = Math.min(2, window.devicePixelRatio || 1);
    w = window.innerWidth; h = window.innerHeight;
    cv.width = w * dpr; cv.height = h * dpr;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    const n = Math.ceil(w / GAP), rows = Math.ceil(h / LINE) + 2;
    cols = Array.from({ length: n }, (_, i) => ({
      x: Math.round(i * GAP + GAP / 2),
      dir: i % 2 === 0 ? -1 : 1, // column 1 up, column 2 down, …
      speed: 18 + ((i * 37) % 23), // px per second
      off: Math.random() * LINE,
      bits: Array.from({ length: rows }, () => (Math.random() < 0.5 ? '0' : '1')),
    }));
    draw(0);
  }

  function draw(dt) {
    ctx.clearRect(0, 0, w, h);
    ctx.fillStyle = 'rgba(190, 190, 190, 0.10)';
    ctx.font = `${FONT}px ui-monospace, Menlo, Consolas, monospace`;
    ctx.textAlign = 'center';
    for (const c of cols) {
      c.off += c.speed * dt;
      // Each time a full line scrolls by, shift the column and add a new bit.
      while (c.off >= LINE) {
        c.off -= LINE;
        if (c.dir < 0) { c.bits.shift(); c.bits.push(Math.random() < 0.5 ? '0' : '1'); }
        else { c.bits.pop(); c.bits.unshift(Math.random() < 0.5 ? '0' : '1'); }
      }
      const shift = c.dir < 0 ? -c.off : c.off - LINE;
      for (let r = 0; r < c.bits.length; r++) ctx.fillText(c.bits[r], c.x, r * LINE + shift + FONT);
    }
  }

  function frame(ts) {
    raf = requestAnimationFrame(frame);
    if (ts - last < STEP) return;
    const dt = last ? Math.min(0.1, (ts - last) / 1000) : 0;
    last = ts;
    draw(dt);
  }
  function start() { if (!raf && !still) { last = 0; raf = requestAnimationFrame(frame); } }
  function stop() { cancelAnimationFrame(raf); raf = 0; }

  window.addEventListener('resize', resize);
  document.addEventListener('visibilitychange', () => (document.hidden ? stop() : start()));
  resize();
  if (!document.hidden) start();
  return { start, stop };
})();

// ---------- go ----------
refresh();
syncSession();
setInterval(() => { if (!document.hidden) refresh(); }, 5000);
document.addEventListener('visibilitychange', () => { if (!document.hidden) refresh(); });
