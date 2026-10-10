'use strict';
const $ = (s) => document.querySelector(s);
const esc = (v) => String(v ?? '').replace(/[&<>"']/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const hr = (h) => { if (!h) return '0 H/s'; const u = ['H','KH','MH','GH','TH','PH','EH']; let i = 0; while (h >= 1000 && i < u.length - 1) { h /= 1000; i++; } return h.toFixed(2) + ' ' + u[i] + '/s'; };
const num = (n) => (n == null ? '—' : Number(n).toLocaleString(undefined, {maximumFractionDigits: 2}));
const pct = (n) => (n == null ? '—' : Number(n).toFixed(2) + '%');
const ago = (t) => { if (!t) return '—'; const s = Math.max(0, Math.round((Date.now() - new Date(t)) / 1000)); return s < 120 ? s + 's ago' : s < 7200 ? Math.round(s / 60) + 'm ago' : Math.round(s / 3600) + 'h ago'; };
const dur = (s) => { s = Math.round(s || 0); const d = Math.floor(s / 86400), h = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60); return (d ? d + 'd ' : '') + h + 'h ' + m + 'm'; };
const yes = (b, y = 'yes', n = 'no') => `<span class="${b ? 'ok' : 'bad'}">${b ? y : n}</span>`;
const dl = (rows) => rows.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${v}</dd>`).join('');
const table = (head, rows) => `<tr>${head.map((h) => `<th>${esc(h)}</th>`).join('')}</tr>` + (rows.length ? rows.join('') : `<tr><td colspan="${head.length}">none</td></tr>`);
const rejects = (m) => { const e = Object.entries(m || {}); return e.length ? e.map(([k, v]) => esc(k) + ' ' + v).join(', ') : '—'; };
const statusCls = (s) => (s === 'accepted' || s === 'matured' || s === 'confirming') ? 'ok' : (s === 'pending' ? 'warn' : 'bad');

function render(s) {
  const tick = s.coin && s.coin.ticker ? s.coin.ticker : '';
  $('#title').textContent = `Wizard-Blocks test · ${tick} ${s.chain || ''}`;
  document.title = `WB Test ${tick}`;
  const n = s.node || {}, d = s.node_detail || {}, t = s.template || {}, p = s.pool || {}, dv = s.derived || {};
  $('#engine').innerHTML = dl([
    ['version', esc(s.version)], ['uptime', dur(s.uptime_s)],
    ['payout set', yes(s.stratum && s.stratum.payout_set)], ['stratum port', esc(s.stratum && s.stratum.port)],
    ['bad messages', num(p.bad_messages)],
  ]);
  $('#node').innerHTML = dl([
    ['connected', yes(n.connected)], ['synced', yes(n.synced)],
    ['height / headers', `${num(n.height)} / ${num(n.headers)}`], ['peers', num(d.peers)],
    ['version', esc(n.subversion || d.subversion)], ['ZMQ', n.zmq_enabled ? yes(n.zmq_connected, 'connected', 'down') + ` (${num(n.zmq_messages)} msgs)` : 'off'],
    ['network difficulty', num(t.network_difficulty)], ['network hashrate', hr(d.network_hashps)],
    ['last error', n.last_error ? `<span class="bad">${esc(n.last_error)}</span> ${ago(n.last_error_at)}` : '—'],
  ]);
  const ch = s.chains ? Object.entries(s.chains) : [];
  $('#chains-sec').hidden = !ch.length;
  if (ch.length) $('#chains').innerHTML = table(
    ['chain', 'merged', 'connected', 'synced', 'height', 'net diff', 'found', 'pending', 'effort', 'note'],
    ch.map(([k, c]) => `<tr><td>${esc(k.toUpperCase())}</td><td>${yes(c.merged)}</td><td>${yes(c.connected)}</td><td>${yes(c.synced)}</td><td>${num(c.height)}</td><td>${num(c.network_difficulty)}</td><td>${num(c.blocks_found)}</td><td>${num(c.blocks_pending)}</td><td>${pct(c.effort_pct)}</td><td>${esc(c.reason || '')}</td></tr>`));
  $('#job').innerHTML = dl([
    ['height', num(t.height)], ['updated', ago(t.updated_at)], ['transactions', num(t.transactions)],
    ['new-block events', num(t.new_block_events)], ['refreshes', num(t.template_refreshes)],
    ['best this job', `${num(dv.best_this_job)} (${pct(dv.best_this_job_pct)})`],
  ]);
  $('#pool').innerHTML = dl([
    ['live (3.5m)', hr(p.hashrate_live)], ['1h', hr(p.hashrate_1h)], ['24h', hr(dv.hashrate_24h)],
    ['miners / conns', `${num(p.workers_online)} / ${num(p.connections)}`],
    ['accepted', num(p.shares_accepted)], ['rejected', `${num(p.shares_rejected)} ${p.shares_rejected ? '<span class="bad">' + rejects(p.rejects) + '</span>' : ''}`],
    ['stale', num(dv.stale_shares)], ['best share', `${num(p.best_share_difficulty)} ${esc(p.best_share_worker || '')}`],
    ['effort since block', pct(dv.luck_since_last_block_pct)], ['blocks found / pending', `${num(p.blocks_found)} / ${num(p.blocks_pending)}`],
  ]);
  $('#miners').innerHTML = table(
    ['miner', 'conns', 'diff', 'live', '1h', 'accepted', 'rejected', 'reasons', 'best', 'last share'],
    (s.workers || []).map((w) => `<tr><td>${esc(w.name)}</td><td>${num(w.connections)}</td><td>${num(w.difficulty)}</td><td>${hr(w.hashrate_live)}</td><td>${hr(w.hashrate_1h)}</td><td>${num(w.shares_accepted)}</td><td class="${w.shares_rejected ? 'bad' : ''}">${num(w.shares_rejected)}</td><td>${rejects(w.rejects)}</td><td>${num(w.best_share_difficulty)}</td><td>${ago(w.last_share_at)}</td></tr>`));
  const blocks = [...(s.blocks || []).map((b) => ({...b, chain: b.chain || tick.toLowerCase(), st: b.chain_status || b.status})),
    ...(s.aux_blocks || []).map((b) => ({...b, st: b.status}))].sort((a, b) => new Date(b.time) - new Date(a.time));
  $('#blocks').innerHTML = table(
    ['height', 'chain', 'status', 'reason', 'miner', 'share diff', 'net diff', 'when'],
    blocks.map((b) => `<tr><td>${num(b.height)}</td><td>${esc((b.chain || '').toUpperCase())}</td><td class="${statusCls(b.st)}">${esc(b.st)}</td><td>${esc(b.reason || '')}</td><td>${esc(b.worker)}</td><td>${num(b.share_difficulty)}</td><td>${num(b.network_difficulty)}</td><td>${ago(b.time)}</td></tr>`));
}

async function refresh() {
  try {
    const r = await fetch('../api/state', {cache: 'no-store'});
    if (!r.ok) throw new Error('HTTP ' + r.status);
    render(await r.json());
    $('#link').innerHTML = `<span class="ok">live</span> ${new Date().toLocaleTimeString()}`;
  } catch (e) {
    $('#link').innerHTML = `<span class="bad">no data: ${esc(e.message)}</span>`;
  }
}
refresh();
setInterval(() => { if (!document.hidden) refresh(); }, 5000);
