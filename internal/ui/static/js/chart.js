// Single-series pixel line chart with crosshair + tooltip.
import { drawText, textWidth } from './font.js';
import { fmtHash } from './format.js';

const COLORS = {
  surface: '#120f22',
  grid: '#2a2547',
  axis: '#8f88b8',
  line: '#5ee6c0',
  fill: '#5ee6c0',
  cross: '#e8e4ff',
};

export class PixelChart {
  constructor(wrap) {
    this.wrap = wrap;
    this.canvas = document.createElement('canvas');
    this.canvas.className = 'chart-canvas';
    this.tip = document.createElement('div');
    this.tip.className = 'chart-tip';
    this.tip.hidden = true;
    wrap.append(this.canvas, this.tip);
    this.points = [];
    this.hover = -1;
    const move = (e) => {
      const r = this.canvas.getBoundingClientRect();
      const x = ((e.touches ? e.touches[0].clientX : e.clientX) - r.left) / r.width;
      this.pick(x);
    };
    this.canvas.addEventListener('pointermove', move);
    this.canvas.addEventListener('pointerdown', move);
    this.canvas.addEventListener('pointerleave', () => { this.hover = -1; this.tip.hidden = true; this.draw(); });
    new ResizeObserver(() => this.draw()).observe(wrap);
  }

  set(points, emptyText) {
    this.points = points || [];
    this.emptyText = emptyText;
    this.hover = -1;
    this.tip.hidden = true;
    this.draw();
  }

  layout() {
    const dpr = window.devicePixelRatio || 1;
    const s = Math.max(2, Math.round(2 * dpr)); // device px per chart px
    const cssW = this.wrap.clientWidth;
    const W = Math.max(60, Math.floor((cssW * dpr) / s));
    const H = 90;
    this.canvas.width = W; this.canvas.height = H;
    this.canvas.style.width = (W * s) / dpr + 'px';
    this.canvas.style.height = (H * s) / dpr + 'px';
    return { W, H };
  }

  geometry(W, H) {
    const pts = this.points;
    let max = 0;
    for (const p of pts) max = Math.max(max, p.hr);
    max = max > 0 ? max * 1.15 : 1;
    const left = 4, right = W - 2, top = 12, bottom = H - 11;
    const t0 = pts.length ? pts[0].t : 0, t1 = pts.length ? pts[pts.length - 1].t : 1;
    const xOf = (t) => left + (t1 === t0 ? 0 : ((t - t0) / (t1 - t0)) * (right - left));
    const yOf = (v) => bottom - (v / max) * (bottom - top);
    return { max, left, right, top, bottom, xOf, yOf, t0, t1 };
  }

  pick(fx) {
    if (!this.points.length) return;
    const { W } = { W: this.canvas.width };
    const g = this.geometry(W, this.canvas.height);
    const x = fx * W;
    let best = 0, bd = Infinity;
    this.points.forEach((p, i) => { const d = Math.abs(g.xOf(p.t) - x); if (d < bd) { bd = d; best = i; } });
    this.hover = best;
    const p = this.points[best];
    this.tip.hidden = false;
    this.tip.textContent = `${new Date(p.t * 1000).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })} · ${fmtHash(p.hr)}`;
    const px = (g.xOf(p.t) / W) * 100;
    this.tip.style.left = Math.min(70, Math.max(0, px - 15)) + '%';
    this.draw();
  }

  draw() {
    const { W, H } = this.layout();
    const ctx = this.canvas.getContext('2d');
    ctx.imageSmoothingEnabled = false;
    ctx.fillStyle = COLORS.surface;
    ctx.fillRect(0, 0, W, H);
    const pts = this.points;
    const g = this.geometry(W, H);
    // Recessive grid: 3 horizontal lines.
    ctx.fillStyle = COLORS.grid;
    for (let i = 0; i <= 2; i++) {
      const y = Math.round(g.top + ((g.bottom - g.top) * i) / 2);
      for (let x = g.left; x < g.right; x += 2) ctx.fillRect(x, y, 1, 1);
    }
    ctx.fillRect(g.left, g.bottom, g.right - g.left, 1);
    if (pts.length < 2) {
      const msg = this.emptyText || 'COLLECTING DATA…';
      drawText(ctx, msg, Math.max(2, Math.floor((W - textWidth(msg)) / 2)), Math.floor(H / 2) - 4, COLORS.axis, 1);
      return;
    }
    // Dithered area fill under the line.
    const ys = new Array(W).fill(null);
    for (let i = 1; i < pts.length; i++) {
      const xa = g.xOf(pts[i - 1].t), xb = g.xOf(pts[i].t);
      const ya = g.yOf(pts[i - 1].hr), yb = g.yOf(pts[i].hr);
      for (let x = Math.round(xa); x <= Math.round(xb); x++) {
        const f = xb === xa ? 0 : (x - xa) / (xb - xa);
        ys[x] = Math.round(ya + (yb - ya) * f);
      }
    }
    ctx.fillStyle = COLORS.fill;
    ctx.globalAlpha = 0.28;
    for (let x = 0; x < W; x++) {
      if (ys[x] == null) continue;
      for (let y = ys[x] + 1; y < g.bottom; y++) if ((x + y) % 2 === 0) ctx.fillRect(x, y, 1, 1);
    }
    ctx.globalAlpha = 1;
    // 1-chart-pixel line (2 css px).
    ctx.fillStyle = COLORS.line;
    let prev = null;
    for (let x = 0; x < W; x++) {
      if (ys[x] == null) { prev = null; continue; }
      const y = ys[x];
      if (prev != null) {
        const lo = Math.min(prev, y), hi = Math.max(prev, y);
        ctx.fillRect(x, lo, 1, Math.max(1, hi - lo));
      } else ctx.fillRect(x, y, 1, 1);
      prev = y;
    }
    // Axis labels: max value top-left, time span bottom.
    drawText(ctx, fmtHash(g.max / 1.15).toUpperCase(), g.left, 2, COLORS.axis, 1);
    const fmtT = (t) => new Date(t * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false });
    const a = fmtT(g.t0), b = fmtT(g.t1);
    drawText(ctx, a, g.left, H - 8, COLORS.axis, 1);
    drawText(ctx, b, W - 2 - textWidth(b), H - 8, COLORS.axis, 1);
    // Crosshair.
    if (this.hover >= 0 && pts[this.hover]) {
      const p = pts[this.hover];
      const x = Math.round(g.xOf(p.t)), y = Math.round(g.yOf(p.hr));
      ctx.fillStyle = COLORS.cross;
      for (let yy = g.top; yy < g.bottom; yy += 2) ctx.fillRect(x, yy, 1, 1);
      ctx.fillRect(x - 2, y - 2, 5, 5);
      ctx.fillStyle = COLORS.line;
      ctx.fillRect(x - 1, y - 1, 3, 3);
    }
  }
}
