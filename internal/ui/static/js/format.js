// Number/time formatting shared by the Mine and the Ledger.

const SI = ['', 'K', 'M', 'G', 'T', 'P', 'E', 'Z'];

function si(v, digits = 3) {
  if (!isFinite(v) || v <= 0) return '0';
  let i = 0;
  while (v >= 1000 && i < SI.length - 1) { v /= 1000; i++; }
  const s = v >= 100 ? v.toFixed(0) : v.toPrecision(digits);
  return String(parseFloat(s)) + SI[i];
}

export function fmtHash(hs) {
  if (!hs || hs <= 0) return '0 H/s';
  return si(hs) + 'H/s';
}

export function fmtDiff(d) {
  if (!d || d <= 0) return '0';
  if (d < 1) return d.toExponential(2).replace('e', 'E');
  return si(d);
}

export function fmtInt(n) {
  return (n ?? 0).toLocaleString('en-US');
}

export function fmtDur(s) {
  if (s == null || !isFinite(s)) return '∞';
  s = Math.max(0, s);
  const y = Math.floor(s / 31557600); s -= y * 31557600;
  const d = Math.floor(s / 86400); s -= d * 86400;
  const h = Math.floor(s / 3600); s -= h * 3600;
  const m = Math.floor(s / 60); const sec = Math.floor(s - m * 60);
  if (y >= 1000) return si(y) + ' YEARS';
  if (y) return `${y}Y ${d}D`;
  if (d) return `${d}D ${h}H`;
  if (h) return `${h}H ${m}M`;
  if (m) return `${m}M ${sec}S`;
  return `${sec}S`;
}

export function fmtAgo(iso, nowMs) {
  if (!iso) return 'never';
  const t = Date.parse(iso);
  if (!t || t < 0) return 'never';
  return fmtDur(((nowMs ?? Date.now()) - t) / 1000) + ' ago';
}

export function fmtPct(p, maxDigits = 3) {
  if (p == null || !isFinite(p)) return '—';
  if (p === 0) return '0%';
  if (p >= 99.995 && p < 100) return '>99.99%';
  if (p < 0.0001) return p.toExponential(2).replace('e', 'E') + '%';
  if (p < 1) return p.toPrecision(maxDigits) + '%';
  return p.toFixed(p >= 10 ? 1 : 2) + '%';
}

export function fmtBytes(b) {
  if (!b) return '0 B';
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; }
  return b.toFixed(i ? 1 : 0) + ' ' + u[i];
}

export function fmtCoin(sats, ticker) {
  return (sats / 1e8).toFixed(8).replace(/0{1,6}$/, '') + ' ' + ticker;
}

export function shortHash(h) {
  return h ? h.slice(0, 10) + '…' + h.slice(-8) : '';
}

export function maskAddress(a) {
  if (!a) return '';
  const i = a.indexOf(':');
  const prefix = i >= 0 ? a.slice(0, i + 1) : '';
  const body = i >= 0 ? a.slice(i + 1) : a;
  return prefix + body.slice(0, 4) + '••••••••' + body.slice(-4);
}

export function fmtTime(iso) {
  const d = new Date(iso);
  if (isNaN(d)) return '';
  const p = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}
