import { Scene } from './scene.js';
import { Sound } from './audio.js';
import { PixelChart } from './chart.js';
import { setPixelText, pixelizeHeadings } from './font.js';
import { blockEvents, shareDelta, finishedRound } from './events.js';
import {
  fmtHash, fmtDiff, fmtInt, fmtDur, fmtAgo, fmtPct, fmtBytes, fmtCoin, shortHash, maskAddress, fmtTime,
} from './format.js';
import { CREATURE, CREATURE_COLORS, TROPHY } from './sprites.js';

const $ = (s, r = document) => r.querySelector(s);
const C = { ink: '#ece8ff', ink2: '#b4acdc', ink3: '#8a83b4', mint: '#5ee6c0', gold: '#ffd166', amber: '#ffb347', red: '#ff7a7a' };

const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);

// ---------- settings (per-viewer conveniences only) ----------
const store = {
  get(k, d) { try { const v = localStorage.getItem('wb.' + k); return v == null ? d : JSON.parse(v); } catch { return d; } },
  set(k, v) { try { localStorage.setItem('wb.' + k, JSON.stringify(v)); } catch { /* private mode */ } },
};

// ---------- small pixel helpers ----------
function spriteCanvas(rows, colorOf, cssPx = 2) {
  const c = document.createElement('canvas');
  const dpr = window.devicePixelRatio || 1;
  const s = Math.max(1, Math.round(cssPx * dpr));
  const w = rows[0].length, h = rows.length;
  c.width = w * s; c.height = h * s;
  c.style.width = (w * s) / dpr + 'px'; c.style.height = (h * s) / dpr + 'px';
  const ctx = c.getContext('2d');
  rows.forEach((row, y) => [...row].forEach((ch, x) => {
    if (ch === '.') return;
    const col = colorOf(ch, x, y);
    if (!col) return;
    ctx.fillStyle = col; ctx.fillRect(x * s, y * s, s, s);
  }));
  return c;
}
const hexCss = (n) => '#' + n.toString(16).padStart(6, '0');
function creatureIcon(tier, cssPx = 1.5) {
  if (tier < 0) { const c = document.createElement('canvas'); c.width = c.height = 1; return c; }
  const base = CREATURE_COLORS[Math.min(9, tier)];
  const shade = (base >> 1) & 0x7f7f7f;
  return spriteCanvas(CREATURE, (ch) => ({ K: '#120c1c', C: hexCss(base), c: hexCss(shade), W: '#ffffff' })[ch], cssPx);
}
const RAINBOW = ['#ff5f6d', '#ffa34d', '#ffe066', '#7ee081', '#4fd1c5', '#6b8cff', '#b06bff'];
function trophyIcon(seed) {
  return spriteCanvas(TROPHY, (ch, x, y) => {
    if (ch === 'Y') return C.gold;
    const col = RAINBOW[(x + y + seed) % RAINBOW.length];
    return ch === 'g' ? col + 'aa' : col;
  }, 2);
}

function pxCanvas(text, color, cssPx) {
  const c = document.createElement('canvas');
  c.className = 'pxtext';
  setPixelText(c, text, color, cssPx);
  return c;
}

// ---------- app ----------
const scene = new Scene($('#scene'));
const sound = new Sound();
let prev = null;
let state = null;
let page = 'mine';
let lastMsg = 0;

scene.on('impact', () => sound.clink());

function narrow() { return window.matchMedia('(max-width: 640px), (max-height: 460px)').matches; }

// HUD: stats are built once, then only re-rendered when their text changes.
const hud = {};
function stat(panel, key, label, opts = {}) {
  const el = document.createElement('div');
  el.className = 'stat';
  const lab = document.createElement('div'); lab.className = 'lab';
  const val = document.createElement('div'); val.className = 'val';
  const lc = document.createElement('canvas'); lc.className = 'pxtext';
  const vc = document.createElement('canvas'); vc.className = 'pxtext';
  lab.append(lc); val.append(vc);
  el.append(lab, val);
  panel.append(el);
  hud[key] = { lc, vc, label, short: opts.short || label, color: opts.color || C.ink };
  return el;
}
function row(panel) { const r = document.createElement('div'); r.className = 'stat-row'; panel.append(r); return r; }

function buildHUD() {
  const h = $('#hud-hash'), c = $('#hud-chain'), l = $('#hud-loot');
  const r1 = row(h);
  stat(r1, 'hrNow', 'HASHRATE NOW', { color: C.mint, short: 'HASH NOW' });
  stat(r1, 'workers', 'WORKERS');
  const r2 = row(h);
  stat(r2, 'hr1h', '1H AVG');
  stat(r2, 'hr24h', '24H AVG');
  const r3 = row(c);
  stat(r3, 'height', 'BLOCK HEIGHT', { short: 'HEIGHT' });
  stat(r3, 'node', 'NODE');
  const r4 = row(c);
  stat(r4, 'netdiff', 'NET DIFFICULTY', { short: 'NET DIFF' });
  stat(r4, 'eta', 'EST. TIME TO BLOCK', { short: 'EST. BLOCK' });
  const r5 = row(l);
  stat(r5, 'bestJob', 'BEST THIS JOB', { color: C.gold, short: 'BEST JOB' });
  stat(r5, 'bestAll', 'BEST ALL-TIME', { color: C.gold, short: 'ALL-TIME' });
  const r6 = row(l);
  stat(r6, 'found', 'BLOCKS FOUND', { color: C.gold, short: 'BLOCKS' });
  stat(r6, 'creature', 'THIS JOB\u2019S BEAST', { short: 'BEAST' });
}

function setStat(key, value, color) {
  const s = hud[key];
  const nar = narrow();
  const big = nar ? 1.25 : 2;
  setPixelText(s.lc, nar ? s.short : s.label, C.ink3, nar ? 1 : 1.5);
  setPixelText(s.vc, String(value).toUpperCase(), color || s.color, big);
}

// In portrait the bottom HUD spans the full width: tell the scene so the
// floor (and the wizard) stay visible above it.
function syncInsets() {
  const portrait = window.innerHeight > window.innerWidth;
  const bottom = document.querySelector('.hud-bottom');
  scene.setBottomInset(portrait && narrow() ? bottom.offsetHeight + 12 : 0);
}

function renderHUD(st) {
  const p = st.pool, d = st.derived, n = st.node, t = st.template;
  const online = st.workers.filter((w) => w.connections > 0).length;
  setStat('hrNow', fmtHash(p.hashrate_1m));
  setStat('workers', `${online}/${st.workers.length}`, online ? C.ink : C.ink3);
  setStat('hr1h', fmtHash(p.hashrate_1h));
  const partial = d.hashrate_24h_span_s > 0 && d.hashrate_24h_span_s < 86000;
  setStat('hr24h', (partial ? '~' : '') + fmtHash(d.hashrate_24h));
  setStat('height', n.height ? fmtInt(n.height) : '—');
  let nodeTxt = 'OFFLINE', nodeCol = C.red;
  if (n.connected && n.synced) { nodeTxt = '● SYNCED'; nodeCol = C.mint; }
  else if (n.connected) { nodeTxt = 'SYNCING ' + (st.node_detail.sync_pct ? st.node_detail.sync_pct.toFixed(1) + '%' : ''); nodeCol = C.amber; }
  setStat('node', nodeTxt, nodeCol);
  setStat('netdiff', fmtDiff(t.network_difficulty));
  setStat('eta', d.expected_time_to_block_s == null ? 'NO HASHRATE' : fmtDur(d.expected_time_to_block_s));
  setStat('bestJob', fmtDiff(d.best_this_job));
  setStat('bestAll', fmtDiff(p.best_share_difficulty));
  setStat('found', String(p.blocks_found));
  const cur = st.rounds.find((r) => r.current);
  setStat('creature', cur && cur.tier >= 0 ? cur.creature : '—', C.ink2);

  // Pending: neutral notice, never a celebration.
  const pill = $('#pending-pill');
  const pending = p.blocks_pending > 0;
  pill.hidden = !pending;
  if (pending) setPixelText($('canvas', pill), `BLOCK SUBMITTED · AWAITING NODE CONFIRMATION (${p.blocks_pending})`, C.amber, narrow() ? 1 : 1.5);

  // Trophy wall: confirmed blocks only.
  const wall = $('#hud-trophies');
  const confirmed = st.blocks.filter((b) => b.status === 'accepted' && b.chain_status !== 'orphaned');
  const sig = confirmed.map((b) => b.hash + b.chain_status).join(',') + narrow();
  if (wall._sig !== sig) {
    wall._sig = sig;
    wall.textContent = '';
    wall.append(pxCanvas('TROPHY WALL', C.ink3, narrow() ? 1 : 1.5));
    if (!confirmed.length) {
      const e = document.createElement('div'); e.className = 'empty';
      e.append(pxCanvas('NO BLOCKS YET. KEEP DIGGING.', C.ink3, narrow() ? 1 : 1.5));
      wall.append(e);
    }
    const max = narrow() ? 6 : 12;
    confirmed.slice(0, max).forEach((b, i) => {
      const a = document.createElement('a');
      a.className = 'trophy'; a.href = '#/ledger'; a.title = `Block ${b.height}`;
      a.append(trophyIcon(i), pxCanvas('#' + b.height, b.chain_status === 'matured' ? C.gold : C.ink2, 1.5));
      wall.append(a);
    });
    if (confirmed.length > max) wall.append(pxCanvas(`+${confirmed.length - max}`, C.ink3, 1.5));
  }
  syncInsets();
}

// ---------- ledger ----------
let chart;
let range = store.get('range', '1h');
let revealed = false;

function kv(el, pairs) {
  el.innerHTML = pairs.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${v}</dd>`).join('');
}

function renderLedger(st) {
  const n = st.node_detail, ns = st.node, p = st.pool, d = st.derived, coin = st.coin;
  const sync = Math.max(0, Math.min(100, n.sync_pct || 0));
  kv($('#node-kv'), [
    ['Software', esc(n.subversion || ns.subversion || '—')],
    ['Network', esc(`${coin.name} · ${st.chain || '—'}`)],
    ['Sync', `${esc(sync.toFixed(2))}%<span class="bar"><i></i></span>`],
    ['Height', `${esc(fmtInt(n.blocks))} / ${esc(fmtInt(n.headers))} headers`],
    ['Peers', esc(n.peers ?? '—')],
    ['Network hashrate', esc(fmtHash(n.network_hashps))],
    ['Network difficulty', esc(fmtDiff(n.difficulty || st.template.network_difficulty))],
    ['Mempool', `${esc(fmtInt(n.mempool_txs))} tx · ${esc(fmtBytes(n.mempool_bytes))}`],
    ['Node uptime', esc(fmtDur(n.uptime_s))],
    ['Disk usage', esc(fmtBytes(n.disk_bytes)) + (n.pruned ? ' (pruned)' : '')],
    ['ZMQ', ns.zmq_enabled ? (ns.zmq_connected ? '<span class="st-online">connected</span>' : '<span class="st-pending">reconnecting (polling)</span>') : 'disabled (polling)'],
    ['Engine uptime', esc(fmtDur(st.uptime_s)) + ' · v' + esc(st.version)],
  ]);
  // Set via CSSOM (inline style attributes are blocked by the CSP).
  const bar = $('#node-kv .bar > i'); if (bar) bar.style.width = sync + '%';
  if (n.error) $('#node-kv').insertAdjacentHTML('beforeend', `<dt>Error</dt><dd class="st-rejected">${esc(n.error)}</dd>`);

  const rej = Object.entries(p.rejects || {}).filter(([, v]) => v > 0).map(([k, v]) => `${esc(k)} ${esc(fmtInt(v))}`).join(', ');
  kv($('#shares-kv'), [
    ['Accepted', esc(fmtInt(p.shares_accepted))],
    ['Rejected', esc(fmtInt(p.shares_rejected)) + (rej ? `<br><small>${rej}</small>` : '')],
    ['Stale', esc(fmtInt(d.stale_shares))],
    ['Best share', esc(fmtDiff(p.best_share_difficulty)) + (p.best_share_worker ? ' · ' + esc(p.best_share_worker) : '')],
  ]);
  kv($('#odds-kv'), [
    ['Expected time', esc(d.expected_time_to_block_s == null ? '∞' : fmtDur(d.expected_time_to_block_s))],
    ['Block / day', esc(fmtPct(d.odds_day * 100))],
    ['Block / week', esc(fmtPct(d.odds_week * 100))],
    ['Block / year', esc(fmtPct(d.odds_year * 100))],
  ]);
  const host = location.hostname || 'this-host';
  const addr = st.stratum.payout_address;
  const addrHtml = addr
    ? `<button class="reveal" id="reveal" aria-label="Reveal payout address">${esc(revealed ? addr : maskAddress(addr))}</button>`
    : '<span>per miner (username is the payout address)</span>';
  kv($('#conn-kv'), [
    ['Stratum URL', `<span class="copyable">stratum+tcp://${esc(host)}:${esc(st.stratum.port)}</span>`],
    ['Username', esc(st.stratum.username_format)],
    ['Password', 'x (or d=&lt;start difficulty&gt;)'],
    ['Payout', addrHtml],
  ]);
  const btn = $('#reveal');
  if (btn) btn.onclick = () => { revealed = !revealed; btn.textContent = revealed ? addr : maskAddress(addr); };

  // Workers.
  const now = st.now;
  const wrows = st.workers.map((w) => {
    const on = w.connections > 0;
    return `<tr><td>${esc(w.name)}</td><td><span class="badge ${on ? 'st-online' : 'st-offline'}">${on ? 'online' : 'offline'}</span></td>
      <td class="num">${esc(fmtHash(w.hashrate_5m))}</td><td class="num">${esc(fmtInt(w.shares_accepted))} / ${esc(fmtInt(w.shares_rejected))}</td>
      <td class="num">${esc(fmtDiff(w.best_share_difficulty))}</td><td class="num">${esc(fmtDiff(w.difficulty))}</td>
      <td>${esc(fmtAgo(w.last_share_at, now))}</td></tr>`;
  }).join('');
  $('#workers tbody').innerHTML = wrows || '<tr><td colspan="7" class="empty">No miners yet. Point a miner at the Stratum URL above.</td></tr>';

  // Blocks.
  const maturity = coin.coinbase_maturity;
  const statusText = { matured: 'matured', confirming: 'confirming', pending: 'pending', orphaned: 'orphaned', stale: 'stale', rejected: 'rejected' };
  const brows = st.blocks.map((b) => {
    const confs = b.confirmations < 0 ? '—' : `${Math.min(b.confirmations, maturity)}/${maturity}`;
    const hash = b.explorer_url
      ? `<a href="${esc(b.explorer_url)}" target="_blank" rel="noopener noreferrer">${esc(shortHash(b.hash))}</a>`
      : esc(shortHash(b.hash));
    return `<tr><td class="num">${esc(fmtInt(b.height))}</td><td title="${esc(b.hash)}">${hash}</td>
      <td class="num">${esc(fmtCoin(b.reward_sats, coin.ticker))}</td><td>${esc(b.worker)}</td><td>${esc(fmtTime(b.time))}</td>
      <td class="num">${esc(confs)}</td><td><span class="badge st-${esc(b.chain_status)}">${esc(statusText[b.chain_status] || b.chain_status)}</span></td></tr>`;
  }).join('');
  $('#blocks tbody').innerHTML = brows || '<tr><td colspan="7" class="empty">No blocks found yet.</td></tr>';

  // Creature log.
  const tbody = $('#rounds tbody');
  tbody.textContent = '';
  if (!st.rounds.length) tbody.innerHTML = '<tr><td colspan="7" class="empty">No jobs yet.</td></tr>';
  for (const r of st.rounds) {
    const tr = document.createElement('tr');
    const dur = ((r.current ? now : Date.parse(r.end)) - Date.parse(r.start)) / 1000;
    tr.innerHTML = `<td class="num">${esc(fmtInt(r.height))}${r.current ? ' <span class="st-pending">(now)</span>' : ''}</td>
      <td><span class="creature-cell"></span></td><td class="num">${esc(fmtDiff(r.best_difficulty))}</td>
      <td class="num">${esc(fmtPct(r.pct_of_network))}</td><td class="num">${r.luck_percentile == null ? '—' : esc(fmtPct(r.luck_percentile, 2))}</td>
      <td class="num">${esc(fmtInt(r.shares))}</td><td>${esc(fmtDur(dur))}</td>`;
    const cell = $('.creature-cell', tr);
    cell.append(creatureIcon(r.tier));
    cell.append(document.createTextNode(r.creature));
    tbody.append(tr);
  }
}

async function loadHistory() {
  if (!chart) return;
  try {
    const res = await fetch('api/history?range=' + encodeURIComponent(range), { cache: 'no-store' });
    const j = await res.json();
    chart.set(j.points, 'COLLECTING DATA… (SAMPLED EVERY 30S)');
  } catch { chart.set([], 'HISTORY UNAVAILABLE'); }
}

// ---------- events from the engine ----------
function onState(next) {
  lastMsg = Date.now();
  setLink('live');
  const ev = blockEvents(prev, next);
  const swings = shareDelta(prev, next);
  if (swings) scene.addSwings(Math.min(3, swings));
  scene.setPending(ev.pending);
  if (ev.newPending.length) sound.pending();
  if (ev.celebrate.length) {
    scene.celebrate();
    sound.fanfare();
    showBanner(ev.celebrate[0]);
  }
  const done = finishedRound(prev, next);
  if (done && done.tier >= 0) {
    scene.showCreature(done.tier, done.creature);
    sound.creature(done.tier);
  }
  scene.setHashrate(next.pool.hashrate_5m || next.pool.hashrate_1m);
  sound.setIntensity(scene.intensity);
  scene.setSleeping(!next.node.connected || !next.node.synced);
  state = next;
  prev = next;
  renderHUD(next);
  if (page === 'ledger') renderLedger(next);
}

let bannerTimer;
function showBanner(b) {
  const el = $('#banner');
  const [c1, c2] = el.querySelectorAll('canvas');
  setPixelText(c1, 'BLOCK FOUND!', C.gold, narrow() ? 3 : 4);
  setPixelText(c2, `#${b.height} · ${fmtCoin(b.reward_sats, state?.coin?.ticker || '').toUpperCase()} · CONFIRMED`, C.ink, narrow() ? 1 : 1.5);
  el.hidden = false;
  clearTimeout(bannerTimer);
  bannerTimer = setTimeout(() => { el.hidden = true; }, 9000);
}

function setLink(s) {
  const el = $('#link');
  el.className = 'link ' + s;
  el.title = s === 'live' ? 'Live link to the engine' : 'Connection to the engine lost; retrying';
}

function connect() {
  const es = new EventSource('api/events');
  es.addEventListener('state', (e) => {
    try { onState(JSON.parse(e.data)); } catch (err) { console.error(err); }
  });
  es.onerror = () => setLink('lost');
  setInterval(() => { if (Date.now() - lastMsg > 6000) setLink('lost'); }, 2000);
}

// ---------- routing & controls ----------
function route() {
  page = location.hash.startsWith('#/ledger') ? 'ledger' : 'mine';
  $('#page-mine').hidden = page !== 'mine';
  $('#page-ledger').hidden = page !== 'ledger';
  document.querySelectorAll('.tab').forEach((a) => {
    if (a.dataset.page === page) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
  });
  if (page === 'mine') { scene.resize(); scene.start(); } else { scene.stop(); }
  if (page === 'ledger') {
    if (!chart) chart = new PixelChart($('#chart'));
    loadHistory();
    if (state) renderLedger(state);
  }
}

function setupControls() {
  const crt = $('#btn-crt');
  const applyCrt = (on) => { document.body.classList.toggle('crt', on); crt.setAttribute('aria-pressed', String(on)); };
  applyCrt(store.get('crt', false));
  crt.onclick = () => { const on = !document.body.classList.contains('crt'); applyCrt(on); store.set('crt', on); };

  const snd = $('#btn-sound');
  snd.onclick = () => {
    if (sound.enabled) sound.disable(); else sound.enable();
    snd.setAttribute('aria-pressed', String(sound.enabled));
    store.set('sound', sound.enabled);
  };
  // Browsers (iOS in particular) only allow audio after a gesture: if the
  // viewer had sound on, re-enable it on their first tap.
  if (store.get('sound', false)) {
    const once = () => { sound.enable(); snd.setAttribute('aria-pressed', 'true'); window.removeEventListener('pointerdown', once); };
    window.addEventListener('pointerdown', once);
  }

  $('#range-tabs').addEventListener('click', (e) => {
    const b = e.target.closest('button[data-range]');
    if (!b) return;
    range = b.dataset.range;
    store.set('range', range);
    document.querySelectorAll('#range-tabs button').forEach((x) => x.setAttribute('aria-selected', String(x === b)));
    loadHistory();
  });
  document.querySelectorAll('#range-tabs button').forEach((x) => x.setAttribute('aria-selected', String(x.dataset.range === range)));
  setInterval(() => { if (page === 'ledger') loadHistory(); }, 30000);
  window.addEventListener('hashchange', route);
  window.addEventListener('resize', () => { if (state) renderHUD(state); });
}

function boot() {
  setPixelText($('#brand'), 'WIZARD-BLOCKS', C.gold, narrow() ? 1.5 : 2);
  pixelizeHeadings();
  buildHUD();
  setupControls();
  route();
  connect();
}

boot();

// Exposed for automated UI checks.
window.__wb = { scene, blockEvents, get state() { return state; } };
