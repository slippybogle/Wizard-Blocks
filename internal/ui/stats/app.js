// Plain stats page (WB_UI_STYLE=stats): pool hashrates and best share, and
// each miner's live hashrate (3.5 min average, updated once a minute).
'use strict';

function fmtHash(h) {
  if (!(h > 0)) return '0 H/s';
  const u = ['H/s', 'kH/s', 'MH/s', 'GH/s', 'TH/s', 'PH/s', 'EH/s'];
  let i = 0;
  while (h >= 1000 && i < u.length - 1) { h /= 1000; i++; }
  return `${h >= 100 ? h.toFixed(1) : h.toFixed(2)} ${u[i]}`;
}

function fmtDiff(d) {
  if (!(d > 0)) return '-';
  if (d < 1) return d.toExponential(2);
  const u = ['', 'K', 'M', 'G', 'T', 'P', 'E'];
  let i = 0;
  while (d >= 1000 && i < u.length - 1) { d /= 1000; i++; }
  return d.toFixed(d >= 100 ? 1 : 2) + u[i];
}

function render(st) {
  const p = st.pool || {};
  const d = st.derived || {};
  const name = (st.coin && st.coin.ticker) ? `Wizard-Blocks-${st.coin.ticker}` : 'Wizard-Blocks';
  const lines = [
    name,
    '',
    'Pool',
    `  hashrate 1m    ${fmtHash(p.hashrate_1m)}`,
    `  hashrate 5m    ${fmtHash(p.hashrate_5m)}`,
    `  hashrate 1h    ${fmtHash(p.hashrate_1h)}`,
    `  hashrate 24h   ${fmtHash(d.hashrate_24h)}`,
    `  best share     ${fmtDiff(p.best_share_difficulty)}`,
    '',
    'Miners',
  ];
  const ws = (st.workers || []).slice().sort((a, b) => (b.hashrate_live || 0) - (a.hashrate_live || 0) || a.name.localeCompare(b.name));
  if (ws.length === 0) lines.push('  none yet');
  const width = Math.max(14, ...ws.map((w) => w.name.length + 2));
  for (const w of ws) lines.push(`  ${w.name.padEnd(width)}${fmtHash(w.hashrate_live)}`);
  document.getElementById('out').textContent = lines.join('\n');
}

async function refresh() {
  try {
    const r = await fetch('api/state', { cache: 'no-store' });
    if (!r.ok) throw new Error(r.status);
    render(await r.json());
  } catch (e) {
    document.getElementById('out').textContent = 'engine not reachable';
  }
}

refresh();
setInterval(() => { if (!document.hidden) refresh(); }, 5000);
