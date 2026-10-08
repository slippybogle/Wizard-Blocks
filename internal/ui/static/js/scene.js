// "The Mine": a 16-bit style endless mine rendered into an indexed-colour
// framebuffer with palette cycling. Every layer is generated in code.

import {
  WIZARD_KEYS, WIZARD_BODY, WIZARD_BLINK_ROW, WIZARD_LEGS, SHOULDER,
  CREATURE, CREATURE_HORNS, CREATURE_COLORS, BUBBLE_Q, MINECART,
} from './sprites.js';
import { drawTextBuf } from './font.js';

// ---------- colour helpers ----------

// RGB555 quantisation (SNES-era colour depth).
function q5(v) { v = Math.max(0, Math.min(255, v | 0)) & 0xf8; return v | (v >> 5); }
// Pack to the little-endian Uint32 layout of ImageData (0xAABBGGRR).
function pack(r, g, b) { return (0xff000000 | (q5(b) << 16) | (q5(g) << 8) | q5(r)) >>> 0; }
function hex32(h) { return pack((h >> 16) & 255, (h >> 8) & 255, h & 255); }
function hsv(h, s, v) {
  const i = Math.floor(h * 6), f = h * 6 - i;
  const p = v * (1 - s), q = v * (1 - f * s), t = v * (1 - (1 - f) * s);
  const [r, g, b] = [[v, t, p], [q, v, p], [p, v, t], [p, q, v], [t, p, v], [v, p, q]][((i % 6) + 6) % 6];
  return pack(r * 255, g * 255, b * 255);
}

// Seeded PRNG so the mine looks the same on every visit.
function rng(seed) {
  return () => {
    seed |= 0; seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const BAYER = [0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5].map((v) => (v + 0.5) / 16);
const bayer = (x, y) => BAYER[(y & 3) * 4 + (x & 3)];

// ---------- palette layout ----------
// 0 transparent | 1-8 rock | 9-12 wood | 13-15 metal | 16-19 lantern
// 20-23 gravel | 24-27 deep shadow | 32-47 rainbow | 48-63 rainbow glow
const ROCK = [0x0b0a14, 0x131124, 0x1b1832, 0x241f42, 0x2d2752, 0x383063, 0x463c76, 0x564a8a];
const WOOD = [0x24150c, 0x452815, 0x66401f, 0x8a5a2c];
const METAL = [0x343844, 0x687080, 0xaab3c2];
const LANTERN = [0xff9a3c, 0xffc46b, 0xff7a1f, 0xfff0b0];
const GRAVEL = [0x17121e, 0x211a2b, 0x2b2338, 0x362c46];
const SHADOW = [0x07060c, 0x0d0b16, 0x100d1c, 0x15122a];
const RAIN = 32, GLOW = 48;

const BG_W = 512, BG_H = 512, MID_W = 384, FG_W = 640, FLOOR_W = 240;

export class Scene {
  constructor(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d', { alpha: false });
    this.pal = new Uint32Array(64);
    this.t = 0;
    this.phase = 0;
    this.intensity = 0.2;
    this.running = false;
    this.cam = 0;
    this.listeners = {};

    // Wizard state
    this.wiz = { x: 0, face: 1, state: 'idle', timer: 1.5, walkT: 0, blinkT: 3, swingP: 0 };
    this.swingQueue = 0;
    this.pending = false;
    this.sleeping = false;
    this.celebrateT = 0;
    this.flashT = 0;
    this.particles = [];
    this.creature = null;
    this.zzz = [];

    for (let i = 0; i < 8; i++) this.pal[1 + i] = hex32(ROCK[i]);
    for (let i = 0; i < 4; i++) this.pal[9 + i] = hex32(WOOD[i]);
    for (let i = 0; i < 3; i++) this.pal[13 + i] = hex32(METAL[i]);
    for (let i = 0; i < 4; i++) this.pal[20 + i] = hex32(GRAVEL[i]);
    for (let i = 0; i < 4; i++) this.pal[24 + i] = hex32(SHADOW[i]);
    this.keys = {};
    for (const [k, v] of Object.entries(WIZARD_KEYS)) this.keys[k] = hex32(v);
    this.white = hex32(0xffffff);
    this.gold = [hex32(0xffd166), hex32(0xffa53a), hex32(0xfff3c4)];

    this.genBackground();
    this.mist = [];
    const r = rng(99);
    for (let i = 0; i < 26; i++) this.mist.push({ x: r() * 900, y: r(), r: 8 + r() * 14, v: 2 + r() * 5 });
    this.resize();
    this._ro = new ResizeObserver(() => this.resize());
    this._ro.observe(canvas.parentElement);
    this._loop = (ts) => this.frame(ts);
  }

  on(ev, fn) { (this.listeners[ev] ||= []).push(fn); }
  emit(ev, ...a) { for (const f of this.listeners[ev] || []) f(...a); }

  // ---------- public API ----------
  setHashrate(hr) {
    const v = hr > 0 ? (Math.log10(hr) - 9) / 6 : 0;
    this.intensity = Math.max(0.08, Math.min(1, v));
  }
  addSwings(n) { this.swingQueue = Math.min(6, this.swingQueue + n); }
  setPending(p) { this.pending = p; }
  setSleeping(s) { this.sleeping = s; }
  celebrate() { this.celebrateT = 8; this.flashT = 0.35; }
  setBottomInset(cssPx) { if (cssPx !== this.bottomInsetCss) { this.bottomInsetCss = cssPx; this.resize(); } }
  showCreature(tier, name) { this.creature = { tier, name, t: 0, life: 6 }; }

  start() {
    if (this.running) return;
    this.running = true;
    this.last = performance.now();
    requestAnimationFrame(this._loop);
  }
  stop() { this.running = false; }

  // ---------- geometry ----------
  resize() {
    const parent = this.canvas.parentElement;
    if (parent.clientWidth < 50 || parent.clientHeight < 50) return; // hidden page
    const dpr = window.devicePixelRatio || 1;
    const devW = Math.max(1, Math.round(parent.clientWidth * dpr));
    const devH = Math.max(1, Math.round(parent.clientHeight * dpr));
    // Integer scale: aim for ~150-260 logical px vertically, >= 200 wide.
    const scale = Math.max(1, Math.floor(Math.min(devH / 150, devW / 200)));
    const W = Math.ceil(devW / scale), H = Math.min(BG_H, Math.ceil(devH / scale));
    if (W === this.W && H === this.H && scale === this.scale && this.bottomInsetCss === this._inset) return;
    this._inset = this.bottomInsetCss;
    this.W = W; this.H = H; this.scale = scale;
    this.canvas.width = W; this.canvas.height = H;
    this.canvas.style.width = (W * scale) / dpr + 'px';
    this.canvas.style.height = (H * scale) / dpr + 'px';
    this.img = this.ctx.createImageData(W, H);
    this.buf = new Uint32Array(this.img.data.buffer);
    // Keep the floor above a full-width bottom HUD (portrait phones).
    const inset = Math.round((this.bottomInsetCss || 0) * dpr / scale);
    this.floorY = Math.min(H - 30, Math.max(Math.round(H * 0.45), H - 44 - inset));
    if (!this._placed) { this.wiz.x = this.cam + W * 0.45; this._placed = true; }
    const sx = this.wiz.x - this.cam;
    if (sx < W * 0.3 || sx > W * 0.6) this.wiz.x = this.cam + W * 0.45;
    this.genMid();
    this.genFloor();
    this.genForeground();
    if (!this.running && this.mid) this.render();
  }

  // ---------- layer generation ----------
  genBackground() {
    const r = rng(1337);
    const bg = new Uint8Array(BG_W * BG_H);
    // Periodic value noise (wraps horizontally) at two octaves.
    const grid = (cells) => {
      const g = new Float32Array(cells * cells);
      for (let i = 0; i < g.length; i++) g[i] = r();
      return (x, y) => {
        const fx = (x / BG_W) * cells, fy = (y / BG_H) * cells;
        const x0 = Math.floor(fx), y0 = Math.floor(fy), tx = fx - x0, ty = fy - y0;
        const at = (a, b) => g[((b % cells) + cells) % cells * cells + (((a % cells) + cells) % cells)];
        const sx = tx * tx * (3 - 2 * tx), sy = ty * ty * (3 - 2 * ty);
        const top = at(x0, y0) * (1 - sx) + at(x0 + 1, y0) * sx;
        const bot = at(x0, y0 + 1) * (1 - sx) + at(x0 + 1, y0 + 1) * sx;
        return top * (1 - sy) + bot * sy;
      };
    };
    const n1 = grid(8), n2 = grid(32), n3 = grid(96);
    for (let y = 0; y < BG_H; y++) {
      const vert = 0.55 + 0.45 * Math.sin((y / BG_H) * Math.PI); // darker ceiling and floor
      for (let x = 0; x < BG_W; x++) {
        let v = n1(x, y) * 0.5 + n2(x, y) * 0.35 + n3(x, y) * 0.15;
        v = v * vert;
        const lvl = v * 8.5 + (bayer(x, y) - 0.5) * 1.1;
        bg[y * BG_W + x] = 1 + Math.max(0, Math.min(7, Math.floor(lvl)));
      }
    }
    // Rock strata cracks.
    for (let i = 0; i < 18; i++) {
      let x = r() * BG_W, y = 200 + r() * 300;
      const len = 20 + r() * 60;
      for (let k = 0; k < len; k++) {
        x += 1; y += r() < 0.3 ? (r() < 0.5 ? -1 : 1) : 0;
        bg[(y | 0) * BG_W + ((x | 0) & (BG_W - 1))] = 1;
      }
    }
    // Crystal veins: colour index flows along the vein so palette cycling
    // makes light travel through them.
    const setVein = (x, y, k) => {
      if (y < 1 || y >= BG_H - 1) return;
      const xi = ((x % BG_W) + BG_W) % BG_W;
      bg[y * BG_W + xi] = RAIN + ((k >> 2) & 15);
      for (const [dx, dy] of [[-2, 0], [2, 0], [0, -2], [0, 2], [-1, -1], [1, 1], [1, -1], [-1, 1]]) {
        const yy = y + dy, xx = (((xi + dx) % BG_W) + BG_W) % BG_W;
        const idx = yy * BG_W + xx;
        if (bg[idx] < RAIN && bayer(xx, yy) < 0.35) bg[idx] = GLOW + ((k >> 2) & 15);
      }
    };
    for (let v = 0; v < 7; v++) {
      let y = 200 + r() * 290, vy = 0, x = Math.floor(r() * BG_W);
      const len = 120 + Math.floor(r() * 380), thick = r() < 0.35 ? 2 : 1;
      for (let k = 0; k < len; k++) {
        vy += (r() - 0.5) * 0.35; vy *= 0.92; y += vy;
        for (let t = 0; t < thick; t++) setVein(x + k, Math.round(y) + t, k + v * 7);
        if (r() < 0.02) { // branch
          let by = y, bvy = (r() - 0.5) * 2;
          for (let b = 0; b < 20 + r() * 30; b++) { by += bvy * 0.5; setVein(x + k + b, Math.round(by), k + b); }
        }
      }
    }
    // Twinkling crystal specks.
    for (let i = 0; i < 260; i++) {
      const x = Math.floor(r() * BG_W), y = 150 + Math.floor(r() * 360);
      bg[y * BG_W + x] = RAIN + Math.floor(r() * 16);
    }
    this.bg = bg;
  }

  genMid() {
    const H = this.H, fy = this.floorY;
    const mid = new Uint8Array(MID_W * H);
    const r = rng(7);
    const put = (x, y, c) => {
      if (y < 0 || y >= H) return;
      mid[y * MID_W + (((x % MID_W) + MID_W) % MID_W)] = c;
    };
    const ceil = Math.max(10, fy - 118);
    // Roof timber (continuous) with grain.
    for (let x = 0; x < MID_W; x++) {
      for (let y = ceil; y < ceil + 7; y++) {
        let c = 11;
        if (y === ceil || y === ceil + 6) c = 9;
        else if (y === ceil + 1) c = 12;
        else if ((x * 7 + y * 13) % 23 === 0) c = 10;
        put(x, y, c);
      }
      if (x % 48 === 0) for (let y = ceil + 1; y < ceil + 6; y++) put(x, y, 9); // plank joints
    }
    // Posts, braces, lanterns.
    for (const px of [40, 232]) {
      for (let y = ceil + 7; y <= fy + 4; y++) {
        for (let dx = 0; dx < 7; dx++) {
          let c = dx === 0 || dx === 6 ? 9 : dx === 1 ? 12 : (dx === 4 && (y % 9) < 5) ? 10 : 11;
          put(px + dx, y, c);
        }
      }
      for (let k = 0; k < 14; k++) { // diagonal braces
        for (let t = 0; t < 3; t++) {
          put(px + 7 + k, ceil + 7 + k + t, t === 0 ? 12 : 10);
          put(px - 1 - k, ceil + 7 + k + t, t === 0 ? 12 : 10);
        }
      }
      // Lantern hanging from the roof.
      const lx = px + 30;
      for (let y = ceil + 7; y < ceil + 13; y++) put(lx + 2, y, 13);
      const L = ['.MMM.', 'MLLLM', 'MLWLM', 'MLLLM', '.MMM.'];
      L.forEach((row, yy) => [...row].forEach((ch, xx) => {
        if (ch === 'M') put(lx + xx, ceil + 13 + yy, 14);
        if (ch === 'L') put(lx + xx, ceil + 13 + yy, 16 + ((xx + yy) & 3));
        if (ch === 'W') put(lx + xx, ceil + 13 + yy, 19);
      }));
      // Warm glow around the lantern (dithered).
      for (let dy = -9; dy <= 12; dy++) for (let dx = -9; dx <= 13; dx++) {
        const x = lx + dx, y = ceil + 15 + dy, d = Math.hypot(dx - 2, dy) / 11;
        const xi = ((x % MID_W) + MID_W) % MID_W;
        if (d < 1 && y >= 0 && y < H && mid[y * MID_W + xi] === 0 && bayer(x, y) < (1 - d) * 0.22) mid[y * MID_W + xi] = 17;
      }
    }
    // Crystal clusters (floor and ceiling); shading uses the cycling ramps.
    const spike = (cx, base, h, w, dir, seed) => {
      for (let i = 0; i < h; i++) {
        const half = Math.max(0, Math.round((w / 2) * (1 - i / h)));
        const y = base - dir * i;
        for (let dx = -half; dx <= half; dx++) {
          const bright = dx <= 0;
          put(cx + dx, y, (bright ? RAIN : GLOW) + ((i + seed + (dx < -half + 1 ? 3 : 0)) & 15));
        }
      }
    };
    const cluster = (cx, base, dir, n) => {
      for (let k = 0; k < n; k++) {
        const h = 6 + Math.floor(r() * (dir > 0 ? 18 : 12));
        spike(cx + Math.floor((r() - 0.5) * 14), base, h, 3 + Math.floor(r() * 4), dir, Math.floor(r() * 16));
      }
      for (let dy = -22; dy <= 6; dy++) for (let dx = -14; dx <= 14; dx++) { // halo
        const y = base + dy * dir, x = cx + dx, d = Math.hypot(dx, dy) / 16;
        const xi = ((x % MID_W) + MID_W) % MID_W;
        if (d < 1 && y >= 0 && y < H && mid[y * MID_W + xi] === 0 && bayer(x, y) < (1 - d) * 0.3) {
          mid[y * MID_W + xi] = GLOW + ((dx + dy + 32) & 15);
        }
      }
    };
    cluster(130, fy + 2, 1, 5);
    cluster(320, fy + 2, 1, 4);
    cluster(180, ceil + 7, -1, 4);
    this.mid = mid;
  }

  genFloor() {
    const H = this.H, fy = this.floorY, rows = H - fy;
    const fl = new Uint8Array(FLOOR_W * rows);
    for (let y = 0; y < rows; y++) for (let x = 0; x < FLOOR_W; x++) {
      let c;
      if (y < 3) c = y === 0 ? 7 : 5;
      else {
        const v = (Math.sin(x * 0.7 + y * 1.3) + Math.sin(x * 0.13 - y * 0.4)) * 0.25 + 0.5;
        c = 20 + Math.max(0, Math.min(3, Math.floor(v * 3.6 + (bayer(x, y) - 0.5) * 1.2 - (y > 30 ? 1 : 0))));
      }
      fl[y * FLOOR_W + x] = c;
    }
    // Sleepers and rails.
    for (let x = 0; x < FLOOR_W; x++) {
      if (x % 12 < 6) for (let y = 15; y < 23; y++) fl[y * FLOOR_W + x] = (x % 12 === 0 || y === 22) ? 9 : (y === 15 ? 12 : 10);
      fl[14 * FLOOR_W + x] = 15; fl[15 * FLOOR_W + x] = 14; fl[16 * FLOOR_W + x] = 13;
      fl[21 * FLOOR_W + x] = 15; fl[22 * FLOOR_W + x] = 14; fl[23 * FLOOR_W + x] = 13;
    }
    this.floor = fl;
  }

  genForeground() {
    const H = this.H;
    const fg = new Uint8Array(FG_W * H);
    const r = rng(4242);
    // Stalactites.
    for (let k = 0; k < 9; k++) {
      const cx = Math.floor(r() * FG_W), len = 8 + Math.floor(r() * 26), w = 4 + Math.floor(r() * 7);
      for (let y = 0; y < len && y < H; y++) {
        const half = Math.round((w / 2) * (1 - y / len));
        for (let dx = -half; dx <= half; dx++) {
          const x = (((cx + dx) % FG_W) + FG_W) % FG_W;
          fg[y * FG_W + x] = dx === -half ? 3 : (dx > half - 2 ? 1 : 2);
        }
      }
    }
    // Foreground rock mounds.
    for (let k = 0; k < 4; k++) {
      const cx = Math.floor(r() * FG_W), w = 30 + Math.floor(r() * 40), h = 6 + Math.floor(r() * 10);
      for (let dx = -w; dx <= w; dx++) {
        const top = Math.round(h * Math.sqrt(Math.max(0, 1 - (dx / w) ** 2)));
        for (let y = H - top; y < H; y++) {
          const x = (((cx + dx) % FG_W) + FG_W) % FG_W;
          fg[y * FG_W + x] = y === H - top ? 3 : 25;
        }
      }
    }
    this.fg = fg;
  }

  // ---------- simulation ----------
  frame(ts) {
    if (!this.running) return;
    const dt = Math.min(0.1, (ts - this.last) / 1000);
    this.last = ts;
    this.update(dt);
    this.render();
    requestAnimationFrame(this._loop);
  }

  updatePalette(dt) {
    const I = this.intensity;
    this.phase = (this.phase + dt * (1.5 + 14 * I)) % 16;
    const pulse = 0.5 + 0.5 * Math.sin(this.t * (1 + 3 * I));
    for (let i = 0; i < 16; i++) {
      const h = (((i + this.phase) % 16) / 16);
      this.pal[RAIN + i] = hsv(h, 0.78 + 0.2 * I, 0.62 + 0.3 * I + 0.08 * pulse);
      this.pal[GLOW + i] = hsv(h, 0.65, 0.2 + 0.18 * I + 0.05 * pulse);
    }
    const fl = 0.85 + Math.random() * 0.15;
    for (let i = 0; i < 4; i++) {
      const c = LANTERN[(i + Math.floor(this.t * 6)) & 3];
      this.pal[16 + i] = pack(((c >> 16) & 255) * fl, ((c >> 8) & 255) * fl, (c & 255) * fl);
    }
  }

  update(dt) {
    this.t += dt;
    this.updatePalette(dt);
    const w = this.wiz, W = this.W;
    w.blinkT -= dt;
    if (w.blinkT < -0.15) w.blinkT = 2 + Math.random() * 4;

    if (this.celebrateT > 0) {
      this.celebrateT -= dt;
      w.state = 'celebrate';
      if (this.celebrateT > 4.5 && Math.random() < dt * 40) this.spawnCoin();
    } else if (this.swingQueue > 0 || w.state === 'swing') {
      if (w.state !== 'swing') { w.state = 'swing'; w.swingP = 0; w.face = 1; }
      const prev = w.swingP;
      w.swingP += dt / 0.55;
      if (prev < 0.5 && w.swingP >= 0.5) this.impact();
      if (w.swingP >= 1) { this.swingQueue = Math.max(0, this.swingQueue - 1); w.state = this.swingQueue > 0 ? 'swing' : 'idle'; w.swingP = 0; w.timer = 0.8; }
    } else if (this.pending) {
      w.state = 'pending';
    } else if (this.sleeping) {
      w.state = 'sleep';
      if (Math.random() < dt * 0.8) this.zzz.push({ x: 0, y: 0, t: 0 });
    } else {
      if (w.state === 'celebrate' || w.state === 'pending' || w.state === 'sleep') { w.state = 'idle'; w.timer = 1; }
      w.timer -= dt;
      if (w.timer <= 0) {
        const roll = Math.random();
        if (roll < 0.55) { w.state = 'walk'; w.face = 1; w.timer = 2 + Math.random() * 5; }
        else if (roll < 0.75) { w.state = 'walk'; w.face = -1; w.timer = 0.8 + Math.random() * 1.5; }
        else { w.state = 'idle'; w.timer = 1.5 + Math.random() * 2.5; }
      }
      if (w.state === 'walk') {
        w.walkT += dt;
        w.x += w.face * 20 * dt;
        const sx = w.x - this.cam;
        if (sx > W * 0.56) this.cam = w.x - W * 0.56;
        if (sx < W * 0.34) { w.x = this.cam + W * 0.34; w.state = 'idle'; w.timer = 1; }
        if (Math.random() < dt * 6) this.particles.push({ x: w.x - this.cam + 6, y: this.floorY + 11, vx: -w.face * 8, vy: -6, life: 0.5, c: this.pal[6] });
      }
    }
    // Particles.
    for (const p of this.particles) {
      p.life -= dt; p.vy += (p.g ?? 140) * dt; p.x += p.vx * dt; p.y += p.vy * dt;
      if (p.y > this.floorY + 12 && p.vy > 0) { p.y = this.floorY + 12; p.vy *= -0.35; p.vx *= 0.6; }
    }
    this.particles = this.particles.filter((p) => p.life > 0);
    if (this.particles.length > 500) this.particles.splice(0, this.particles.length - 500);
    for (const z of this.zzz) z.t += dt;
    this.zzz = this.zzz.filter((z) => z.t < 3);
    if (this.creature) { this.creature.t += dt; if (this.creature.t > this.creature.life) this.creature = null; }
    if (this.flashT > 0) this.flashT -= dt;
    for (const m of this.mist) { m.x += m.v * dt; }
  }

  // Pickaxe head position for the current swing (screen coords).
  impact() {
    const g = this.pose();
    const hx = g.ox + g.hand.x, hy = g.oy + g.hand.y;
    const ex = hx + Math.cos(g.angle) * 11, ey = hy + Math.sin(g.angle) * 11;
    for (let i = 0; i < 14; i++) {
      this.particles.push({
        x: ex, y: ey, vx: (Math.random() - 0.2) * 90, vy: -30 - Math.random() * 70,
        life: 0.4 + Math.random() * 0.5, rain: Math.floor(Math.random() * 16),
      });
    }
    this.emit('impact');
  }

  spawnCoin() {
    const w = this.wiz, sx = w.x - this.cam;
    this.particles.push({
      x: sx + 8, y: this.floorY - 20, vx: (Math.random() - 0.5) * 120, vy: -90 - Math.random() * 80,
      life: 2 + Math.random(), coin: true, g: 160,
    });
    this.particles.push({ x: Math.random() * this.W, y: -4, vx: 0, vy: 20 + Math.random() * 30, life: 3, rain: Math.floor(Math.random() * 16), g: 30 });
  }

  // Current wizard pose: origin, legs, hand position, pickaxe angle.
  pose() {
    const w = this.wiz, t = this.t;
    const feet = this.floorY + 12;
    let oy = feet - 26, ox = Math.round(w.x - this.cam), legs = 'stand';
    let hand = { x: 13, y: 17 }, angle = 1.3, pick = true, arms2 = null, gem = false;
    switch (w.state) {
      case 'walk': {
        const f = Math.floor(w.walkT * 8) % 4;
        legs = f === 1 ? 'walk1' : f === 3 ? 'walk3' : 'stand';
        oy -= f % 2;
        hand = { x: 11, y: 15 }; angle = -2.25;
        break;
      }
      case 'swing': {
        const p = w.swingP;
        if (p < 0.3) angle = -1.0 - (p / 0.3) * 1.6;
        else if (p < 0.5) angle = -2.6 + ((p - 0.3) / 0.2) * 3.3;
        else if (p < 0.75) angle = 0.7;
        else angle = 0.7 - ((p - 0.75) / 0.25) * 1.7;
        hand = { x: SHOULDER.x + Math.round(Math.cos(angle) * 4), y: SHOULDER.y + Math.round(Math.sin(angle) * 4) };
        break;
      }
      case 'celebrate': {
        const j = Math.abs(Math.sin(t * 7));
        oy -= Math.round(j * 12);
        legs = j > 0.3 ? 'jump' : 'stand';
        hand = { x: 10, y: 5 }; angle = -1.57 + Math.sin(t * 10) * 0.3;
        arms2 = { x: 3, y: 6 };
        break;
      }
      case 'pending':
        hand = { x: 12, y: 7 }; pick = false; gem = true;
        break;
      case 'sleep':
        oy += 1;
        break;
      default:
        oy -= Math.floor(t * 1.5) % 2 === 0 ? 0 : 0;
    }
    // Facing left mirrors the sprite, hand and angle.
    if (w.face < 0) {
      hand = { x: 15 - hand.x, y: hand.y };
      angle = Math.PI - angle;
      if (arms2) arms2 = { x: 15 - arms2.x, y: arms2.y };
    }
    return { ox, oy, legs, hand, angle, pick, arms2, gem, face: w.face };
  }

  // ---------- rendering ----------
  render() {
    const { W, H, buf, pal, floorY } = this;
    if (!buf) return;
    const bg = this.bg, mid = this.mid, fl = this.floor, fg = this.fg;
    const cam = this.cam;
    const mod = (v, m) => ((Math.floor(v) % m) + m) % m;
    const bgOff = mod(cam * 0.25, BG_W), midOff = mod(cam * 0.6, MID_W);
    const flOff = mod(cam, FLOOR_W), fgOff = mod(cam * 1.6, FG_W);
    const bgY0 = BG_H - H;
    for (let y = 0; y < H; y++) {
      const row = y * W, fgRow = y * FG_W, midRow = y * MID_W;
      const bgRow = (y + bgY0) * BG_W, flRow = (y - floorY) * FLOOR_W;
      let fx = fgOff, mx = midOff, bx = bgOff, lx = flOff;
      for (let x = 0; x < W; x++) {
        let c = fg[fgRow + fx];
        if (!c) {
          c = mid[midRow + mx];
          if (!c) c = y >= floorY ? fl[flRow + lx] : bg[bgRow + bx];
        }
        buf[row + x] = pal[c];
        if (++fx === FG_W) fx = 0;
        if (++mx === MID_W) mx = 0;
        if (++bx === BG_W) bx = 0;
        if (++lx === FLOOR_W) lx = 0;
      }
    }
    this.drawWorldObjects();
    this.drawMist();
    this.drawWizard();
    this.drawParticles();
    this.drawCreature();
    if (this.flashT > 0) this.flash(this.flashT / 0.35);
    this.ctx.putImageData(this.img, 0, 0);
  }

  px(x, y, c) {
    x |= 0; y |= 0;
    if (x >= 0 && y >= 0 && x < this.W && y < this.H) this.buf[y * this.W + x] = c;
  }

  sprite(rows, ox, oy, colorOf, mirror = false) {
    const w = rows[0].length;
    for (let y = 0; y < rows.length; y++) {
      const row = rows[y];
      for (let x = 0; x < w; x++) {
        const ch = row[x];
        if (ch === '.') continue;
        const c = colorOf(ch, x, y);
        if (c !== undefined) this.px(ox + (mirror ? w - 1 - x : x), oy + y, c);
      }
    }
  }

  robe(x, y) { return this.pal[RAIN + (((x + y) >> 1) & 15)]; }

  drawWorldObjects() {
    // A minecart parked on the rails every 700 world px (seeded positions).
    const start = Math.floor((this.cam - 40) / 700), end = Math.floor((this.cam + this.W) / 700);
    for (let k = start; k <= end; k++) {
      const wx = k * 700 + 260 + ((k * 137) % 200);
      const sx = Math.round(wx - this.cam), sy = this.floorY + 6;
      this.sprite(MINECART, sx, sy, (ch, x, y) => {
        if (ch === 'K') return this.keys.K;
        if (ch === 'M') return this.pal[15];
        if (ch === 'm') return this.pal[14];
        if (ch === 'O') return this.pal[13];
        if (ch === 'R') return this.pal[RAIN + ((x + y * 3 + k) & 15)];
        return undefined;
      });
    }
  }

  drawMist() {
    const { W, H, buf } = this;
    const mc = { r: 150, g: 140, b: 200 };
    for (const m of this.mist) {
      const cx = ((((m.x - this.cam * 0.8) % (W + 80)) + (W + 80)) % (W + 80)) - 40;
      const cy = this.floorY - 30 + m.y * 50;
      const r = m.r;
      for (let dy = -r; dy <= r; dy++) {
        const y = Math.round(cy + dy);
        if (y < 0 || y >= H) continue;
        for (let dx = -r * 2; dx <= r * 2; dx++) {
          const x = Math.round(cx + dx);
          if (x < 0 || x >= W) continue;
          const d = Math.hypot(dx / 2, dy) / r;
          if (d >= 1 || bayer(x, y) > (1 - d) * 0.35) continue;
          const i = y * W + x, c = buf[i];
          const rr = c & 255, gg = (c >> 8) & 255, bb = (c >> 16) & 255;
          buf[i] = pack((rr + mc.r) >> 1, (gg + mc.g) >> 1, (bb + mc.b) >> 1);
        }
      }
    }
  }

  line(x0, y0, x1, y1, colorAt) {
    x0 = Math.round(x0); y0 = Math.round(y0); x1 = Math.round(x1); y1 = Math.round(y1);
    const dx = Math.abs(x1 - x0), dy = -Math.abs(y1 - y0), sx = x0 < x1 ? 1 : -1, sy = y0 < y1 ? 1 : -1;
    let err = dx + dy, i = 0;
    for (;;) {
      this.px(x0, y0, colorAt(i++, x0, y0));
      if (x0 === x1 && y0 === y1) break;
      const e2 = 2 * err;
      if (e2 >= dy) { err += dy; x0 += sx; }
      if (e2 <= dx) { err += dx; y0 += sy; }
    }
  }

  drawPickaxe(hx, hy, a) {
    const ex = hx + Math.cos(a) * 11, ey = hy + Math.sin(a) * 11;
    const wood = this.pal[12], woodD = this.pal[10];
    this.line(hx, hy, ex, ey, (i) => (i % 3 === 2 ? woodD : wood));
    // Head: perpendicular, tips bent back toward the handle.
    const px = -Math.sin(a), py = Math.cos(a), bx = -Math.cos(a), by = -Math.sin(a);
    const metal = this.pal[15], metalD = this.pal[14];
    for (let s = -5; s <= 5; s++) {
      const bend = Math.abs(s) >= 4 ? (Math.abs(s) - 3) * 1.2 : 0;
      const x = ex + px * s + bx * bend, y = ey + py * s + by * bend;
      this.px(x, y, Math.abs(s) >= 4 ? metalD : metal);
      this.px(x + bx * 0.9, y + by * 0.9, metalD);
    }
  }

  drawWizard() {
    const g = this.pose(), K = this.keys, mirror = g.face < 0;
    const blink = this.wiz.blinkT < 0;
    const legRows = WIZARD_LEGS[g.legs];
    const colorOf = (ch, x, y) => {
      if (ch === 'R') return this.robe(x, y);
      return K[ch];
    };
    // Pickaxe behind the body when it is over the shoulder.
    const behind = g.pick && Math.sin(g.angle) < -0.2 && this.wiz.state !== 'swing';
    if (behind) this.drawPickaxe(g.ox + g.hand.x, g.oy + g.hand.y, g.angle);
    const body = blink ? WIZARD_BODY.map((r, i) => (i === 9 ? WIZARD_BLINK_ROW : r)) : WIZARD_BODY;
    // Beard sway when idle.
    this.sprite(body, g.ox, g.oy, colorOf, mirror);
    this.sprite(legRows, g.ox, g.oy + 23, colorOf, mirror);
    // Arm (rainbow sleeve) from shoulder to hand, then the hand.
    const sx = mirror ? 15 - SHOULDER.x : SHOULDER.x;
    const arm = (h) => {
      this.line(g.ox + sx, g.oy + SHOULDER.y, g.ox + h.x, g.oy + h.y, (i, x, y) => this.robe(x, y));
      this.px(g.ox + h.x, g.oy + h.y, K.S); this.px(g.ox + h.x + 1, g.oy + h.y, K.S);
      this.px(g.ox + h.x, g.oy + h.y + 1, K.s);
    };
    if (g.pick && !behind) this.drawPickaxe(g.ox + g.hand.x, g.oy + g.hand.y, g.angle);
    arm(g.hand);
    if (g.arms2) arm(g.arms2);
    if (g.gem) {
      const gx = g.ox + g.hand.x - 2, gy = g.oy + g.hand.y - 7;
      const glow = 4 + Math.round(Math.sin(this.t * 5) * 2);
      for (let dy = -glow; dy <= glow; dy++) for (let dx = -glow; dx <= glow; dx++) {
        const d = Math.hypot(dx, dy);
        if (d > glow - 1 && d <= glow && bayer(gx + dx, gy + dy) < 0.6) this.px(gx + 2 + dx, gy + 2 + dy, this.pal[GLOW + ((dx + dy) & 15)]);
      }
      for (let dy = 0; dy < 5; dy++) for (let dx = 0; dx < 5; dx++) {
        if (Math.abs(dx - 2) + Math.abs(dy - 2) <= 2) this.px(gx + dx, gy + dy, this.pal[RAIN + ((dx + dy) & 15)]);
      }
      this.sprite(BUBBLE_Q, g.ox + (mirror ? -8 : 14), g.oy - 12, (ch) => (ch === 'K' ? K.K : this.white));
    }
    if (this.wiz.state === 'sleep') {
      for (const z of this.zzz) {
        drawTextBuf(this.buf, this.W, this.H, 'Z', g.ox + 12 + Math.round(z.t * 4), g.oy - 2 - Math.round(z.t * 9), this.white, K.K);
      }
    }
  }

  drawParticles() {
    for (const p of this.particles) {
      if (p.coin) {
        const tw = Math.floor((this.t + p.x) * 10) % 3;
        const c = this.gold[tw];
        this.px(p.x, p.y, c); this.px(p.x + 1, p.y, c); this.px(p.x, p.y + 1, this.gold[1]); this.px(p.x + 1, p.y + 1, this.gold[1]);
      } else if (p.rain !== undefined) {
        this.px(p.x, p.y, this.pal[RAIN + ((p.rain + Math.floor(this.t * 20)) & 15)]);
      } else {
        this.px(p.x, p.y, p.c);
      }
    }
  }

  drawCreature() {
    const cr = this.creature;
    if (!cr) return;
    const tier = Math.max(0, Math.min(9, cr.tier));
    const scale = tier >= 9 ? 3 : tier >= 5 ? 2 : 1;
    const base = CREATURE_COLORS[tier];
    const C = hex32(base), c = hex32(((base >> 1) & 0x7f7f7f));
    const rows = tier >= 6 ? [...CREATURE_HORNS, ...CREATURE] : CREATURE;
    const enter = Math.min(1, cr.t / 0.6), leave = Math.min(1, (cr.life - cr.t) / 0.6);
    const hop = Math.abs(Math.sin(cr.t * 5)) * 4;
    const w = rows[0].length * scale, h = rows.length * scale;
    const x0 = Math.round(this.W * 0.7 - w / 2 + (1 - enter) * 60 + (1 - leave) * 60);
    const y0 = Math.round(this.floorY + 12 - h - hop);
    const colorOf = (ch) => ({ K: this.keys.K, C, c, W: this.white, Y: this.gold[0] })[ch];
    for (let y = 0; y < rows.length; y++) for (let x = 0; x < rows[0].length; x++) {
      const ch = rows[y][x];
      if (ch === '.') continue;
      const col = colorOf(ch);
      for (let sy = 0; sy < scale; sy++) for (let sx = 0; sx < scale; sx++) this.px(x0 + x * scale + sx, y0 + y * scale + sy, col);
    }
    const label = cr.name.toUpperCase();
    const lw = label.length * 6;
    drawTextBuf(this.buf, this.W, this.H, label, Math.round(x0 + w / 2 - lw / 2), y0 - 11, this.white, this.keys.K);
  }

  flash(a) {
    const n = this.buf.length, buf = this.buf, k = Math.max(0, Math.min(1, a)) * 0.8;
    for (let i = 0; i < n; i++) {
      const c = buf[i];
      const r = c & 255, g = (c >> 8) & 255, b = (c >> 16) & 255;
      buf[i] = (0xff000000 | ((b + (255 - b) * k) << 16) | ((g + (255 - g) * k) << 8) | (r + (255 - r) * k)) >>> 0;
    }
  }
}
