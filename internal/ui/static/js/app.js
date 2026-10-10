import { Scene } from './scene.js';
import { Sound } from './audio.js';
import { PixelChart } from './chart.js';
import { SettingsPanel } from './settings.js';
import { setPixelText, pixelizeHeadings } from './font.js';
import { blockEvents, shareDelta } from './events.js';
import {
  fmtHash, fmtDiff, fmtInt, fmtDur, fmtAgo, fmtPct, fmtBytes, fmtCoin, shortHash, maskAddress, fmtTime,
} from './format.js';
import { creatureColor, TROPHY, LEGENDARY, BLOCK_TIER } from './sprites.js';
import { creatureSprite, creatureBody, CREATURE_KEYS } from './creatures.js';

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
function creatureIcon(tier, cssPx = 1.5, variant = 0, species = 0) {
  if (tier < 0) { const c = document.createElement('canvas'); c.width = c.height = 1; return c; }
  if (tier >= BLOCK_TIER) return trophyIcon(0); // the job found a block
  const base = creatureBody(tier, species, creatureColor(tier, variant));
  const shade = (base >> 1) & 0x7f7f7f;
  const rows = creatureSprite(tier, species).rows;
  const big = rows[0].length > 14;
  return spriteCanvas(rows, (ch, x, y) => (ch === 'C' ? hexCss(base) : ch === 'c' ? hexCss(shade) : ch === 'R' ? RAINBOW[(x + y) % RAINBOW.length] : hexCss(CREATURE_KEYS[ch])), big ? Math.max(1, cssPx - 0.5) : cssPx);
}
const RARITY_COLOR = { Common: '#b4acdc', Uncommon: '#7ee081', Rare: '#6b8cff', Epic: '#c77dff', Legendary: '#ffd166' };
const RAINBOW = ['#ff5f6d', '#ffa34d', '#ffe066', '#7ee081', '#4fd1c5', '#6b8cff', '#b06bff'];
function trophyIcon(seed) {
  return spriteCanvas(TROPHY, (ch, x, y) => {
    if (ch === 'Y') return C.gold;
    const col = RAINBOW[(x + y + seed) % RAINBOW.length];
    return ch === 'g' ? col + 'aa' : col;
  }, 2);
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

function buildHUD() {
  // Bottom bar: loot and luck on the left; hashrate, workers and chain stats
  // in one block on the right (4 x 2).
  const c = $('#hud-stats'), l = $('#hud-loot');
  stat(c, 'hrNow', 'HASHRATE NOW', { color: C.mint, short: 'HASH NOW' });
  stat(c, 'workers', 'WORKERS');
  stat(c, 'height', 'BLOCK HEIGHT', { short: 'HEIGHT' });
  stat(c, 'node', 'NODE');
  stat(c, 'hr1h', '1H AVG');
  stat(c, 'hr24h', '24H AVG');
  stat(c, 'netdiff', 'NET DIFFICULTY', { short: 'NET DIFF' });
  stat(c, 'eta', 'EST. TIME TO BLOCK', { short: 'EST. BLOCK' });
  stat(l, 'bestJob', 'BEST THIS JOB', { color: C.gold, short: 'BEST JOB' });
  stat(l, 'bestAll', 'BEST ALL-TIME', { color: C.gold, short: 'ALL-TIME' });
  stat(l, 'found', 'BLOCKS FOUND', { color: C.gold, short: 'BLOCKS' });
  stat(l, 'creature', 'THIS JOB\u2019S BEAST', { short: 'BEAST' });
  // Effort since our last found block: work done as a % of network
  // difficulty (100 % = a block is statistically due; resets only on a find).
  const luck = document.createElement('div');
  luck.className = 'stat luck';
  luck.innerHTML = '<div class="lab"><canvas class="pxtext"></canvas></div><div class="bar-row"><canvas class="luck-bar"></canvas><canvas class="pxtext"></canvas></div>';
  l.append(luck);
  const [lc, bar, vc] = luck.querySelectorAll('canvas');
  hud.luck = { lc, bar, vc };
  // DIGGING: the newest found block still collecting confirmations.
  const dig = document.createElement('div');
  dig.className = 'stat dig';
  dig.innerHTML = '<div class="lab"><canvas class="pxtext"></canvas></div><div class="bar-row"><canvas class="dig-bar"></canvas><canvas class="pxtext"></canvas></div>';
  l.append(dig);
  const [dl, dbar, dv] = dig.querySelectorAll('canvas');
  hud.dig = { el: dig, lc: dl, bar: dbar, vc: dv };
}

// Pixel-art progress bar: 48 cells, frame, glowing gradient fill with sparkles.
const LUCK_STOPS = ['#1b2a8f', '#2b4cff', '#39a0ff', '#39d0ff', '#8f7dff', '#c77dff'];
function drawBar(canvas, pct, stops = LUCK_STOPS, hi = '#bfe6ff', lo = '#141c5c') {
  const cells = 48, rows = 7;
  const dpr = window.devicePixelRatio || 1;
  const s = Math.max(1, Math.round((narrow() ? 2 : 3) * dpr));
  const W = cells + 4, H = rows + 4;
  if (canvas.width !== W * s) {
    canvas.width = W * s; canvas.height = H * s;
    canvas.style.width = (W * s) / dpr + 'px'; canvas.style.height = (H * s) / dpr + 'px';
  }
  const ctx = canvas.getContext('2d');
  const px = (x, y, c) => { ctx.fillStyle = c; ctx.fillRect(x * s, y * s, s, s); };
  ctx.clearRect(0, 0, canvas.width, canvas.height);
  // Frame: dark outline, inner bevel.
  for (let x = 1; x < W - 1; x++) { px(x, 0, '#120c1c'); px(x, H - 1, '#120c1c'); px(x, 1, '#4b3f8a'); px(x, H - 2, '#2a2050'); }
  for (let y = 1; y < H - 1; y++) { px(0, y, '#120c1c'); px(W - 1, y, '#120c1c'); px(1, y, '#4b3f8a'); px(W - 2, y, '#2a2050'); }
  for (let y = 2; y < H - 2; y++) for (let x = 2; x < W - 2; x++) px(x, y, (x + y) % 2 ? '#0b0a14' : '#100d1f');
  const fill = Math.round((Math.max(0, Math.min(100, pct || 0)) / 100) * cells);
  const t = Date.now() / 120;
  for (let i = 0; i < fill; i++) {
    const stop = stops[Math.min(stops.length - 1, Math.floor((i / cells) * stops.length))];
    for (let r = 0; r < rows; r++) {
      let c = stop;
      if (r === 0) c = hi;                   // top highlight
      else if (r === rows - 1) c = lo;       // bottom shade
      else if ((i * 7 + r * 3 + Math.floor(t)) % 23 === 0) c = '#ffffff'; // sparkle
      px(2 + i, 2 + r, c);
    }
  }
  if (fill > 0 && fill < cells) for (let r = 1; r < rows - 1; r++) px(2 + fill, 2 + r, '#e8f6ff'); // bright leading edge
}

function setStat(key, value, color) {
  const s = hud[key];
  const nar = narrow();
  const big = nar ? 1.25 : 2;
  // Short labels on phones and below 1500 px, so both bottom blocks fit in a row.
  setPixelText(s.lc, nar || window.innerWidth < 1500 ? s.short : s.label, C.ink3, nar ? 1 : 1.5);
  setPixelText(s.vc, String(value).toUpperCase(), color || s.color, big);
}

// The stats live in the bottom bar now, so the scene fills the whole band
// between the top bar and the bottom bar.
function syncInsets() {
  const wrap = document.querySelector('.scene-wrap');
  wrap.style.top = '0px';
  wrap.style.bottom = '0px';
  scene.setBottomInset(0);
}

function renderHUD(st) {
  const p = st.pool, d = st.derived, n = st.node, t = st.template;
  const online = st.workers.filter((w) => w.connections > 0).length;
  setStat('hrNow', fmtHash(p.hashrate_live));
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
  // Best share of the current job; until it has one, show the last job's.
  const lastJob = st.rounds.find((r) => !r.current);
  if (d.best_this_job > 0 || !lastJob) setStat('bestJob', fmtDiff(d.best_this_job));
  else setStat('bestJob', `${fmtDiff(lastJob.best_difficulty)} LAST JOB`, C.ink3);
  setStat('bestAll', fmtDiff(p.best_share_difficulty));
  setStat('found', String(p.blocks_found));
  const cur = st.rounds.find((r) => r.current);
  // Only the % of network difficulty is shown; tiers stay backend-only.
  let beast = '—', beastColor = C.ink2;
  if (cur && cur.tier >= 0) {
    beast = fmtPct(cur.pct_of_network, 3);
    beastColor = RARITY_COLOR[cur.rarity] || (cur.tier > LEGENDARY ? C.gold : C.ink2);
  }
  setStat('creature', beast, beastColor);
  const luckPct = d.luck_since_last_block_pct;
  setPixelText(hud.luck.lc, 'LUCK SINCE LAST BLOCK', C.ink3, narrow() ? 1 : 1.5);
  setPixelText(hud.luck.vc, luckPct == null ? '—' : fmtPct(luckPct, 3), '#7fd8ff', narrow() ? 1.25 : 2);
  drawBar(hud.luck.bar, luckPct);

  // Block being dug out: newest block still collecting confirmations.
  const digging = st.blocks.filter((b) => b.chain_status === 'confirming');
  hud.dig.el.hidden = digging.length === 0;
  if (digging.length) {
    const b = digging[0], m = st.coin.coinbase_maturity, c = Math.max(0, Math.min(b.confirmations, m));
    setPixelText(hud.dig.lc, 'DIGGING OUT (CONFIRMATIONS)', C.ink3, narrow() ? 1 : 1.5);
    setPixelText(hud.dig.vc, `#${b.height} ${c}/${m}` + (digging.length > 1 ? ` +${digging.length - 1} MORE` : ''), C.gold, narrow() ? 1.25 : 2);
    drawBar(hud.dig.bar, (c / m) * 100, ['#8a5a00', '#c98a00', '#ffb347', '#ffd166', '#fff0b0'], '#fff6d6', '#5c3b00');
  }

  // Pending: neutral notice, never a celebration. A missing payout address
  // (no work is issued) takes priority.
  const pill = $('#pending-pill');
  const pending = p.blocks_pending > 0;
  const noPayout = !st.stratum.payout_set;
  pill.hidden = !pending && !noPayout;
  if (noPayout) setPixelText($('canvas', pill), 'NO PAYOUT ADDRESS · SET IT IN THE LEDGER, SETTINGS', C.amber, narrow() ? 1 : 1.5);
  else if (pending) setPixelText($('canvas', pill), `BLOCK SUBMITTED · AWAITING NODE CONFIRMATION (${p.blocks_pending})`, C.amber, narrow() ? 1 : 1.5);

  // Trophy wall (on the back wall of the mine): matured blocks only, each
  // shown as its mounted head or the regular gem, with the block number.
  // Blocks still collecting confirmations are dug out in the scene.
  scene.setDigging(st.blocks.filter((b) => b.chain_status === 'confirming')
    .map((b) => ({ height: b.height, conf: Math.max(0, b.confirmations), maturity: st.coin.coinbase_maturity })));
  const heads = new Map((st.mounts || []).map((m) => [m.block_hash, m]));
  scene.setTrophyWall(st.blocks.filter((b) => b.chain_status === 'matured')
    .map((b) => { const m = heads.get(b.hash); return { height: b.height, head: m ? m.tier : null, variant: m ? m.variant : 0, species: m ? m.species : null }; }));
  syncMountPrompt(st);
  syncInsets();
}

// Found a block while signed in to Settings: offer to mount the head of the
// best creature dropped since the previous block. No names: just the head.
let sessionAt = 0, sessionAuthed = false, mountBusy = false;
async function syncMountPrompt(st) {
  const box = $('#mount-prompt');
  const o = st.mount_offer;
  if (!o) { box.hidden = true; return; }
  if (Date.now() - sessionAt > 15000) {
    sessionAt = Date.now();
    try {
      const r = await fetch('api/admin/session', { cache: 'no-store', credentials: 'same-origin' });
      sessionAuthed = r.ok && (await r.json()).authed === true;
    } catch { sessionAuthed = false; }
  }
  box.hidden = !sessionAuthed;
  if (!sessionAuthed || box._hash === o.block_hash) return;
  box._hash = o.block_hash;
  const head = $('.mp-head', box);
  head.replaceWith(Object.assign(creatureIcon(o.tier, narrow() ? 1.5 : 2, o.variant, o.species), { className: 'mp-head' }));
  setPixelText($('.mp-q', box), 'MOUNT YOUR BEST DROP ON THE TROPHY WALL?', C.gold, narrow() ? 1 : 1.5);
  const answer = async (mount) => {
    if (mountBusy) return;
    mountBusy = true;
    try {
      await fetch('api/admin/mount', {
        method: 'POST', credentials: 'same-origin', cache: 'no-store',
        headers: { 'Content-Type': 'application/json', 'X-WB-Admin': '1' },
        body: JSON.stringify({ block_hash: o.block_hash, mount }),
      });
    } finally { mountBusy = false; }
    box.hidden = true;
  };
  $('#mp-yes').onclick = () => answer(true);
  $('#mp-no').onclick = () => answer(false);
}

// ---------- ledger ----------
let chart;
let settings;
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
    ...(p.bad_messages ? [['Non-stratum', `${esc(fmtInt(p.bad_messages))} <small>lines (not miners, e.g. a browser on the port)</small>`]] : []),
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
    : st.stratum.payout_mode === 'miner'
      ? '<span>per miner (username is the payout address)</span>'
      : '<a class="st-orphaned" href="#/ledger/settings">NOT SET: miners are refused until you set it in Settings</a>';
  kv($('#conn-kv'), [
    ['Stratum URL', `<span class="copyable">stratum+tcp://${esc(host)}:${esc(st.stratum.port)}</span>`],
    ['Username', esc(st.stratum.username_format)],
    ['Password', 'x · d=&lt;difficulty&gt; to pin it · race=elf (human, elf, dwarf, darkelf, halfling, halforc, gnome)'],
    ['Difficulty', esc(diffMode(st.stratum.difficulty))],
    ['Payout', addrHtml],
  ]);
  const btn = $('#reveal');
  if (btn) btn.onclick = () => { revealed = !revealed; btn.textContent = revealed ? addr : maskAddress(addr); };

  // Workers.
  const now = st.now;
  const wrows = st.workers.map((w) => {
    const on = w.connections > 0;
    return `<tr><td>${esc(w.name)}</td><td><span class="badge ${on ? 'st-online' : 'st-offline'}">${on ? 'online' : 'offline'}</span></td>
      <td class="num">${esc(fmtHash(w.hashrate_live))}</td><td class="num">${esc(fmtInt(w.shares_accepted))} / ${esc(fmtInt(w.shares_rejected))}</td>
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
    cell.append(creatureIcon(r.tier, 1.5, r.variant, r.species));
    cell.append(document.createTextNode(r.tier > LEGENDARY ? r.creature : `${r.rarity} · ${r.creature}`));
    tbody.append(tr);
  }
}

function diffMode(d) {
  if (!d) return 'vardiff';
  const base = d.fixed_diff > 0 ? `fixed ${fmtDiff(d.fixed_diff)}` : `vardiff ${fmtDiff(d.vardiff_min)}–${fmtDiff(d.vardiff_max)}, ${d.vardiff_target_seconds}s/share`;
  const n = Object.keys(d.worker_overrides || {}).length;
  return base + (n ? ` · ${n} worker override${n > 1 ? 's' : ''}` : '');
}

async function loadHistory() {
  if (!chart) return;
  try {
    const res = await fetch('api/history?range=' + encodeURIComponent(range), { cache: 'no-store' });
    const j = await res.json();
    chart.set(j.points, 'COLLECTING DATA… (SAMPLED EVERY 30S)');
  } catch { chart.set([], 'HISTORY UNAVAILABLE'); }
}

// "3/100 confirmations" until the coinbase can be spent, then "matured".
function confLabel(b, maturity) {
  if (b.chain_status === 'matured') return 'matured';
  if (b.chain_status === 'orphaned') return 'orphaned';
  return `${Math.max(0, Math.min(b.confirmations, maturity))}/${maturity} confirmations`;
}

// Every job gets a creature as soon as it starts; its rarity follows the
// job's best share as a % of network difficulty, and it leaves when the job
// ends. A job that found a block gets the congratulations rainbow instead.
let shownJob = null;
function syncCreature(st) {
  const cur = st.rounds.find((r) => r.current);
  if (!cur) return;
  if (cur.tier > LEGENDARY) { scene.hideCreature(); shownJob = null; return; }
  const label = `${cur.creature} ${fmtPct(cur.pct_of_network, 3)}`;
  if (!shownJob || shownJob.prev !== cur.prev_hash) {
    scene.showCreature(cur.tier, label, true, cur.variant, cur.species); // new job: a new creature
    shownJob = { prev: cur.prev_hash, tier: cur.tier };
  } else if (cur.tier !== shownJob.tier) {
    scene.showCreature(cur.tier, label, false, cur.variant, cur.species);
    if (cur.tier > shownJob.tier) sound.creature(cur.tier);
    shownJob.tier = cur.tier;
  } else {
    scene.setCreatureName(label);
  }
}

// ---------- party ----------
// Reserved, hand-made characters by worker name (or the part after the last
// "." / "_" / "-"): ernie = the wizard, chester = the dwarf, ash = the elf,
// bandit = the dog, kaido = the angel, molly = Molly (white blob alien).
// Every other miner gets a generated character (heroes.js), race from the
// stratum password ("x,race=elf") or seeded from its name. A character is
// there while its miner is connected; it gets confused when its miner's
// shares come slow, small or few for 5+ minutes, or the miner drops, and
// leaves after 5 minutes disconnected.
const CHARACTERS = { ernie: 'wizard', chester: 'dwarf', ash: 'elf', bandit: 'dog', kaido: 'angel', molly: 'molly' };
const CHARACTER_NAMES = { wizard: 'Wizard', dwarf: 'Dwarf (Rune Smith)', elf: 'Elf Archer', dog: 'Cosmic Dog', angel: 'Angel', molly: 'Molly (Blob Alien)' };
const MEMBERS = ['dwarf', 'elf', 'dog', 'angel', 'molly'];
const CONFUSED_AFTER = 5 * 60 * 1000;
let lastShareAt = 0;
const goneSince = {};
const baseName = (name) => (name || '').toLowerCase().split(/[._-]/).pop();
function characterOf(name) {
  const n = (name || '').toLowerCase();
  return CHARACTERS[n] || CHARACTERS[baseName(name)] || 'hero:' + name;
}
function slowWorkers(ws, now) {
  const last = Math.max(0, ...ws.map((w) => Date.parse(w.last_share_at || '') || 0));
  const hr5 = ws.reduce((a, w) => a + (w.hashrate_5m || 0), 0), hr1h = ws.reduce((a, w) => a + (w.hashrate_1h || 0), 0);
  return (last && now - last >= CONFUSED_AFTER) || (hr1h > 0 && hr5 < 0.25 * hr1h);
}
let lastWorkers = [];
function syncParty(st) {
  const now = Date.now();
  lastWorkers = st.workers || [];
  const online = lastWorkers.filter((w) => w.connections > 0);
  const mine = (id) => online.filter((w) => characterOf(w.name) === id);
  const wiz = mine('wizard');
  scene.setConfused('wizard', wiz.length > 0 && slowWorkers(wiz, now));
  for (const id of MEMBERS) {
    const ws = mine(id);
    if (ws.length) { delete goneSince[id]; scene.setMember(id, true); scene.setConfused(id, slowWorkers(ws, now)); continue; }
    goneSince[id] ??= now;
    if (now - goneSince[id] >= CONFUSED_AFTER) scene.setMember(id, false);
    else scene.setConfused(id, true);
  }
  // Generated characters: online miners, plus recently dropped ones (confused).
  const heroes = [];
  for (const w of lastWorkers) {
    if (!characterOf(w.name).startsWith('hero:')) continue;
    const key = 'hero:' + w.name;
    if (w.connections > 0) { delete goneSince[key]; heroes.push({ name: w.name, race: w.race || null, confused: slowWorkers([w], now) }); continue; }
    if (!(key in goneSince)) { if (!scene.heroes.has(key)) continue; goneSince[key] = now; }
    if (now - goneSince[key] < CONFUSED_AFTER) heroes.push({ name: w.name, race: w.race || null, confused: true });
  }
  scene.setHeroes([...heroes, ...demoHeroes()]);
}

// ---------- demo mode (?demo or ?demo=N): N random miners join the fight ----------
const demoN = (() => { const q = new URLSearchParams(location.search); return q.has('demo') ? Math.max(1, Math.min(60, Number(q.get('demo')) || 20)) : 0; })();
const DEMO_SYL = ['ka', 'ri', 'zo', 'mel', 'dra', 'vin', 'tor', 'sa', 'lu', 'pex', 'gor', 'ny', 'bel', 'qui', 'fen', 'ash', 'rok', 'li', 'mu', 'tha'];
const demo = Array.from({ length: demoN }, (_, i) => ({
  name: `${DEMO_SYL[(i * 7) % 20]}${DEMO_SYL[(i * 13 + 5) % 20]}${i + 1}`,
  hr: 1e12 * (0.2 + ((i * 37) % 23)),
}));
function demoHeroes() { return demo.map((d) => ({ name: d.name, race: null, confused: false })); }
if (demoN) setInterval(() => {
  const d = demo[Math.floor(Math.random() * demo.length)];
  scene.onShares([{ s: Math.random() * Math.random(), members: ['hero:' + d.name] }]);
}, 350);

// ---------- hover / tap a character: race, character, miner and hashrate ----------
const tip = document.createElement('div');
tip.className = 'party-tip';
document.body.append(tip);
function describe(hit) {
  if (!hit) return null;
  if (hit.id.startsWith('hero:')) {
    const name = hit.name, w = lastWorkers.find((x) => x.name === name), d = demo.find((x) => x.name === name);
    return { title: hit.label, name, hr: w ? w.hashrate_1m : d ? d.hr : 0 };
  }
  const ws = lastWorkers.filter((w) => characterOf(w.name) === hit.id && w.connections > 0);
  return { title: CHARACTER_NAMES[hit.id], name: ws.map((w) => w.name).join(', ') || (hit.id === 'wizard' ? 'ernie' : '—'), hr: ws.reduce((a, w) => a + (w.hashrate_1m || 0), 0) };
}
function showTip(ev) {
  const cv = $('#scene'), r = cv.getBoundingClientRect();
  const k = scene.cssPerPx || 1, x = (ev.clientX - r.left) / k, y = (ev.clientY - r.top) / k;
  const info = describe(scene.memberAt(x, y));
  if (!info) { tip.style.display = 'none'; cv.style.cursor = ''; return; }
  tip.innerHTML = `<b>${esc(info.title)}</b><br>${esc(info.name)}<br>${esc(fmtHash(info.hr))}`;
  tip.style.display = 'block';
  tip.style.left = Math.min(window.innerWidth - tip.offsetWidth - 8, ev.clientX + 12) + 'px';
  tip.style.top = Math.max(8, ev.clientY - tip.offsetHeight - 10) + 'px';
  cv.style.cursor = 'pointer';
}
$('#scene').addEventListener('pointermove', showTip);
$('#scene').addEventListener('pointerdown', showTip);
$('#scene').addEventListener('pointerleave', (ev) => { if (ev.pointerType === 'mouse') tip.style.display = 'none'; });

// Attack strength 0..1 per new share: its difficulty as a % of network
// difficulty on a log scale spanning real mainnet shares (1e-10 % = 0,
// 100 % = 1; a typical home-miner share lands around 0.2-0.3). Each share
// is an attack by its miner's character.
function shareStrengths(st, n) {
  const nd = st.template?.network_difficulty || 0;
  const recent = st.pool?.recent_shares || [];
  const list = recent.slice(-Math.min(n, recent.length));
  while (list.length < Math.min(n, 4)) list.unshift({ worker: '', difficulty: 0 });
  return list.map((sh) => {
    const pct = nd > 0 ? (sh.difficulty / nd) * 100 : 0;
    const s = pct > 0 ? Math.max(0, Math.min(1, (Math.log10(pct) + 10) / 12)) : 0;
    return { s, members: sh.worker ? [characterOf(sh.worker)] : [] };
  });
}

// ---------- events from the engine ----------
function onState(next) {
  lastMsg = Date.now();
  setLink('live');
  const ev = blockEvents(prev, next);
  const n = shareDelta(prev, next);
  if (n) { lastShareAt = Date.now(); scene.onShares(shareStrengths(next, n)); }
  syncParty(next);
  scene.setPending(ev.pending);
  if (ev.newPending.length) sound.pending();
  if (ev.celebrate.length) {
    scene.celebrate();
    sound.fanfare();
    showBanner(ev.celebrate[0]);
  }
  syncCreature(next);
  scene.setHashrate(next.pool.hashrate_5m || next.pool.hashrate_1m);
  sound.setIntensity(scene.intensity);
  scene.setSleeping(!next.node.connected || !next.node.synced);
  state = next;
  prev = next;
  renderHUD(next);
  if (bannerBlock) renderBanner();
  if (page === 'ledger') renderLedger(next);
}

let bannerTimer, bannerBlock = null;
function renderBanner() {
  const el = $('#banner');
  const [c1, c2] = el.querySelectorAll('canvas');
  const b = (state?.blocks || []).find((x) => x.hash === bannerBlock) || null;
  if (!b) return;
  setPixelText(c1, 'BLOCK FOUND!', C.gold, narrow() ? 3 : 4);
  const conf = confLabel(b, state.coin.coinbase_maturity).toUpperCase();
  setPixelText(c2, `#${b.height} · ${fmtCoin(b.reward_sats, state.coin.ticker).toUpperCase()} · ${conf}`, C.ink, narrow() ? 1 : 1.5);
}
function showBanner(b) {
  bannerBlock = b.hash;
  renderBanner();
  $('#banner').hidden = false;
  clearTimeout(bannerTimer);
  bannerTimer = setTimeout(() => { $('#banner').hidden = true; bannerBlock = null; }, 9000);
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
    if (!settings) settings = new SettingsPanel($('#settings-body'));
    settings.refresh().then(() => {
      if (location.hash === '#/ledger/settings') $('#sec-settings').scrollIntoView();
    });
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
  setPixelText($('#brand'), 'WIZARD-BLOCKS', C.gold, narrow() ? 1.5 : 2.4);
  pixelizeHeadings();
  buildHUD();
  setupControls();
  route();
  connect();
}

boot();

// Exposed for automated UI checks.
window.__wb = { scene, blockEvents, get state() { return state; } };
