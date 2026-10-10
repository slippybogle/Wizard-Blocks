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
      if (n.prune_set_mib || n.prune_running_mib) {
        const pend = n.prune_set_mib && n.prune_running_mib && n.prune_set_mib !== n.prune_running_mib ? ` (set ${int(n.prune_set_mib)} MiB, applies on restart)` : '';
        lines.push(`  prune        ${int(n.prune_running_mib || n.prune_set_mib)} MiB${pend}`);
      }
      if (n.chain && n.chain !== 'main') lines.push(`  chain        ${n.chain}`);
      lines.push(`  version      ${n.version || '-'}`);
    }
    lines.push('');
  }
  lines.push(`checked ${ago(st.now - Math.min(...(st.nodes || []).map((n) => n.checked || st.now)))}`);
  document.getElementById('out').textContent = lines.join('\n');
  controls(st.nodes || []);
}

// Controls: pause/resume for the nodes whose sync may be paused (Dogecoin,
// so the Litecoin node has the bandwidth and disk while it syncs), and each
// node's prune target (applying it restarts that node).
let busy = false;
const picked = {}; // prune target chosen in a menu but not applied yet, by node key
function controls(nodes) {
  const box = document.getElementById('controls');
  // Do not rebuild under the user's hands (an open menu would close).
  if (box.contains(document.activeElement) && document.activeElement.tagName === 'SELECT') return;
  const rows = [];
  for (const n of nodes) {
    const coin = n.name.replace(' Node', '');
    if (n.pausable) {
      const b = document.createElement('button');
      b.type = 'button';
      b.disabled = busy;
      b.textContent = n.paused ? `resume ${coin} sync` : `pause ${coin} sync`;
      b.addEventListener('click', () => setPaused(n, !n.paused));
      rows.push(b);
    }
    if (n.prune_options && n.prune_options.length) {
      const row = document.createElement('span');
      row.className = 'prune';
      const label = document.createElement('label');
      label.textContent = `${coin} prune`;
      const sel = document.createElement('select');
      sel.id = `prune-${n.key}`;
      label.htmlFor = sel.id;
      const cur = picked[n.key] || n.prune_set_mib;
      for (const mib of n.prune_options) {
        const o = document.createElement('option');
        o.value = String(mib);
        o.textContent = `${int(mib)} MiB${mib === n.prune_set_mib ? ' (now)' : ''}`;
        o.selected = mib === cur;
        sel.append(o);
      }
      sel.disabled = busy;
      sel.addEventListener('change', () => { picked[n.key] = Number(sel.value); });
      const b = document.createElement('button');
      b.type = 'button';
      b.disabled = busy;
      b.textContent = 'apply (restarts node)';
      b.addEventListener('click', () => setPrune(n, Number(sel.value)));
      row.append(label, sel, b);
      rows.push(row);
    }
  }
  box.replaceChildren(...rows);
}

async function post(path, body) {
  const r = await fetch(path, {
    method: 'POST', credentials: 'same-origin',
    headers: { 'X-NS-Action': '1', 'Content-Type': 'application/json' }, // CSRF guard
    body: JSON.stringify(body),
  });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || `HTTP ${r.status}`);
  return j;
}

async function setPrune(n, mib) {
  const msg = document.getElementById('msg');
  if (mib === n.prune_set_mib) { msg.textContent = `${n.name} is already set to ${int(mib)} MiB.`; return; }
  if (!window.confirm(`Set ${n.name} prune target to ${int(mib)} MiB and restart it now?`)) return;
  busy = true;
  msg.textContent = `setting ${n.name} prune target to ${int(mib)} MiB…`;
  try {
    const j = await post(`api/nodes/${encodeURIComponent(n.key)}/prune`, { mib });
    delete picked[n.key];
    msg.textContent = j.restarting
      ? `${n.name} prune target set to ${int(mib)} MiB; the node is restarting to apply it (a minute or two).`
      : `${n.name} prune target saved (${int(mib)} MiB). ${j.warning || ''}`;
  } catch (e) {
    msg.textContent = `could not set the prune target: ${e.message}`;
  } finally {
    busy = false;
    refresh();
  }
}

async function setPaused(n, paused) {
  busy = true;
  const msg = document.getElementById('msg');
  msg.textContent = `${paused ? 'pausing' : 'resuming'} ${n.name}…`;
  try {
    const j = await post(`api/nodes/${encodeURIComponent(n.key)}/sync`, { paused });
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
