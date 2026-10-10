// Litecoin + Dogecoin Node page: both nodes' live state, refreshed every 5 s. Plain text only.
'use strict';

const int = (n) => (n || 0).toLocaleString('en-US');

function bytes(b) {
  if (!(b > 0)) return '0 B';
  const u = ['B', 'kB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (b >= 1000 && i < u.length - 1) { b /= 1000; i++; }
  return `${b >= 100 ? b.toFixed(0) : b.toFixed(1)} ${u[i]}`;
}

function si(d) {
  if (!(d > 0)) return '-';
  if (d < 1) return d.toExponential(2); // regtest/testnet
  const u = ['', 'k', 'M', 'G', 'T', 'P'];
  let i = 0;
  while (d >= 1000 && i < u.length - 1) { d /= 1000; i++; }
  return d.toFixed(d >= 100 ? 1 : 2) + u[i];
}

function ago(secs) {
  if (!(secs >= 0)) return '-';
  if (secs < 60) return `${Math.round(secs)}s ago`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m ${Math.round(secs % 60)}s ago`;
  if (secs < 86400 * 2) return `${Math.floor(secs / 3600)}h ${Math.floor((secs % 3600) / 60)}m ago`;
  return `${Math.floor(secs / 86400)} days ago`;
}

function state(n) {
  const p = n.paused ? (n.network_active === false ? ' · PAUSED (sync stopped)' : ' · pausing…') : '';
  return base(n) + p;
}

function base(n) {
  switch (n.state) {
    case 'synced': return 'synced';
    case 'syncing': return n.headers > 0 ? `syncing ${(n.progress * 100).toFixed(2)}%` : 'syncing (finding peers)';
    case 'starting': return `starting: ${n.error || ''}`.trim();
    default: return `down: ${n.error || 'not reachable'}`;
  }
}

function render(st) {
  const lines = ['Litecoin + Dogecoin Node', ''];
  for (const n of st.nodes || []) {
    lines.push(n.name);
    lines.push(`  status       ${state(n)}`);
    if (n.state === 'synced' || n.state === 'syncing') {
      lines.push(`  height       ${int(n.blocks)} / ${int(n.headers)} headers`);
      lines.push(`  last block   ${n.tip_time ? ago(st.now - n.tip_time) : '-'}`);
      lines.push(`  peers        ${n.peers === undefined ? '-' : int(n.peers)}`);
      lines.push(`  mempool      ${n.mempool_tx === undefined ? '-' : `${int(n.mempool_tx)} tx (${bytes(n.mempool_bytes)})`}`);
      lines.push(`  difficulty   ${si(n.difficulty)}`);
      lines.push(`  disk         ${n.size_on_disk ? bytes(n.size_on_disk) : '-'}${n.pruned ? ' (pruned)' : ''}`);
      if (n.chain && n.chain !== 'main') lines.push(`  chain        ${n.chain}`);
      lines.push(`  version      ${n.version || '-'}`);
    }
    lines.push('');
  }
  lines.push(`checked ${ago(st.now - Math.min(...(st.nodes || []).map((n) => n.checked || st.now)))}`);
  document.getElementById('out').textContent = lines.join('\n');
  controls(st.nodes || []);
}

// Pause/resume buttons for the nodes whose sync may be paused (Dogecoin):
// lets the Litecoin node have the bandwidth and disk while it syncs.
let busy = false;
function controls(nodes) {
  const box = document.getElementById('controls');
  const want = nodes.filter((n) => n.pausable);
  box.replaceChildren(...want.map((n) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.disabled = busy;
    b.textContent = n.paused ? `resume ${n.name.replace(' Node', '')} sync` : `pause ${n.name.replace(' Node', '')} sync`;
    b.addEventListener('click', () => setPaused(n, !n.paused));
    return b;
  }));
}

async function setPaused(n, paused) {
  busy = true;
  const msg = document.getElementById('msg');
  msg.textContent = `${paused ? 'pausing' : 'resuming'} ${n.name}…`;
  try {
    const r = await fetch(`api/nodes/${encodeURIComponent(n.key)}/sync`, {
      method: 'POST', credentials: 'same-origin',
      headers: { 'X-NS-Action': '1', 'Content-Type': 'application/json' }, // CSRF guard
      body: JSON.stringify({ paused }),
    });
    const j = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(j.error || `HTTP ${r.status}`);
    msg.textContent = paused
      ? `${n.name} sync paused (kept across restarts). Merged mining of this coin is off until you resume.${j.warning ? ' ' + j.warning : ''}`
      : `${n.name} sync resumed.${j.warning ? ' ' + j.warning : ''}`;
  } catch (e) {
    msg.textContent = `could not ${paused ? 'pause' : 'resume'}: ${e.message}`;
  } finally {
    busy = false;
    refresh();
  }
}

async function refresh() {
  try {
    const r = await fetch('api/status', { cache: 'no-store' });
    if (!r.ok) throw new Error(r.status);
    render(await r.json());
  } catch (e) {
    document.getElementById('out').textContent = 'Litecoin + Dogecoin Node\n\nstatus page not reachable';
  }
}

refresh();
setInterval(() => { if (!document.hidden) refresh(); }, 5000);
