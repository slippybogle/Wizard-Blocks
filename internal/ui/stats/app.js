// LTC app page (WB_UI_STYLE=stats): engine, node, chains, job, pool, miners
// and blocks, refreshed every 5 s. Built with DOM text nodes only.
'use strict';

const $ = (id) => document.getElementById(id);

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = String(text);
  return e;
}

const int = (n) => Math.round(n || 0).toLocaleString('en-US');
const yes = (b) => (b ? 'yes' : 'no');

function num(n) {
  if (!(n > 0)) return '0';
  if (n < 1) return n.toExponential(2);
  return n.toLocaleString('en-US', { maximumFractionDigits: n >= 1000 ? 1 : 2 });
}

function hash(h) {
  if (!(h > 0)) return '0 H/s';
  const u = ['H/s', 'kH/s', 'MH/s', 'GH/s', 'TH/s', 'PH/s', 'EH/s'];
  let i = 0;
  while (h >= 1000 && i < u.length - 1) { h /= 1000; i++; }
  return `${h.toFixed(2)} ${u[i]}`;
}

function dur(s) {
  s = Math.max(0, Math.floor(s || 0));
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60);
  if (d) return `${d}d ${h}h ${m}m`;
  if (h) return `${h}h ${m}m`;
  if (m) return `${m}m ${s % 60}s`;
  return `${s}s`;
}

function ago(iso, now) {
  const t = Date.parse(iso || '');
  if (!t || t < 1e12) return '—';
  const s = Math.max(0, now - t / 1000);
  if (s < 60) return `${Math.floor(s)}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

const pct = (p) => (p === null || p === undefined ? '—' : `${p.toFixed(2)}%`);

function reasons(m) {
  const parts = Object.entries(m || {}).filter(([, n]) => n > 0).map(([k, n]) => `${k} ${n}`);
  return parts.length ? parts.join(', ') : '';
}

function card(title, rows) {
  const c = el('section', 'card');
  c.append(el('h2', '', title));
  const kv = el('div', 'kv');
  for (const [k, v, bad] of rows) {
    kv.append(el('span', 'k', k), el('span', bad ? 'v bad' : 'v', v));
  }
  c.append(kv);
  return c;
}

function table(title, head, rows, empty) {
  const c = el('section', 'card');
  c.append(el('h2', '', title));
  const w = el('div', 'wrap');
  const t = el('table');
  const hr = el('tr');
  for (const h of head) hr.append(el('th', '', h));
  const thead = el('thead');
  thead.append(hr);
  t.append(thead);
  const tb = el('tbody');
  if (!rows.length) {
    const tr = el('tr');
    const td = el('td', 'empty', empty);
    td.colSpan = head.length;
    tr.append(td);
    tb.append(tr);
  }
  for (const r of rows) {
    const tr = el('tr');
    for (const cell of r) {
      const [text, bad] = Array.isArray(cell) ? cell : [cell, false];
      tr.append(el('td', bad ? 'bad' : '', text));
    }
    tb.append(tr);
  }
  t.append(tb);
  w.append(t);
  c.append(w);
  return c;
}

function render(st) {
  const now = (st.now || Date.now()) / 1000; // st.now is unix milliseconds
  const coin = st.coin || {};
  const ticker = coin.ticker || (coin.coin || '').toUpperCase();
  // Node-reported difficulties are shown as the node reports them; the
  // engine keeps Scrypt difficulty in share units (65536x).
  const scale = ['ltc', 'doge'].includes(coin.coin) ? 65536 : 1;
  const n = st.node || {}, d = st.node_detail || {}, p = st.pool || {}, dv = st.derived || {}, tp = st.template || {}, sv = st.stratum || {};
  const aux = Object.keys(st.chains || {}).filter((c) => c !== coin.coin).map((c) => c.toUpperCase());
  const coins = [ticker, ...aux].join(' + ');
  $('title').textContent = `Wizard-Blocks · ${coins} ${st.chain || n.chain || ''}`.trim();
  $('status').textContent = `live ${new Date(now * 1000).toLocaleTimeString()}`;

  const main = $('main');
  main.replaceChildren();

  const r1 = el('div', 'row');
  r1.append(card('ENGINE', [
    ['version', st.version || '—'],
    ['uptime', dur(st.uptime_s)],
    ['payout set', yes(sv.payout_set), !sv.payout_set],
    ['stratum port', sv.port || '—'],
    ['bad messages', int(p.bad_messages)],
  ]));
  const zmq = n.zmq_connected ? `connected (${int(n.zmq_messages)} msgs)` : (n.zmq_enabled ? 'disconnected' : 'off');
  r1.append(card(`${ticker} NODE`, [
    ['connected', yes(n.connected), !n.connected],
    ['synced', yes(n.synced), !n.synced],
    ['height / headers', `${int(n.height)} / ${int(n.headers)}`],
    ['peers', int(d.peers)],
    ['version', n.subversion || d.subversion || '—'],
    ['ZMQ', zmq, n.zmq_enabled && !n.zmq_connected],
    ['network difficulty', num(d.difficulty || (tp.network_difficulty || 0) / scale)],
    ['network hashrate', hash(d.network_hashps)],
    ['last error', n.last_error || '—', !!n.last_error],
  ]));
  // Merged-mined chains' nodes (DOGE), from the engine's view of them.
  for (const [name, c] of Object.entries(st.chains || {})) {
    if (name === coin.coin) continue;
    r1.append(card(`${name.toUpperCase()} NODE`, [
      ['connected', yes(c.connected), !c.connected],
      ['synced', yes(c.synced), !c.synced],
      ['height', int(c.height)],
      ['ZMQ', c.zmq_connected ? 'connected' : 'disconnected', !c.zmq_connected],
      ['network difficulty', num((c.network_difficulty || 0) / scale)],
      ['merged mining', c.merged ? 'yes' : `no${c.reason ? ` (${c.reason})` : ''}`, !c.merged],
      ['payout address', c.payout_address || 'not set', !c.payout_address],
      ['blocks found / pending', `${int(c.blocks_found)} / ${int(c.blocks_pending)}`],
    ]));
  }
  main.append(r1);

  const chains = Object.entries(st.chains || {}).sort(([a], [b]) => (a === coin.coin ? -1 : b === coin.coin ? 1 : a.localeCompare(b)));
  if (chains.length) {
    main.append(table('CHAINS', ['chain', 'merged', 'connected', 'synced', 'height', 'net diff', 'found', 'pending', 'effort', 'note'],
      chains.map(([name, c]) => [
        name.toUpperCase(),
        [yes(c.merged), !c.merged],
        [yes(c.connected), !c.connected],
        [yes(c.synced), !c.synced],
        int(c.height),
        num((c.network_difficulty || 0) / scale),
        int(c.blocks_found),
        int(c.blocks_pending),
        pct(c.effort_pct),
        c.reason || '',
      ]), 'no chains'));
  }

  const r2 = el('div', 'row');
  r2.append(card('JOB', [
    ['height', int(tp.height)],
    ['updated', ago(tp.updated_at, now)],
    ['transactions', int(tp.transactions)],
    ['new-block events', int(tp.new_block_events)],
    ['refreshes', int(tp.template_refreshes)],
    ['best this job', dv.best_this_job ? `${int(dv.best_this_job)} (${(dv.best_this_job_pct || 0).toFixed(2)}%)` : '—'],
  ]));
  const rej = reasons(p.rejects);
  r2.append(card('POOL', [
    ['live (3.5m)', hash(p.hashrate_live)],
    ['1h', hash(p.hashrate_1h)],
    ['24h', hash(dv.hashrate_24h)],
    ['miners / conns', `${int(p.workers_online)} / ${int(p.connections)}`],
    ['accepted', int(p.shares_accepted)],
    ['rejected', rej ? `${int(p.shares_rejected)} (${rej})` : int(p.shares_rejected), p.shares_rejected > 0],
    ['stale', int(dv.stale_shares)],
    ['best share', p.best_share_difficulty ? `${int(p.best_share_difficulty)} ${p.best_share_worker || ''}`.trim() : '—'],
    ['effort since block', pct(dv.luck_since_last_block_pct)],
    ['blocks found / pending', `${int(p.blocks_found)} / ${int(p.blocks_pending)}`],
  ]));
  main.append(r2);

  const ws = (st.workers || []).slice().sort((a, b) => (b.hashrate_live || 0) - (a.hashrate_live || 0) || a.name.localeCompare(b.name));
  main.append(table('MINERS', ['miner', 'conns', 'diff', 'live', '1h', 'accepted', 'rejected', 'reasons', 'best', 'last share'],
    ws.map((w) => [
      w.name, int(w.connections), int(w.difficulty), hash(w.hashrate_live), hash(w.hashrate_1h), int(w.shares_accepted),
      [int(w.shares_rejected), w.shares_rejected > 0], reasons(w.rejects), int(w.best_share_difficulty), ago(w.last_share_at, now),
    ]), 'no miners yet'));

  const blocks = [
    ...(st.blocks || []).map((b) => ({ ...b, chain: coin.coin })),
    ...(st.aux_blocks || []),
  ].sort((a, b) => Date.parse(b.time) - Date.parse(a.time));
  main.append(table('BLOCKS', ['height', 'chain', 'status', 'reason', 'miner', 'share diff', 'net diff', 'when'],
    blocks.map((b) => [
      int(b.height), (b.chain || coin.coin || '').toUpperCase(), [b.status, b.status === 'rejected' || b.status === 'orphaned'],
      b.reason || '', b.worker || '', num((b.share_difficulty || 0) / scale), num((b.network_difficulty || 0) / scale), ago(b.time, now),
    ]), 'no blocks yet'));
}

// ---------- settings: LTC and DOGE payout addresses ----------
async function api(path, method, body) {
  const res = await fetch(path, {
    method, credentials: 'same-origin',
    headers: { 'X-WB-Admin': '1', ...(body ? { 'Content-Type': 'application/json' } : {}) }, // CSRF guard
    body: body ? JSON.stringify(body) : undefined,
  });
  const j = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(j.error || `HTTP ${res.status}`);
  return j;
}
const smsg = (t) => { $('s-msg').textContent = t; };

function fillPayouts(st) {
  // Show the saved addresses unless the user is typing.
  const ltc = (st.stratum || {}).payout_address || '';
  const doge = ((st.chains || {}).doge || {}).payout_address || '';
  if (!$('s-ltc').dataset.dirty) $('s-ltc').value = ltc;
  if (!$('s-doge').dataset.dirty) $('s-doge').value = doge;
  $('s-doge').closest('.line').hidden = !(st.chains && st.chains.doge);
  $('s-doge').closest('.line').previousElementSibling.hidden = !(st.chains && st.chains.doge);
}

async function syncSession() {
  try {
    const s = await api('api/admin/session', 'GET');
    const locked = !s.enabled || (s.password_required && !s.authed);
    $('s-login').hidden = !(s.enabled && s.password_required && !s.authed);
    $('s-body').hidden = locked;
    if (!s.enabled) smsg('settings are disabled on this engine');
  } catch (e) { smsg(e.message); }
}

for (const id of ['s-ltc', 's-doge']) $(id).addEventListener('input', () => { $(id).dataset.dirty = '1'; });
$('s-ltc-save').addEventListener('click', async () => {
  smsg('checking with the Litecoin node…');
  try {
    const r = await api('api/admin/payout', 'PUT', { address: $('s-ltc').value.trim() });
    delete $('s-ltc').dataset.dirty;
    smsg(`LTC payout saved: ${(r.payout && r.payout.address) || 'ok'}`);
    refresh();
  } catch (e) { smsg(e.message); }
});
$('s-doge-save').addEventListener('click', async () => {
  smsg('checking with the Dogecoin node…');
  try {
    const r = await api('api/admin/doge-payout', 'PUT', { address: $('s-doge').value.trim() });
    delete $('s-doge').dataset.dirty;
    smsg(r.doge_payout_address ? `DOGE payout saved: ${r.doge_payout_address}` : 'DOGE payout cleared: mining Litecoin only');
    refresh();
  } catch (e) { smsg(e.message); }
});
$('s-unlock').addEventListener('click', async () => {
  try { await api('api/admin/login', 'POST', { password: $('s-pw').value }); $('s-pw').value = ''; smsg('unlocked'); syncSession(); } catch (e) { smsg(e.message); }
});

async function refresh() {
  try {
    const r = await fetch('api/state', { cache: 'no-store' });
    if (!r.ok) throw new Error(r.status);
    const st = await r.json();
    render(st);
    fillPayouts(st);
  } catch (e) {
    console.error(e);
    $('status').textContent = 'engine not reachable';
  }
}

refresh();
syncSession();
setInterval(() => { if (!document.hidden) refresh(); }, 5000);
