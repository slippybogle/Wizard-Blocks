// "The Mine": a 16-bit style endless mine rendered into an indexed-colour
// framebuffer with palette cycling. Every layer is generated in code.

import {
  WIZARD_KEYS, WIZARD_BODY, WIZARD_BLINK_ROW, WIZARD_LEGS, SHOULDER,
  creatureColor, LEGENDARY, BUBBLE_Q, MINECART,
  DRAGON_HEAD, TROPHY, DWARF_BODY, DWARF_KEYS, RUNE_GLOW,
  ELF_BODY, ELF_KEYS, DOG_BODY, NEBULA, ANGEL_BODY, ANGEL_KEYS, MOLLY_BODY, MOLLY_KEYS,
} from './sprites.js';
import { drawTextBuf, textWidth } from './font.js';
import { buildHero } from './heroes.js';
import { creatureSprite, creatureHead, creatureBody, CREATURE_KEYS } from './creatures.js';

// Floating members at the back: Kaido's angel and Molly the blob alien.
// at = x right of the wizard's rock, lift = height above the floor,
// hand = where the ray leaves, beam = ray colour.
const FLYERS = {
  angel: { rows: ANGEL_BODY, keys: ANGEL_KEYS, at: 24, lift: 34, hand: { x: 12, y: 8 }, halo: 5, wings: true, beam: 0xffe9a8 },
  molly: { rows: MOLLY_BODY, keys: MOLLY_KEYS, at: 50, lift: 20, hand: { x: 15, y: 8 }, beam: 0x9cff5a },
};

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
const ROCK_H = 10; // the wizard's rock when the party is present

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
    this.castQueue = []; // fireball strengths (0..1) waiting to be thrown
    this.fireballs = [];
    this.dwarf = null; // the Rune Smith, present at >= 10 TH/s
    this.window = [];    // share strengths of the current 20 s window
    this.windowT = 0;
    this.waves = [];   // hammer shockwaves rolling along the floor
    this.enemyShots = []; // the creature's projectiles
    this.arrows = [];     // the elf's arrows
    this.barks = [];      // the dog's barks and shadow balls
    this.beams = [];      // the angel's rays and pillars of light
    this.heroes = new Map(); // generated characters, by 'hero:<miner>'
    this.heroCount = 0;
    this.shakeT = 0;
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
  // Accepted shares: strengths 0..1 (closer to network difficulty = 1).
  // Normally each becomes a fireball at the creature; while a found block is
  // being dug out, they become pickaxe swings at its gem instead.
  // Every share is a small fireball. Every 20 seconds the two largest
  // shares of that window (from all miners) become big attacks by the whole
  // party, each scaled to its share.
  // Accepted shares: [{s, member}]. Each share is an attack by its miner's
  // character, bigger for higher share difficulty; no members = no attack
  // (still counts toward the big attacks). Plain numbers mean the wizard.
  onShares(items) {
    if (!items.length) return;
    items = items.map((it) => (typeof it === 'number' ? { s: it, member: 'wizard' } : it));
    if (this.isDigging()) { this.addSwings(Math.min(3, items.length)); return; }
    for (const it of items) this.window.push(it.s);
    for (const it of items.slice(-4)) {
      for (const id of it.members || [it.member || 'wizard']) {
        if (id === 'wizard') { if (this.castQueue.length < 6) this.castQueue.push(Math.min(it.s, 0.3)); }
        else this.queueAttack(id, it.s);
      }
    }
  }
  // A party member's regular attack on one share; false if it can't fight now.
  queueAttack(id, st) {
    if (id === 'dwarf') {
      if (!this.dwarfReady()) return false;
      if (this.dwarf.swingQueue.length < 3) this.dwarf.swingQueue.push(st);
      return true;
    }
    const m = this.member(id);
    if (!m || m.leaving || m.state === 'enter' || m.confused) return false;
    if (m.queue.length < 3) m.queue.push(st);
    return true;
  }
  // Any party member by id: 'dwarf', 'elf', 'dog', 'angel' or 'hero:<miner>'.
  member(id) { return id && id.startsWith('hero:') ? this.heroes.get(id) : this[id]; }
  dwarfReady() { const d = this.dwarf; return !!(d && !d.leaving && d.state !== 'drop' && !d.confused); }
  bigAttacks() {
    const top = this.window.sort((x, y) => y - x).slice(0, 2);
    this.window = [];
    if (this.isDigging()) return;
    for (const st of top) {
      this.castQueue.unshift(Math.max(st, 0.5));
      const d = this.dwarf;
      if (d && !d.leaving && d.state !== 'drop' && !d.confused && d.slamQueue.length < 2) d.slamQueue.push(st);
      for (const m of [this.elf, this.dog, this.angel, this.molly, ...this.heroes.values()]) {
        if (m && !m.leaving && m.state !== 'enter' && !m.confused && m.bigQueue.length < 2) m.bigQueue.push(st);
      }
    }
  }
  // Low or slow hashrate: confused members look around with a "?" and
  // their attacks fizzle; see app.js for when this is set.
  setConfused(who, on) {
    if (who === 'wizard') this.wiz.confused = on;
    else if (this.member(who)) this.member(who).confused = on;
  }
  // Generated characters for the other miners: [{name, race, confused}].
  // Each is built once from the miner's name (and race), so it always looks
  // the same; they stand in a crowd between the wizard and the front line.
  setHeroes(list) {
    const want = new Map(list.map((h) => ['hero:' + h.name, h]));
    for (const [id, m] of this.heroes) if (!want.has(id) && !m.leaving) m.leaving = true;
    for (const [id, h] of want) {
      let m = this.heroes.get(id);
      if (!m || m.leaving) {
        m = { id, kind: 'hero', name: h.name, raceReq: h.race, hero: buildHero(h.name, h.race), state: 'enter',
          x: m ? m.x : -20 - Math.random() * 60, t: Math.random() * 3, p: 0, queue: [], bigQueue: [], hurtT: 0, blinkT: 2 + Math.random() * 3 };
        this.heroes.set(id, m);
      } else if (m.raceReq !== h.race) { m.raceReq = h.race; m.hero = buildHero(h.name, h.race); }
      m.confused = !!h.confused;
    }
    const live = [...this.heroes.values()].filter((m) => !m.leaving).sort((a, b) => (a.name < b.name ? -1 : 1));
    live.forEach((m, i) => { m.slot = i; m.lane = i % 3; });
    this.heroCount = live.length;
  }
  // Party members by id: the dwarf drops in; the elf and the dog walk in
  // from the left. Leaving, they walk off to the left.
  setMember(id, present) {
    if (id === 'dwarf') return this.setDwarf(present);
    const m = this[id];
    if (present && (!m || m.leaving)) {
      this[id] = { id, state: 'enter', x: m ? m.x : -24, t: 0, p: 0, queue: [], bigQueue: [], hurtT: 0, blinkT: 2 };
    } else if (!present && m && !m.leaving) m.leaving = true;
  }
  // The dwarf arrives (drops from the ceiling) or leaves (walks off left).
  setDwarf(present) {
    if (present && (!this.dwarf || this.dwarf.leaving)) {
      this.dwarf = { state: 'drop', y: -60, vy: 0, t: 0, x: null, slamP: 0, slamQueue: [], swingQueue: [], blinkT: 2, hurtT: 0 };
    } else if (!present && this.dwarf && !this.dwarf.leaving) {
      this.dwarf.leaving = true;
    }
  }
  // With the party present the wizard fights from the back, on a rock at
  // the far left; alone he moves freely.
  inGroup() { return ['dwarf', 'elf', 'dog', 'angel', 'molly'].some((id) => this[id] && !this[id].leaving) || this.heroCount > 0; }
  rockX() { return Math.round(this.W * 0.1); } // screen x of the wizard's rock
  isDigging() { return !!(this.digging && this.digging.length) && !this.congrats; }
  setPending(p) { this.pending = p; }
  setSleeping(s) { this.sleeping = s; }
  celebrate() { this.celebrateT = 8; this.flashT = 0.35; this.congrats = { t: 0, life: 9 }; }
  setBottomInset(cssPx) { if (cssPx !== this.bottomInsetCss) { this.bottomInsetCss = cssPx; this.resize(); } }
  // The current job's creature stays in the mine until the job ends.
  showCreature(tier, name, respawn = false, variant = 0, species = 0) {
    if (!respawn && this.creature && !this.creature.leaving && this.creature.tier === tier && this.creature.variant === variant && this.creature.species === species) return;
    this.creature = { tier, name, variant, species, t: 0, leaving: false, leaveT: 0 };
  }
  // Blocks found but not yet matured: shown as a glowing block in the floor
  // that the wizard digs at (its confirmations are a HUD readout).
  setDigging(list) { this.digging = list || []; }
  // Trophy wall on the back cave wall: [{height, head: tier | null, variant, species}], newest first.
  setTrophyWall(items) { this.trophies = items || []; }
  setCreatureName(name) { if (this.creature && !this.creature.leaving) this.creature.name = name; }
  hideCreature() { if (this.creature) this.creature.leaving = true; }

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
    // Landscape: ~128+ logical px tall. Portrait: zoom in (narrower view) so
    // the mine fills the space instead of leaving empty rock above it.
    const portrait = devH > devW;
    const scale = Math.max(1, Math.floor(portrait ? Math.min(devH / 180, devW / 150) : Math.min(devH / 128, devW / 200)));
    const W = Math.ceil(devW / scale), H = Math.min(BG_H, Math.ceil(devH / scale));
    if (W === this.W && H === this.H && scale === this.scale && this.bottomInsetCss === this._inset) return;
    this._inset = this.bottomInsetCss;
    this.W = W; this.H = H; this.scale = scale;
    this.canvas.width = W; this.canvas.height = H;
    this.canvas.style.width = (W * scale) / dpr + 'px';
    this.canvas.style.height = (H * scale) / dpr + 'px';
    this.cssPerPx = scale / dpr;
    this.img = this.ctx.createImageData(W, H);
    this.buf = new Uint32Array(this.img.data.buffer);
    // Keep the floor above a full-width bottom HUD (portrait phones).
    const inset = Math.round((this.bottomInsetCss || 0) * dpr / scale);
    this.floorY = Math.min(H - 30, Math.max(Math.round(H * 0.45), H - 44 - inset));
    if (!this._placed) { this.wiz.x = this.cam + W * 0.4; this._placed = true; }
    const sx = this.wiz.x - this.cam;
    if (sx < W * 0.3 || sx > W * 0.5) this.wiz.x = this.cam + W * 0.4;
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
    // Roof: 118 px above the floor, or higher up on tall (portrait) scenes
    // so the timber frame fills the view instead of empty rock.
    const ceil = Math.max(10, Math.min(fy - 118, Math.round(H * 0.08)));
    this.ceil = ceil;
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
    } else if (this.isDigging()) {
      // Dig out the found block's gem: walk to it, then swing the pickaxe.
      this.castQueue.length = 0;
      const tx = this.cam + W * 0.62 - 17;
      if (w.state !== 'swing' && Math.abs(w.x - tx) > 1.5) {
        w.state = 'walk'; w.face = w.x < tx ? 1 : -1; w.walkT += dt;
        w.x += w.face * 26 * dt;
        if ((w.face > 0 && w.x > tx) || (w.face < 0 && w.x < tx)) w.x = tx;
      } else if (this.swingQueue > 0 || w.state === 'swing') {
        if (w.state !== 'swing') { w.state = 'swing'; w.swingP = 0; w.face = 1; }
        const prev = w.swingP;
        w.swingP += dt / 0.55;
        if (prev < 0.5 && w.swingP >= 0.5) this.impact();
        if (w.swingP >= 1) { this.swingQueue = Math.max(0, this.swingQueue - 1); w.state = 'dig'; w.swingP = 0; w.timer = 1.4; }
      } else {
        w.state = 'dig'; w.face = 1;
        if ((w.timer -= dt) <= 0) this.swingQueue = 1; // keeps chipping between shares
      }
    } else if (this.castQueue.length || w.state === 'cast') {
      if (w.state !== 'cast') { w.state = 'cast'; w.swingP = 0; w.face = 1; }
      const prev = w.swingP;
      w.swingP += dt / 0.45;
      if (prev < 0.5 && w.swingP >= 0.5) this.throwFireball(this.castQueue[0] ?? 0, w.confused);
      if (w.swingP >= 1) { this.castQueue.shift(); w.state = this.castQueue.length ? 'cast' : 'idle'; w.swingP = 0; w.timer = 0.8; }
    } else if (w.confused) {
      // Looks around, puzzled, turning every second or so.
      w.state = 'confused';
      if ((w.timer -= dt) <= 0) { w.face = -w.face; w.timer = 0.8 + Math.random() * 0.8; }
    } else if (this.inGroup() && !this.pending && !this.sleeping) {
      // Walk back to the rock at the far left, then stand there facing the fight.
      const tx = this.cam + this.rockX();
      if (Math.abs(w.x - tx) > 1.5) {
        w.state = 'walk'; w.face = w.x < tx ? 1 : -1; w.walkT += dt;
        w.x += w.face * 30 * dt;
        if ((w.face > 0 && w.x > tx) || (w.face < 0 && w.x < tx)) w.x = tx;
      } else { w.x = tx; w.state = 'idle'; w.face = 1; }
    } else if (this.pending) {
      w.state = 'pending';
    } else if (this.sleeping) {
      w.state = 'sleep';
      if (Math.random() < dt * 0.8) this.zzz.push({ x: 0, y: 0, t: 0 });
    } else {
      if (w.state === 'celebrate' || w.state === 'pending' || w.state === 'sleep' || w.state === 'dig' || w.state === 'swing' || w.state === 'confused') { w.state = 'idle'; w.timer = 1; }
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
        if (sx > W * 0.5) this.cam = w.x - W * 0.5;
        if (sx < W * 0.3) { w.x = this.cam + W * 0.3; w.state = 'idle'; w.timer = 1; }
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
    if ((this.windowT += dt) >= 20) { this.windowT = 0; this.bigAttacks(); }
    this.updateFireballs(dt);
    this.updateDwarf(dt);
    this.updateMember(this.elf, dt);
    this.updateMember(this.dog, dt);
    this.updateMember(this.angel, dt);
    this.updateMember(this.molly, dt);
    for (const m of [...this.heroes.values()]) this.updateMember(m, dt);
    for (const b of this.beams) b.t += dt;
    this.beams = this.beams.filter((b) => b.t < b.life);
    this.updateMissiles(dt);
    this.updateCreatureAttacks(dt);
    if (this.wiz.hurtT > 0) this.wiz.hurtT -= dt;
    for (const w of this.waves) w.r += (w.speed || 110) * dt;
    if (this.creature) {
      this.creature.t += dt;
      if (this.creature.hitT > 0) this.creature.hitT -= dt;
      if (this.creature.airT > 0) this.creature.airT -= dt;
      if (this.creature.leaving && (this.creature.leaveT += dt) > 0.6) this.creature = null;
    }
    if (this.congrats && (this.congrats.t += dt) > this.congrats.life) this.congrats = null;
    if (this.flashT > 0) this.flashT -= dt;
    if (this.shakeT > 0) this.shakeT -= dt;
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

  // Where the creature currently is (screen coords), or null.
  // home = true: its resting spot (ignores lunges), for positioning.
  creatureBox(home = false) {
    const cr = this.creature;
    if (!cr || cr.tier < 0 || cr.tier > LEGENDARY || cr.leaving) return null;
    const scale = cr.tier >= 2 ? 2 : 1, rows = creatureSprite(cr.tier, cr.species).rows;
    const w = rows[0].length * scale, h = rows.length * scale;
    const enter = Math.min(1, cr.t / 0.6);
    return { x: Math.round(this.W * 0.83 - w / 2 + (1 - enter) * 60 + (home ? 0 : cr.lungeX || 0)), y: Math.round(this.floorY + 12 - h), w, h };
  }

  staffTip() {
    const g = this.pose();
    const hx = g.ox + g.hand.x, hy = g.oy + g.hand.y;
    return { x: hx + Math.cos(g.angle) * 16, y: hy + Math.sin(g.angle) * 16 };
  }

  throwFireball(st, fizzle = false) {
    const tip = this.staffTip();
    const sv = Math.max(0, Math.min(1, st)) * (fizzle ? 0.4 : 1);
    // Weak shares sometimes miss: the fireball sails past behind the creature.
    // A confused wizard's fireballs fizzle and always miss.
    const miss = fizzle || (sv < 0.6 && Math.random() < 0.5 * (1 - sv / 0.6));
    this.fireballs.push({ x: tip.x, y: tip.y, s: sv, t: 0, miss, dy: miss ? (Math.random() < 0.5 ? -1 : 1) * (6 + Math.random() * 6) : 0 });
    this.emit('cast', st);
  }

  updateFireballs(dt) {
    for (const f of this.fireballs) {
      f.t += dt;
      const box = f.miss ? null : this.creatureBox();
      if (f.miss && f.ty === undefined) { const b2 = this.creatureBox(); f.ty = b2 ? b2.y + b2.h / 2 + f.dy : f.y; }
      const tx = box ? box.x + box.w / 2 : this.W + 20, ty = box ? box.y + box.h / 2 : (f.ty ?? f.y);
      const dx = tx - f.x, dy = ty - f.y, d = Math.hypot(dx, dy);
      const sp = (150 + f.s * 70) * dt;
      if (d <= sp + 1) { f.done = true; if (box) this.hit(f, tx, ty); continue; }
      f.x += (dx / d) * sp; f.y += (dy / d) * sp;
      if (f.x > this.W + 10) f.done = true;
      // Ember trail.
      if (Math.random() < 0.6 + f.s) this.particles.push({ x: f.x, y: f.y, vx: -20 - Math.random() * 20, vy: (Math.random() - 0.5) * 20, life: 0.2 + f.s * 0.3, g: -10, c: this.fireColor(f.s, 1 + Math.floor(Math.random() * 2)) });
    }
    this.fireballs = this.fireballs.filter((f) => !f.done);
  }

  // Fire palette: 0 = core ... 3 = outer; stronger shares burn hotter.
  fireColor(st, ring) {
    const hot = [hex32(0xffffff), hex32(0xfff3a0), hex32(0xffb347), hex32(0xff5a1f)];
    const warm = [hex32(0xfff0a0), hex32(0xffa53a), hex32(0xff6a1f), hex32(0xb8320f)];
    return (st > 0.6 ? hot : warm)[Math.min(3, ring)];
  }

  hit(f, x, y) {
    const cr = this.creature;
    if (cr) { cr.hitT = 0.3; cr.hitS = f.s; cr.knock = 2 + f.s * 9; }
    const n = 6 + Math.round(f.s * 22);
    for (let i = 0; i < n; i++) {
      const a = Math.random() * Math.PI * 2, v = 30 + Math.random() * (40 + f.s * 90);
      this.particles.push({ x, y, vx: Math.cos(a) * v, vy: Math.sin(a) * v - 20, life: 0.3 + Math.random() * 0.4, c: this.fireColor(f.s, Math.floor(Math.random() * 4)) });
    }
    if (f.s > 0.85) this.flashT = 0.12;
    this.emit('impact', f.s);
  }

  // behind = true draws missed fireballs (before the creature, so it hides them).
  drawFireballs(behind = false) {
    for (const f of this.fireballs) {
      if (!!f.miss !== behind) continue;
      const r = Math.round(2 + f.s * f.s * 4) + (Math.floor(this.t * 12) % 2 && f.s > 0.4 ? 1 : 0);
      for (let dy = -r - 1; dy <= r + 1; dy++) for (let dx = -r - 1; dx <= r + 1; dx++) {
        const d = Math.hypot(dx, dy);
        if (d > r + 1) continue;
        const ring = d <= r * 0.35 ? 0 : d <= r * 0.7 ? 1 : d <= r ? 2 : 3;
        if (ring === 3 && bayer(f.x + dx, f.y + dy) > 0.5) continue;
        this.px(f.x + dx, f.y + dy, this.fireColor(f.s, ring));
      }
      if (f.s > 0.85) { // near-block shares glow in rainbow
        for (let k = 0; k < 8; k++) {
          const a = this.t * 8 + (k * Math.PI) / 4;
          this.px(f.x + Math.cos(a) * (r + 3), f.y + Math.sin(a) * (r + 3), this.pal[GLOW + ((k * 2 + Math.floor(this.t * 10)) & 15)]);
        }
      }
    }
  }

  drawStaff(hx, hy, a) {
    const ca = Math.cos(a), sa = Math.sin(a);
    const tx = hx + ca * 14, ty = hy + sa * 14;
    const bx = hx - ca * 8, by = hy - sa * 8;
    // Two pixels thick: shaft plus a darker edge.
    this.line(bx + 1, by, tx + 1, ty, () => this.pal[10]);
    this.line(bx, by, tx, ty, (i) => (i % 4 === 3 ? this.pal[11] : this.pal[12]));
    // Glowing orb at the top, coloured from the cave's cycling rainbow.
    const ox = Math.round(tx + ca * 2), oy = Math.round(ty + sa * 2);
    const pulse = Math.floor(this.t * 10);
    for (let dy = -2; dy <= 2; dy++) for (let dx = -2; dx <= 2; dx++) {
      const d = Math.abs(dx) + Math.abs(dy);
      if (d > 3) continue;
      this.px(ox + dx, oy + dy, d === 0 ? this.white : d === 3 ? this.pal[GLOW + ((dx + dy + pulse) & 15)] : this.pal[RAIN + ((dx + dy + pulse) & 15)]);
    }
    if (this.wiz.state === 'cast') {
      for (let k = 0; k < 6; k++) this.px(ox + Math.round(Math.cos(this.t * 9 + k * 1.05) * 4), oy + Math.round(Math.sin(this.t * 9 + k * 1.05) * 4), this.pal[GLOW + ((k * 3 + pulse) & 15)]);
    }
  }

  drawRock() {
    if (!this.inGroup()) return;
    const x0 = this.rockX() - 5, w = 26, top = this.floorY + 12 - ROCK_H;
    for (let y = 0; y < ROCK_H + 1; y++) for (let x = 0; x < w; x++) {
      const inset = y === 0 ? 3 : y === 1 ? 1 : 0;
      if (x < inset || x >= w - inset) continue;
      const edge = y === 0 || x === inset || x === w - 1 - inset;
      this.px(x0 + x, top + y, edge ? this.keys.K : y === 1 ? this.pal[8] : ((x * 3 + y * 5) % 7 === 0 ? this.pal[5] : this.pal[6]));
    }
  }

  question(x, y) {
    const bob = Math.floor(this.t * 3) % 2;
    this.sprite(BUBBLE_Q, x, y - bob, (ch) => (ch === 'K' ? this.keys.K : this.white));
  }

  // ---------- the Rune Smith ----------
  dwarfOrigin() {
    const d = this.dwarf;
    const target = this.dwarfTarget();
    if (d.x === null) d.x = target;
    return { ox: Math.round(d.x), oy: Math.round(this.floorY + 12 - 24 + (d.state === 'drop' ? d.y : 0)) };
  }

  // The dwarf holds the front line, closest to the creature; while the
  // wizard digs out a found block he stands back to make room.
  // Melee: he closes in on the creature so his hammer reaches it.
  dwarfTarget() { return this.frontX(); }
  frontX() {
    if (this.isDigging()) return Math.round(this.W * 0.36);
    const box = this.creatureBox(true);
    const fight = box ? box.x - 30 : this.W * 0.56 - 10;
    return Math.round(Math.max(this.rockX() + 26, Math.min(fight, this.W - 70)));
  }

  dwarfAngle() {
    const d = this.dwarf;
    if (d.state === 'swing') { // quick hit on a share
      const p = d.swingP;
      if (p < 0.3) return -1.45 - (p / 0.3) * 0.4;
      if (p < 0.45) return -1.85 + ((p - 0.3) / 0.15) * 2.2;
      if (p < 0.65) return 0.35;
      return 0.35 - ((p - 0.65) / 0.35) * 1.8;
    }
    if (d.state !== 'slam') return -1.45;
    const p = d.slamP;
    if (p < 0.35) return -1.45 - (p / 0.35) * 0.5;
    if (p < 0.5) return -1.95 + ((p - 0.35) / 0.15) * 2.45;
    if (p < 0.75) return 0.5;
    return 0.5 - ((p - 0.75) / 0.25) * 1.95;
  }

  updateDwarf(dt) {
    const d = this.dwarf;
    if (!d) return;
    d.t += dt;
    if ((d.blinkT -= dt) < -0.15) d.blinkT = 2 + Math.random() * 4;
    if (d.state === 'drop') {
      d.vy += 260 * dt; d.y += d.vy * dt;
      if (d.y >= 0) { d.y = 0; d.state = 'idle'; const o = this.dwarfOrigin(); this.slamImpact(o.ox + 18, 0.7); }
      return;
    }
    if (d.leaving) {
      d.x -= 30 * dt;
      if (d.x < -30) this.dwarf = null;
      return;
    }
    if (d.hurtT > 0) d.hurtT -= dt;
    // Move into fighting position next to the creature.
    const target = this.dwarfTarget();
    if (d.state === 'idle' && Math.abs(d.x - target) > 1) { d.x += Math.sign(target - d.x) * Math.min(Math.abs(target - d.x), 30 * dt); d.walking = true; }
    else d.walking = false;
    if (d.state === 'idle' && d.slamQueue.length && !d.confused) { d.state = 'slam'; d.slamP = 0; d.power = d.slamQueue.shift(); }
    else if (d.state === 'idle' && d.swingQueue.length && !d.confused && !d.walking) { d.state = 'swing'; d.swingP = 0; d.power = d.swingQueue.shift(); }
    if (d.state === 'swing') {
      const prev = d.swingP;
      d.swingP += dt / 0.5;
      if (prev < 0.45 && d.swingP >= 0.45) {
        const o = this.dwarfOrigin(), a = 0.35;
        this.meleeHit(o.ox + 18 + Math.cos(a) * 18, o.oy + 15 + Math.sin(a) * 18, d.power ?? 0.2);
      }
      if (d.swingP >= 1) d.state = 'idle';
    }
    if (d.state === 'slam') {
      const prev = d.slamP;
      d.slamP += dt / 0.9;
      if (prev < 0.5 && d.slamP >= 0.5) {
        const o = this.dwarfOrigin(), a = 0.5;
        this.slamImpact(o.ox + 18 + Math.cos(a) * 18, d.power ?? 0.5);
      }
      if (d.slamP >= 1) d.state = 'idle';
    }
  }


  // ---------- the elf archer and the cosmic dog ----------
  presentMembers() {
    const ids = ['dwarf', 'dog', 'elf', 'angel', 'molly'].filter((id) => this[id] && !this[id].leaving && this[id].state !== 'drop' && this[id].state !== 'enter');
    for (const m of this.heroes.values()) if (!m.leaving && m.state !== 'enter') ids.push(m.id);
    return ids;
  }
  // Ranged members hang back: the dog just behind the front line, the elf
  // further back in the near lane (lower on screen, closer to the viewer).
  memberTarget(m) {
    if (FLYERS[m.id]) return Math.round(this.rockX() + FLYERS[m.id].at); // floats above the back
    if (m.kind === 'hero') { // the crowd: three lanes between the wizard and the front line
      const L = this.rockX() + 26, R = Math.max(L + 10, this.frontX() - 14), ncol = Math.max(1, Math.ceil(this.heroCount / 3));
      return Math.round(L + ((R - L) * (Math.floor((m.slot || 0) / 3) + 0.5)) / ncol - 6 + ((m.lane || 0) - 1) * 5);
    }
    const f = this.frontX(), min = this.rockX() + 22;
    return Math.round(Math.max(min + (m.id === 'dog' ? 26 : 0), f - (m.id === 'dog' ? 32 : 62)));
  }
  memberOrigin(m) {
    if (m.kind === 'hero') return { ox: Math.round(m.x), oy: Math.round(this.floorY + 12 + [-5, 1, 7][m.lane || 0] - m.hero.h) };
    if (FLYERS[m.id]) { const f = FLYERS[m.id]; return { ox: Math.round(m.x), oy: Math.round(this.floorY + 12 - f.rows.length - f.lift + Math.sin(m.t * 1.8 + (m.id === 'molly' ? 2 : 0)) * 3) }; }
    const h = m.id === 'elf' ? 24 : 13, lane = m.id === 'elf' ? 7 : 0;
    return { ox: Math.round(m.x), oy: Math.round(this.floorY + 12 + lane - h) };
  }
  // Mouth / bow hand, where shots leave from.
  memberMuzzle(m) {
    const o = this.memberOrigin(m);
    if (m.kind === 'hero') return { x: o.ox + m.hero.hand.x + 1, y: o.oy + m.hero.hand.y - 2 };
    return m.id === 'elf' ? { x: o.ox + 13, y: o.oy + 10 } : FLYERS[m.id] ? { x: o.ox + FLYERS[m.id].hand.x, y: o.oy + FLYERS[m.id].hand.y } : { x: o.ox + 19, y: o.oy + 5 };
  }

  updateMember(m, dt) {
    if (!m) return;
    m.t += dt;
    if ((m.blinkT -= dt) < -0.15) m.blinkT = 2 + Math.random() * 4;
    if (m.hurtT > 0) m.hurtT -= dt;
    if (m.leaving) { m.x -= 34 * dt; m.walking = true; if (m.x < -30) { if (m.kind === 'hero') this.heroes.delete(m.id); else this[m.id] = null; } return; }
    const target = this.memberTarget(m);
    const busy = m.state === 'shoot' || m.state === 'bark' || m.state === 'ray' || m.state === 'big';
    if (!busy && Math.abs(m.x - target) > 1) { m.x += Math.sign(target - m.x) * Math.min(Math.abs(target - m.x), 34 * dt); m.walking = true; return; }
    m.walking = false;
    if (m.state === 'enter') m.state = 'idle';
    if (this.isDigging()) { m.queue.length = 0; m.bigQueue.length = 0; }
    if (m.state === 'idle' && !m.confused) {
      if (m.bigQueue.length) { m.state = 'big'; m.p = 0; m.power = m.bigQueue.shift(); }
      else if (m.queue.length) { m.state = { elf: 'shoot', dog: 'bark', angel: 'ray', molly: 'ray' }[m.id] || 'shoot'; m.p = 0; m.power = m.queue.shift(); }
    }
    if (m.state === 'idle') return;
    const prev = m.p, dur = m.kind === 'hero' ? (m.state === 'big' ? 0.75 : 0.45) : m.state === 'big' ? (m.id === 'dog' ? 1.1 : FLYERS[m.id] ? 1.2 : 0.8) : m.id === 'elf' ? 0.5 : FLYERS[m.id] ? 0.5 : 0.35;
    m.p += dt / dur;
    const at = m.kind === 'hero' ? 0.5 : m.id === 'dog' && m.state === 'big' ? 0.7 : FLYERS[m.id] ? 0.35 : 0.6;
    if (prev < at && m.p >= at) this.memberFire(m);
    if (m.p >= 1) m.state = 'idle';
  }

  // Elf: one arrow for a small share, more for bigger ones; her big attack
  // is a volley, plus a glowing arrow that pierces on the best shares.
  // Dog: a bark per share; his big attack is a black shadow ball.
  memberFire(m) {
    const st = Math.max(0, Math.min(1, m.power ?? 0.2)), o = this.memberMuzzle(m);
    const box = this.creatureBox();
    const tx = box ? box.x + box.w * 0.4 : this.W * 0.83, ty = box ? box.y + box.h * 0.5 : this.floorY;
    if (m.kind === 'hero') return this.heroFire(m, st, o, tx, ty);
    if (m.id === 'elf') {
      const big = m.state === 'big';
      const n = big ? 3 + Math.round(st * 3) : 1 + Math.floor(st * 4);
      for (let i = 0; i < n; i++) this.arrows.push(this.aimArrow(o, tx, ty + (i - (n - 1) / 2) * 3, st, i * 0.07, false));
      if (big && st > 0.7) this.arrows.push(this.aimArrow(o, tx, ty, st, n * 0.07 + 0.1, true));
      this.emit('cast', st);
    } else if (FLYERS[m.id]) {
      // Angel: a ray of light per share, wider and brighter for bigger ones;
      // her big attack is a pillar of light, up to a full blinding pillar.
      const big = m.state === 'big';
      const c = FLYERS[m.id].beam;
      this.beams.push(big ? { kind: 'pillar', x: tx, s: st, t: 0, life: 0.9, c } : { kind: 'ray', x0: o.x, y0: o.y, x1: tx, y1: ty, s: st, t: 0, life: 0.3, c });
      if (this.creature) Object.assign(this.creature, big ? { hitT: 0.4, hitS: st, knock: 3 + st * 8 } : { hitT: 0.2, hitS: st, knock: 1 + st * 3 });
      if (big) { this.shakeT = Math.max(this.shakeT, 0.1 + st * 0.2); if (st > 0.8) this.flashT = Math.max(this.flashT, 0.15); }
      for (let i = 0; i < (big ? 14 + Math.round(st * 20) : 4); i++) this.particles.push({ x: tx + (Math.random() - 0.5) * 10, y: ty + (Math.random() - 0.5) * 10, vx: (Math.random() - 0.5) * 50, vy: -20 - Math.random() * 40, life: 0.5, g: 20, c: hex32(i % 2 ? 0xffe9a8 : 0xffffff) });
      this.emit('impact', st);
    } else if (m.state === 'big') {
      this.barks.push({ kind: 'shadow', x: o.x + 2, y: o.y, s: st, r: 3 + st * 6, tx, ty, t: 0 });
      this.shakeT = Math.max(this.shakeT, 0.06);
    } else {
      this.barks.push({ kind: 'bark', x: o.x, y: o.y, s: st, t: 0, tx });
    }
  }
  // Generated characters attack by weapon: bows shoot arrows, staves cast
  // bolts, daggers are thrown, everything else sends a slash. Big attacks
  // fire more and bigger.
  heroFire(m, st, o, tx, ty) {
    const big = m.state === 'big', H = m.hero, c = H.accent;
    const straight = (extra, delay) => { const v = 170, T = Math.max(0.15, (tx - o.x) / v); return { x: o.x, y: o.y, vx: v, vy: (ty - o.y) / T, g: 0, s: st, delay, hit: false, t: 0, c, ...extra }; };
    if (H.attack === 'arrow') {
      const n = big ? 3 + Math.round(st * 3) : 1 + Math.floor(st * 2.5);
      for (let i = 0; i < n; i++) this.arrows.push(this.aimArrow(o, tx, ty + (i - (n - 1) / 2) * 3, st, i * 0.07, false));
    } else if (H.attack === 'bolt') {
      for (let i = 0; i < (big ? 3 : 1); i++) this.arrows.push(straight({ kind: 'bolt', r: big ? 2 + st * 3 : 1 + st * 1.5 }, i * 0.12));
    } else if (H.attack === 'dagger') {
      const n = big ? 3 : 1;
      for (let i = 0; i < n; i++) this.arrows.push({ ...this.aimArrow(o, tx, ty + (i - (n - 1) / 2) * 4, st, i * 0.08, false), kind: 'dagger', c });
    } else {
      this.arrows.push(straight({ kind: 'slash', size: big ? 4 + st * 5 : 2 + st * 2 }, 0));
      if (big) this.arrows.push(straight({ kind: 'slash', size: 3 + st * 4 }, 0.12));
    }
    this.emit('cast', st);
  }

  drawHero(m) {
    const H = m.hero;
    let { ox, oy } = this.memberOrigin(m);
    if (m.kFor !== H) { m.k = Object.fromEntries(Object.entries(H.keys).map(([k, v]) => [k, hex32(v)])); m.kFor = H; } // colours, rebuilt if the race changes
    const KK = m.k;
    const bob = m.walking ? Math.floor(m.t * 8) % 2 : (m.state === 'idle' ? Math.floor(m.t * 1.5) % 2 : 0);
    const hurtFlash = m.hurtT > 0 && Math.floor(this.t * 20) % 2;
    if (m.hurtT > 0) ox -= Math.round((m.hurtT / 0.4) * 3);
    oy -= bob;
    const blink = m.blinkT < 0;
    this.sprite(H.rows, ox, oy, (ch) => (hurtFlash && ch !== 'K' ? this.white : blink && ch === 'E' ? KK.S : KK[ch]));
    this.drawHeroWeapon(m, ox + H.hand.x, oy + H.hand.y);
    if (m.confused && m.state !== 'enter') this.question(ox + 3, oy - 12);
  }

  drawHeroWeapon(m, hx, hy) {
    const H = m.hero, kind = H.weapon, acting = (m.state === 'shoot' || m.state === 'big') ? m.p : -1;
    const wood = hex32(0x7a4a22), steel = hex32(0xdfe6f0), dark = hex32(0x8a93a3), gold = hex32(0xffd23f), acc = hex32(H.accent);
    if (kind === 'bow') {
      const draw = acting >= 0 && acting < 0.5 ? Math.min(1, acting / 0.35) : 0, pull = Math.round(draw * 4);
      for (let j = -6; j <= 6; j++) this.px(hx + 1 + Math.round(2 * Math.cos((j / 6) * Math.PI / 2)), hy + j, wood);
      for (let j = -6; j <= 6; j++) this.px(hx + 1 - Math.round(pull * (1 - Math.abs(j) / 6)), hy + j, hex32(0xe8e0c8));
      return;
    }
    const rest = { sword: -1.0, axe: -1.1, mace: -1.1, hammer: -1.2, spear: -1.45, dagger: -0.5, staff: -1.57 }[kind] ?? -1;
    let a = rest, reach = 0;
    if (acting >= 0) {
      if (kind === 'spear') { a = -0.15; reach = acting < 0.5 ? Math.round((acting / 0.5) * 4) : Math.round((1 - acting) * 8); }
      else if (kind === 'staff') a = -1.57 + Math.sin(acting * Math.PI) * 0.5;
      else if (acting < 0.4) a = rest - (acting / 0.4) * 0.8;
      else if (acting < 0.55) a = rest - 0.8 + ((acting - 0.4) / 0.15) * 2.2;
      else a = rest + 1.4 - ((acting - 0.55) / 0.45) * 1.4;
    }
    const L = { sword: 9, axe: 9, mace: 8, hammer: 9, spear: 14, dagger: 5, staff: 14 }[kind] ?? 8;
    const dx = Math.cos(a), dy = Math.sin(a), pvx = -dy, pvy = dx;
    const bx = hx + dx * reach - dx * (kind === 'spear' || kind === 'staff' ? 4 : 0), by = hy + dy * reach - dy * (kind === 'spear' || kind === 'staff' ? 4 : 0);
    const P = (u, v, c) => this.px(bx + dx * u + pvx * v, by + dy * u + pvy * v, c);
    for (let u = 0; u <= L; u++) {
      if (kind === 'sword' || kind === 'dagger') {
        const blade = u > (kind === 'sword' ? 2 : 1);
        P(u, 0, blade ? (u === L ? this.white : steel) : wood);
        if (u === (kind === 'sword' ? 2 : 1)) { P(u, -1, gold); P(u, 1, gold); }
      } else P(u, 0, u > L - 2 && kind === 'spear' ? steel : wood);
    }
    if (kind === 'axe') for (let u = L - 3; u <= L; u++) for (let v = 1; v <= 3; v++) P(u, v, v === 3 ? this.white : dark);
    if (kind === 'hammer') for (let u = L - 1; u <= L + 1; u++) for (let v = -2; v <= 2; v++) P(u, v, Math.abs(v) === 2 ? this.keys.K : dark);
    if (kind === 'mace') { for (let u = L - 1; u <= L + 1; u++) for (let v = -1; v <= 1; v++) P(u, v, dark); P(L + 2, 0, steel); P(L, -2, steel); P(L, 2, steel); }
    if (kind === 'spear') { P(L + 1, 0, this.white); P(L - 1, -1, steel); P(L - 1, 1, steel); }
    if (kind === 'staff') {
      const glow = acting >= 0 || Math.floor(this.t * 3 + (m.t || 0)) % 3 === 0;
      for (let u = L; u <= L + 2; u++) for (let v = -1; v <= 1; v++) P(u, v, u === L + 1 && v === 0 && glow ? this.white : acc);
    }
  }

  // Which character is at screen pixel (x, y)? Front lane first.
  memberAt(x, y) {
    const boxes = [];
    const add = (id, ox, oy, w, h, z) => boxes.push({ id, ox, oy, w, h, z });
    const g = this.pose(); add('wizard', g.ox, g.oy, 16, 26, 0);
    if (this.dwarf && !this.dwarf.leaving) { const o = this.dwarfOrigin(); add('dwarf', o.ox, o.oy, 22, 24, 1); }
    for (const [id, w, h, z] of [['elf', 14, 24, 3], ['dog', 20, 13, 1], ['angel', 16, 18, 0], ['molly', 17, 14, 0]]) {
      const m = this[id]; if (m && !m.leaving) { const o = this.memberOrigin(m); add(id, o.ox, o.oy, w, h, z); }
    }
    for (const m of this.heroes.values()) if (!m.leaving) { const o = this.memberOrigin(m); add(m.id, o.ox, o.oy, m.hero.w, m.hero.h, [0.5, 0.7, 2][m.lane || 0]); }
    const hits = boxes.filter((b) => x >= b.ox - 1 && x <= b.ox + b.w + 1 && y >= b.oy - 2 && y <= b.oy + b.h + 1).sort((a, b) => b.z - a.z);
    if (!hits.length) return null;
    const id = hits[0].id, m = this.heroes.get(id);
    return m ? { id, name: m.name, label: m.hero.label } : { id };
  }

  aimArrow(o, tx, ty, st, delay, pierce) {
    const g = 90, v = pierce ? 300 : 200, T = Math.max(0.2, (tx - o.x) / v);
    return { x: o.x, y: o.y, vx: (tx - o.x) / T, vy: (ty - o.y) / T - 0.5 * g * T, g: pierce ? 0 : g, s: st, delay, pierce, hit: false, t: 0 };
  }

  updateMissiles(dt) {
    const box = this.creatureBox();
    for (const a of this.arrows) {
      if ((a.delay -= dt) > 0) continue;
      if (a.pierce && a.vy) { a.vy = 0; }
      a.t += dt; a.vy += a.g * dt; a.x += a.vx * dt; a.y += a.vy * dt;
      if (a.kind === 'bolt' && Math.random() < 0.7) this.particles.push({ x: a.x - 2, y: a.y, vx: -15, vy: (Math.random() - 0.5) * 10, life: 0.25, g: 0, c: hex32(a.c) });
      if (a.pierce && Math.random() < 0.8) this.particles.push({ x: a.x - 6, y: a.y, vx: -20, vy: (Math.random() - 0.5) * 20, life: 0.3, g: 0, c: hex32(Math.random() < 0.5 ? 0xb8ffb0 : 0xffffff) });
      if (!a.hit && box && a.x >= box.x + 2 && a.y >= box.y - 4 && a.y <= box.y + box.h + 2) {
        a.hit = true;
        if (this.creature) Object.assign(this.creature, { hitT: 0.25, hitS: a.s, knock: (a.pierce ? 6 : 1) + a.s * 3 + (a.size || a.r || 0) * 0.5 });
        for (let i = 0; i < (a.pierce ? 18 : 5); i++) this.particles.push({ x: a.x, y: a.y, vx: (Math.random() - 0.3) * 80, vy: (Math.random() - 0.7) * 60, life: 0.3, c: hex32(i % 2 ? (a.c ?? 0x7dff7a) : 0xffffff) });
        if (a.pierce) { this.flashT = Math.max(this.flashT, 0.06); this.shakeT = Math.max(this.shakeT, 0.1); }
        this.emit('impact', a.s);
        if (!a.pierce) a.done = true;
      }
      if (a.y > this.floorY + 14 || a.x > this.W + 10) a.done = true;
    }
    this.arrows = this.arrows.filter((a) => !a.done);
    for (const b of this.barks) {
      b.t += dt;
      if (b.kind === 'bark') b.x += 150 * dt;
      else if (b.t > 0.25) { // shadow ball: hovers at the mouth, then flies
        const dx = b.tx - b.x, dy = b.ty - b.y, d = Math.hypot(dx, dy) || 1, sp = (90 + b.s * 60) * dt;
        b.x += (dx / d) * Math.min(sp, d); b.y += (dy / d) * Math.min(sp, d);
        if (Math.random() < 0.9) this.particles.push({ x: b.x + (Math.random() - 0.5) * b.r * 2, y: b.y + (Math.random() - 0.5) * b.r * 2, vx: -30, vy: (Math.random() - 0.5) * 15, life: 0.4, g: -10, c: hex32(Math.random() < 0.6 ? 0x05020c : 0x6a2fb0) });
      }
      if (box && b.x >= (b.kind === 'bark' ? box.x : b.tx - 1)) {
        b.done = true;
        if (b.kind === 'bark') {
          if (this.creature) Object.assign(this.creature, { hitT: 0.2, hitS: b.s, knock: 1 + b.s * 3 });
        } else {
          if (this.creature) Object.assign(this.creature, { hitT: 0.4, hitS: b.s, knock: 4 + b.s * 10, airT: 0.5, airH: 3 + b.s * 10 });
          this.shakeT = Math.max(this.shakeT, 0.1 + b.s * 0.25);
          for (let i = 0; i < 16 + Math.round(b.s * 30); i++) {
            const a = Math.random() * Math.PI * 2, v = 20 + Math.random() * (40 + b.s * 80);
            this.particles.push({ x: b.x, y: b.y, vx: Math.cos(a) * v, vy: Math.sin(a) * v, life: 0.4 + Math.random() * 0.4, g: 0, c: hex32(i % 3 ? 0x05020c : i % 2 ? 0x8a3fd1 : 0xd14fb8) });
          }
        }
        this.emit('impact', b.s);
      }
      if (!box && b.x > this.W) b.done = true;
    }
    this.barks = this.barks.filter((b) => !b.done);
  }

  drawMissiles() {
    for (const a of this.arrows) {
      if (a.delay > 0) continue;
      if (a.kind === 'slash') { // crescent of light
        const r = Math.round(a.size), c = hex32(a.c);
        for (let j = -r; j <= r; j++) { const x = a.x + Math.round(Math.sqrt(r * r - j * j) * 0.7); this.px(x, a.y + j, this.white); this.px(x - 1, a.y + j, c); if (Math.abs(j) < r - 1) this.px(x - 2, a.y + j, c); }
        continue;
      }
      if (a.kind === 'bolt') {
        const r = Math.max(1, Math.round(a.r)), c = hex32(a.c);
        for (let dy = -r; dy <= r; dy++) for (let dx = -r; dx <= r; dx++) if (dx * dx + dy * dy <= r * r) this.px(a.x + dx, a.y + dy, dx * dx + dy * dy <= (r - 1) * (r - 1) ? this.white : c);
        continue;
      }
      if (a.kind === 'dagger') { // spinning
        const ang = a.t * 22, ux = Math.cos(ang), uy = Math.sin(ang);
        for (let i = -2; i <= 2; i++) this.px(a.x + ux * i, a.y + uy * i, i < 0 ? hex32(0x7a4a22) : hex32(0xdfe6f0));
        continue;
      }
      const len = a.pierce ? 9 : 6, sp = Math.hypot(a.vx, a.vy) || 1, ux = a.vx / sp, uy = a.vy / sp;
      const glow = a.pierce ? hex32((Math.floor(this.t * 16) % 2) ? 0xb8ffb0 : 0xffffff) : null;
      for (let i = 0; i < len; i++) { this.px(a.x - ux * i, a.y - uy * i, i === 0 ? this.white : glow || hex32(0xf0d8a0)); this.px(a.x - ux * i, a.y - uy * i + 1, i === 0 ? this.white : glow || hex32(0x9a6233)); }
      this.px(a.x - ux * (len - 1), a.y - uy * (len - 1) - 1, hex32(0x7dff7a)); this.px(a.x - ux * (len - 1), a.y - uy * (len - 1) + 1, hex32(0x7dff7a)); // fletching
    }
    for (const b of this.barks) {
      if (b.kind === 'bark') { // ")))" sound waves
        const fade = Math.min(1, b.t * 4);
        for (let k = 0; k < 3; k++) {
          const r = 3 + k * 3 + Math.round(b.s * 3), c = k === 0 ? this.white : hex32(k === 1 ? 0xd14fb8 : 0x3fa0ff);
          for (let j = -r; j <= r; j++) {
            const x = b.x - k * 5 + Math.round(Math.sqrt(Math.max(0, r * r - j * j)) * 0.6);
            if (bayer(x, b.y + j) < fade + 0.3) { this.px(x, b.y + j, c); this.px(x - 1, b.y + j, c); }
          }
        }
      } else { // black shadow ball with a pulsing purple rim
        const r = Math.round(b.r * Math.min(1, b.t / 0.25 + 0.3)), pulse = (Math.sin(this.t * 14) + 1) / 2;
        for (let dy = -r - 1; dy <= r + 1; dy++) for (let dx = -r - 1; dx <= r + 1; dx++) {
          const d = Math.hypot(dx, dy);
          if (d <= r - 1) this.px(b.x + dx, b.y + dy, hex32(d < r * 0.4 && pulse > 0.7 ? 0x1a0b3d : 0x05020c));
          else if (d <= r) this.px(b.x + dx, b.y + dy, hex32(pulse > 0.5 ? 0x8a3fd1 : 0x4b2aa8));
          else if (d <= r + 1 && bayer(b.x + dx, b.y + dy) < 0.4) this.px(b.x + dx, b.y + dy, hex32(0xd14fb8));
        }
      }
    }
  }

  // Back lanes (0, 1) draw behind the front line; the near lane (2) in front.
  sortedHeroes(front) { return [...this.heroes.values()].filter((m) => ((m.lane || 0) === 2) === !!front).sort((a, b) => (a.lane || 0) - (b.lane || 0) || a.x - b.x); }

  drawBeams() {
    for (const b of this.beams) {
      const gold = hex32(b.c ?? 0xffe9a8), soft = hex32(b.c === 0x9cff5a ? 0xd8ffc0 : 0xfff6d8);
      const fade = 1 - b.t / b.life;
      if (b.kind === 'ray') {
        const w = 1 + Math.round(b.s * 2), steps = Math.ceil(Math.hypot(b.x1 - b.x0, b.y1 - b.y0));
        for (let i = 0; i <= steps; i++) {
          const x = b.x0 + ((b.x1 - b.x0) * i) / steps, y = b.y0 + ((b.y1 - b.y0) * i) / steps;
          for (let j = -w; j <= w; j++) if (bayer(x, y + j) < fade + 0.2) this.px(x, y + j, Math.abs(j) < w ? this.white : gold);
        }
      } else { // pillar from the ceiling onto the creature
        const half = Math.round((2 + b.s * 12) * Math.min(1, b.t / 0.15 + 0.2));
        const top = this.ceil ?? 0, bot = this.floorY + 12;
        for (let y = top; y <= bot; y++) for (let dx = -half - 2; dx <= half + 2; dx++) {
          const x = b.x + dx, edge = Math.abs(dx) > half;
          if (edge ? bayer(x, y + Math.floor(this.t * 30)) < 0.35 * fade : bayer(x, y) < fade + 0.25) this.px(x, y, edge ? gold : Math.abs(dx) < half * 0.5 ? this.white : soft);
        }
      }
    }
  }

  // Kaido's angel and Molly: wings beat, halo glows, light gathers before
  // each ray.
  drawFlyer(m) {
    if (!m) return;
    const f = FLYERS[m.id];
    let { ox, oy } = this.memberOrigin(m);
    if (!f.k) f.k = Object.fromEntries(Object.entries(f.keys).map(([k, v]) => [k, hex32(v)]));
    const K = f.k, w = f.rows[0].length;
    const hurtFlash = m.hurtT > 0 && Math.floor(this.t * 20) % 2;
    if (m.hurtT > 0) ox -= Math.round((m.hurtT / 0.4) * 3);
    const flap = Math.floor(m.t * 4) % 2, blink = m.blinkT < 0;
    f.rows.forEach((row, y) => [...row].forEach((ch, x) => {
      if (ch === '.') return;
      const wing = f.wings && (ch === 'W' || ch === 'w') && (x < 3 || x > w - 4);
      const c = hurtFlash && ch !== 'K' ? this.white : blink && ch === 'E' ? K.S : K[ch];
      this.px(ox + x, oy + y + (wing && flap ? 1 : 0), c); // wing beat
    }));
    if (f.halo != null) {
      const hx = ox + f.halo, glow = (Math.sin(this.t * 3) + 1) / 2;
      for (let x = hx + 1; x <= hx + 4; x++) { this.px(x, oy - 3, K.Y); this.px(x, oy - 1, K.Y); }
      this.px(hx, oy - 2, K.Y); this.px(hx + 5, oy - 2, K.Y);
      if (glow > 0.6) { this.px(hx + 2, oy - 4, this.white); this.px(hx + 3, oy - 4, this.white); }
    }
    const acting = m.state === 'ray' || m.state === 'big';
    const charging = acting && m.p < 0.35;
    if (charging) for (let k = 0; k < 6; k++) { const a = k * 1.05 + this.t * 10, d = (m.state === 'big' ? 6 : 3) * (1 - m.p / 0.35) + 1; this.px(ox + f.hand.x + Math.cos(a) * d, oy + f.hand.y + Math.sin(a) * d, k % 2 ? this.white : hex32(f.beam)); }
    if (m.confused && m.state !== 'enter') this.question(ox + 4, oy - 15);
  }

  drawElf() {
    const m = this.elf;
    if (!m) return;
    let { ox, oy } = this.memberOrigin(m);
    const K = this.ek || (this.ek = Object.fromEntries(Object.entries(ELF_KEYS).map(([k, v]) => [k, hex32(v)])));
    const bob = m.walking ? Math.floor(m.t * 8) % 2 : (m.state === 'idle' ? Math.floor(m.t * 1.4) % 2 : 0);
    const hurtFlash = m.hurtT > 0 && Math.floor(this.t * 20) % 2;
    if (m.hurtT > 0) ox -= Math.round((m.hurtT / 0.4) * 3);
    oy -= bob;
    const blink = m.blinkT < 0;
    this.sprite(ELF_BODY, ox, oy, (ch, x, y) => {
      if (hurtFlash && ch !== 'K') return this.white;
      if (blink && ch === 'E') return K.S;
      return K[ch] ?? this.keys.K;
    });
    // Hair sways behind her.
    if (Math.floor(m.t * 1.2) % 2) this.px(ox + 2, oy + 12, K.y); else this.px(ox + 2, oy + 13, K.y);
    // Bow in her front hand; the string pulls back while she draws.
    const hx = ox + 11, hy = oy + 10;
    const draw = (m.state === 'shoot' || m.state === 'big') && m.p < 0.6 ? Math.min(1, m.p / 0.4) : 0;
    for (let j = -8; j <= 8; j++) this.px(hx + 2 + Math.round(3 * Math.cos((j / 8) * Math.PI / 2)), hy + j, j % 4 ? K.b : K.B);
    const pull = Math.round(draw * 5);
    for (let j = -8; j <= 8; j++) { const sx = hx + 2 - Math.round(pull * (1 - Math.abs(j) / 8)); this.px(sx, hy + j, hex32(0xe8e0c8)); }
    if (draw > 0) { // nocked arrow
      for (let i = 0; i < 8; i++) this.px(hx + 2 - pull + i, hy, i === 7 ? this.white : K.b);
      if (m.state === 'big' && (m.power ?? 0) > 0.7) for (let k = 0; k < 4; k++) { const t = k * 1.6 + this.t * 10; this.px(hx + 6 + Math.cos(t) * 3, hy + Math.sin(t) * 3, hex32(0xb8ffb0)); }
    }
    if (m.confused && m.state !== 'enter') this.question(ox + 3, oy - 12);
  }

  drawDog() {
    const m = this.dog;
    if (!m) return;
    let { ox, oy } = this.memberOrigin(m);
    const bob = m.walking ? Math.floor(m.t * 10) % 2 : (m.state === 'idle' ? Math.floor(m.t * 1.8) % 2 : 0);
    const hurtFlash = m.hurtT > 0 && Math.floor(this.t * 20) % 2;
    if (m.hurtT > 0) ox -= Math.round((m.hurtT / 0.4) * 3);
    const barking = (m.state === 'bark' && m.p > 0.2 && m.p < 0.7) || (m.state === 'big' && m.p > 0.1 && m.p < 0.8);
    const t = this.t, frame = Math.floor(t * 2);
    const legStep = m.walking && Math.floor(m.t * 8) % 2;
    // Body breathes (rises a pixel); legs stay planted and trot when walking.
    const col = (ch, x, y) => {
      if (hurtFlash && ch !== 'K') return this.white;
      if (ch === 'K') return this.keys.K;
      if (ch === 'E') return this.white;
      return this.nebula(ox + x, oy + y, t, frame);
    };
    this.sprite(DOG_BODY.slice(0, 10), ox, oy - bob, col);
    this.sprite(DOG_BODY.slice(10), ox, oy + 10, (ch, x, y) => (legStep && x > 6 && x < 11 && y < 2 ? undefined : col(ch, x, y + 10)));
    if (barking) { this.px(ox + 19, oy + 6 - bob, this.keys.K); this.px(ox + 18, oy + 6 - bob, hex32(0xd14fb8)); } // open mouth
    if (m.state === 'big' && m.p < 0.7) { // shadow gathering at the mouth
      const r = Math.round(1 + m.p * 6 * (0.5 + (m.power ?? 0.5)));
      for (let k = 0; k < 8; k++) { const a = k * 0.8 + t * 8, d = r + 4 - (t * 20 + k) % 4; this.px(ox + 22 + Math.cos(a) * d, oy + 5 + Math.sin(a) * d, hex32(k % 2 ? 0x05020c : 0x8a3fd1)); }
      for (let dy = -r; dy <= r; dy++) for (let dx = -r; dx <= r; dx++) if (dx * dx + dy * dy <= r * r) this.px(ox + 22 + dx, oy + 5 + dy, hex32(0x05020c));
    }
    if (m.confused && m.state !== 'enter') this.question(ox + 12, oy - 13);
  }
  // Night sky: drifting nebula colours with twinkling stars.
  nebula(x, y, t, frame) {
    const h = ((x * 73856093) ^ (y * 19349663) ^ (frame * 83492791)) >>> 0;
    if (h % 23 === 0) return this.white;
    if (h % 31 === 0) return hex32(0x9ff0ff);
    const v = Math.sin(x * 0.55 + t * 1.3) + Math.sin(y * 0.8 - t * 0.9) + Math.sin((x + y) * 0.35 + t * 0.6);
    const nb = this.nb || (this.nb = NEBULA.map(hex32));
    return nb[Math.max(0, Math.min(5, Math.floor((v + 3) / 6 * 6 + (bayer(x, y) - 0.5))))];
  }

  // A hammer swing on a share: hits the creature if it is in reach; bigger
  // shares knock it back further with more sparks.
  meleeHit(x, y, power) {
    const pw = Math.max(0, Math.min(1, power));
    const box = this.creatureBox();
    const inReach = box && x >= box.x - 6 && x <= box.x + box.w + 6;
    if (inReach && this.creature) Object.assign(this.creature, { hitT: 0.3, hitS: pw, knock: 2 + pw * 7 });
    const n = 5 + Math.round(pw * 18);
    for (let i = 0; i < n; i++) {
      const a = Math.random() * Math.PI * 2, v = 25 + Math.random() * (30 + pw * 70);
      this.particles.push({ x, y, vx: Math.cos(a) * v, vy: Math.sin(a) * v - 15, life: 0.25 + Math.random() * 0.35, c: hex32(i % 3 ? 0x9ff0ff : 0xffffff) });
    }
    if (pw > 0.7) this.shakeT = Math.max(this.shakeT, 0.12);
    this.emit('impact', pw);
  }

  // ---------- the creature fights back ----------
  // Where a party member is (screen centre), for the creature to aim at.
  memberCenter(who) {
    if (who.startsWith('hero:') && this.heroes.get(who)) { const m = this.heroes.get(who), o = this.memberOrigin(m); return { x: o.ox + 6, y: o.oy + Math.round(m.hero.h / 2) }; }
    if (who === 'dwarf' && this.dwarf) { const o = this.dwarfOrigin(); return { x: o.ox + 10, y: o.oy + 12 }; }
    if ((who === 'elf' || who === 'dog' || FLYERS[who]) && this[who]) { const o = this.memberOrigin(this[who]); return who === 'elf' ? { x: o.ox + 7, y: o.oy + 12 } : who === 'dog' ? { x: o.ox + 10, y: o.oy + 7 } : { x: o.ox + 8, y: o.oy + 9 }; }
    const g = this.pose(); return { x: g.ox + 8, y: g.oy + 13 };
  }

  updateCreatureAttacks(dt) {
    const cr = this.creature;
    for (const e of this.enemyShots) {
      const tgt = this.memberCenter(e.target);
      const dx = tgt.x - e.x, dy = tgt.y - e.y, d = Math.hypot(dx, dy), sp = 130 * dt;
      e.t += dt;
      if (d <= sp + 1) { e.done = true; this.hurtMember(e.target, e.kind); continue; }
      e.x += (dx / d) * sp; e.y += (dy / d) * sp - (e.kind === 'rock' ? Math.cos(e.t * 4) * 0.8 : 0);
    }
    this.enemyShots = this.enemyShots.filter((e) => !e.done);
    if (!cr || cr.leaving || cr.tier < 0 || cr.tier > LEGENDARY || this.congrats || this.isDigging()) return;
    if (cr.atk) {
      const a = cr.atk;
      a.t += dt;
      const p = a.t / a.dur;
      if (a.kind === 'lunge') { cr.lungeX = -(a.dist + 8) * Math.sin(Math.min(1, p) * Math.PI); cr.lungeY = Math.round(Math.sin(Math.min(1, p) * Math.PI) * 6); }
      if (a.kind === 'breath' && p < 0.8) { // stream of fire toward the target
        const box = this.creatureBox(), tgt = this.memberCenter(a.target);
        for (let k = 0; k < 3; k++) {
          const mx = box.x + 2, my = box.y + box.h * 0.3, vx = (tgt.x - mx) * (1.6 + Math.random() * 0.4), vy = (tgt.y - my) * 1.6 + (Math.random() - 0.5) * 30;
          this.particles.push({ x: mx, y: my, vx, vy, life: 0.55, g: -20, c: this.fireColor(0.7, Math.floor(Math.random() * 4)) });
        }
      }
      if (!a.hit && p >= 0.5) {
        a.hit = true;
        if (a.kind === 'lunge' || a.kind === 'breath') this.hurtMember(a.target, a.kind);
        else if (a.kind === 'pound') { cr.airT = 0; this.waves.push({ x: this.creatureBox().x, r: 2, hitDone: true, pw: 0.5, speed: 120, h: 4, reach: this.W, enemy: true, hurt: {} }); this.shakeT = 0.15; }
        else { const box = this.creatureBox(); this.enemyShots.push({ x: box.x, y: box.y + box.h * 0.4, target: a.target, kind: a.kind, t: 0, tier: cr.tier }); }
      }
      if (p >= 1) { cr.atk = null; cr.lungeX = 0; cr.lungeY = 0; }
      return;
    }
    if ((cr.atkT = (cr.atkT ?? 2 + Math.random() * 2) - dt) > 0) return;
    cr.atkT = 4.5 - cr.tier * 0.5 + Math.random() * 2.5;
    const here = this.presentMembers();
    const front = here.find((id) => id === 'dwarf') || here.find((id) => id === 'dog') || here[0] || 'wizard';
    const target = Math.random() < 0.6 ? front : [...here, 'wizard'][Math.floor(Math.random() * (here.length + 1))];
    const box = this.creatureBox(), tc = this.memberCenter(target);
    const dist = box ? box.x - (tc.x + 10) : 99;
    let kind;
    if (cr.tier === LEGENDARY) kind = 'breath';
    else if (cr.tier === 3) kind = 'pound';
    else if (dist < 45) kind = 'lunge';
    else kind = cr.tier === 2 ? 'rock' : 'spit';
    if (kind === 'pound') cr.airT = 0.5;
    cr.atk = { kind, target, t: 0, dur: kind === 'breath' ? 1.1 : kind === 'pound' ? 0.6 : 0.6, dist: Math.max(0, dist), hit: false };
  }

  hurtMember(who, kind) {
    const c = this.memberCenter(who);
    if (who !== 'wizard' && this.member(who)) this.member(who).hurtT = 0.4; else this.wiz.hurtT = 0.4;
    const col = kind === 'breath' ? 0xff7a1f : kind === 'rock' ? 0x8a8f99 : 0xff4d6d;
    for (let i = 0; i < 10; i++) {
      const a = Math.random() * Math.PI * 2, v = 20 + Math.random() * 50;
      this.particles.push({ x: c.x, y: c.y, vx: Math.cos(a) * v, vy: Math.sin(a) * v - 10, life: 0.3 + Math.random() * 0.3, c: hex32(i % 2 ? col : 0xffffff) });
    }
    this.emit('hurt', who);
  }

  drawEnemyShots() {
    for (const e of this.enemyShots) {
      const pulse = Math.floor(this.t * 12) % 2;
      if (e.kind === 'rock') { // a spinning boulder
        const rot = Math.floor(e.t * 10) % 2;
        for (let dy = -3; dy <= 3; dy++) for (let dx = -3; dx <= 3; dx++) {
          const d = Math.abs(dx) + Math.abs(dy);
          if (d > 4) continue;
          this.px(e.x + dx, e.y + dy, d === 4 ? this.keys.K : hex32((dx + dy + rot) & 1 ? 0xb8b0a0 : 0x7a7266));
        }
        this.px(e.x + 4, e.y - 1, hex32(0x7a7266)); this.px(e.x + 6, e.y + 1, hex32(0x7a7266));
      } else { // spit: a glowing glob with a dripping trail
        const col = hex32(0x9cff5a), dark = hex32(0x3f9a2a);
        for (let dy = -2; dy <= 2; dy++) for (let dx = -2; dx <= 2; dx++) {
          const d = dx * dx + dy * dy;
          if (d <= 5) this.px(e.x + dx, e.y + dy, d >= 4 ? this.keys.K : d === 0 && pulse ? this.white : dy > 0 ? dark : col);
        }
        for (let k = 1; k <= 3; k++) this.px(e.x + 2 + k * 2, e.y + (k & 1), k === 3 ? dark : col);
      }
    }
  }

  // Bigger shares: taller, faster, longer shockwave, more sparks and shake.
  slamImpact(x, power) {
    const pw = Math.max(0, Math.min(1, power));
    const y = this.floorY + 12;
    this.waves.push({ x, r: 2, hitDone: false, pw, speed: 70 + pw * 110, h: 3 + pw * 7, reach: this.W * (0.35 + pw * 0.65) });
    this.shakeT = 0.08 + pw * 0.3;
    if (pw > 0.6) this.flashT = Math.max(this.flashT, 0.08);
    const n = 10 + Math.round(pw * 30);
    for (let i = 0; i < n; i++) {
      const a = Math.PI + Math.random() * Math.PI, v = 25 + Math.random() * (40 + pw * 70);
      this.particles.push({ x, y: y - 1, vx: Math.cos(a) * v, vy: Math.sin(a) * v, life: 0.35 + Math.random() * 0.5,
        c: i % 4 === 0 ? this.pal[7] : hex32(i % 3 ? 0x9ff0ff : 0xffffff) });
    }
    this.emit('slam', pw);
  }

  drawWaves() {
    const y = this.floorY + 12;
    for (const w of this.waves) {
      if (w.enemy) { // the creature's ground pound rolls toward the party
        for (const who of [...this.presentMembers().filter((id) => !FLYERS[id]), 'wizard']) {
          const c = this.memberCenter(who);
          if (!w.hurt[who] && w.x - w.r <= c.x) { w.hurt[who] = true; if (!(who === 'wizard' && this.inGroup() && this.wiz.x - this.cam <= this.rockX() + 1)) this.hurtMember(who, 'pound'); }
        }
      }
      const box = this.creatureBox();
      if (box && !w.hitDone && w.x + w.r >= box.x + box.w / 2 && w.r <= (w.reach || this.W)) {
        w.hitDone = true;
        if (this.creature) { const pw = w.pw ?? 0.6; Object.assign(this.creature, { airT: 0.55, airH: 4 + pw * 14, hitT: 0.3, hitS: pw, knock: 1 + pw * 4 }); }
      }
      const fade = Math.max(0, 1 - w.r / (w.reach || this.W * 0.9));
      for (const side of [-1, 1]) for (let k = 0; k < 6; k++) {
        const x = w.x + side * (w.r - k), h = Math.round(((w.h || 8) - k) * fade);
        for (let j = 0; j <= h; j++) this.px(x, y - j, j === h ? this.white : this.pal[GLOW + ((k * 3 + j + Math.floor(this.t * 20)) & 15)]);
      }
      // Glowing crack along the floor where it passed.
      for (let x = -w.r; x <= w.r; x += 2) if (bayer(w.x + x, y) < 0.5 * fade) this.px(w.x + x, y + 1, this.pal[GLOW + ((x + Math.floor(this.t * 12)) & 15)]);
    }
    this.waves = this.waves.filter((w) => w.r < (w.reach || this.W));
  }

  drawDwarf() {
    const d = this.dwarf;
    if (!d) return;
    let { ox, oy } = this.dwarfOrigin();
    const K = this.dk || (this.dk = Object.fromEntries(Object.entries(DWARF_KEYS).map(([k, v]) => [k, hex32(v)])));
    const pulse = (Math.sin(this.t * 2.2) + 1) / 2;
    const charged = d.state === 'slam' && d.slamP > 0.2 && d.slamP < 0.6;
    const swinging = d.state === 'swing' && d.swingP > 0.3 && d.swingP < 0.45;
    const rune = hex32(RUNE_GLOW[Math.min(4, Math.floor((charged ? 1 : pulse * 0.75) * 4.99))]);
    const bob = d.walking ? Math.floor(d.t * 8) % 2 : (d.state === 'idle' ? Math.floor(d.t * 1.6) % 2 : 0); // walk / breathing
    const blink = d.blinkT < 0;
    const hurt = d.hurtT > 0, hurtFlash = hurt && Math.floor(this.t * 20) % 2;
    const kx = hurt ? -Math.round((d.hurtT / 0.4) * 3) : 0;
    const ox0 = ox; ox += kx;
    this.sprite(DWARF_BODY, ox, oy - bob, (ch, x, y) => {
      if (hurtFlash && ch !== 'K') return this.white;
      if (ch === 'G') return rune;
      if (y === 6 && ch === 'E') return K.W; // eyes look right, at the creature
      if (y === 6 && ch === 'W') return K.E;
      if (blink && y === 6 && (ch === 'E' || ch === 'W')) return K.S;
      return K[ch];
    });
    if (Math.floor(d.t * 0.7) % 3 === 0) { this.px(ox + 8, oy - bob + 16, K.R); this.px(ox + 11, oy - bob + 16, K.R); } // beard sway
    // Hammer: pivot in the right hand.
    const a = this.dwarfAngle();
    const hx = ox + 18, hy = oy + 15 - bob, L = 18; void ox0;
    const dx = Math.cos(a), dy = Math.sin(a), pxv = -dy, pyv = dx;
    const ex = hx + dx * L, ey = hy + dy * L;
    if ((d.state === 'slam' && d.slamP > 0.35 && d.slamP < 0.5) || swinging) { // swing trail
      for (let k = 1; k <= 4; k++) { const t = a - k * 0.3; for (let j = -3; j <= 3; j++) if (bayer(k, j) < 0.6) this.px(hx + Math.cos(t) * L - Math.sin(t) * j, hy + Math.sin(t) * L + Math.cos(t) * j, this.pal[GLOW + ((k * 4) & 15)]); }
    }
    for (let i = 0; i <= L - 3; i++) this.px(hx + dx * i, hy + dy * i, i % 3 ? K.B : K.b);
    for (let u = -3; u <= 3; u += 0.5) for (let v = -4; v <= 4; v += 0.5) {
      const edge = Math.abs(u) >= 3 || Math.abs(v) >= 4;
      const r = Math.abs(u) <= 1 && Math.abs(v) <= 1;
      this.px(ex + dx * u + pxv * v, ey + dy * u + pyv * v, edge ? K.K : r ? rune : (u < 0 ? K.H : K.h));
    }
    if (d.confused && d.state !== 'drop') this.question(ox + 6, oy - 12);
    if (charged) for (let k = 0; k < 6; k++) { const t = k * 1.05 + this.t * 9; this.px(ex + Math.cos(t) * 7, ey + Math.sin(t) * 6, k % 2 ? this.white : hex32(0x39d0ff)); }
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
    const digging = this.isDigging();
    const feet = this.floorY + 12;
    let oy = feet - 26, ox = Math.round(w.x - this.cam), legs = 'stand';
    if (this.inGroup() && !digging && Math.abs(ox - this.rockX()) <= 1) oy -= ROCK_H; // standing on the rock
    // Pickaxe only while digging out a found block; otherwise the staff.
    let hand = { x: 13, y: 17 }, angle = digging ? 1.3 : -1.45, pick = digging, staff = !digging, arms2 = null, gem = false;
    switch (w.state) {
      case 'walk': {
        const f = Math.floor(w.walkT * 8) % 4;
        legs = f === 1 ? 'walk1' : f === 3 ? 'walk3' : 'stand';
        oy -= f % 2;
        hand = { x: 11, y: 15 }; angle = digging ? -2.25 : -1.25;
        break;
      }
      case 'cast': {
        // Staff swings back, then thrusts toward the creature.
        const p = w.swingP;
        if (p < 0.4) angle = -1.45 - (p / 0.4) * 0.8;
        else if (p < 0.55) angle = -2.25 + ((p - 0.4) / 0.15) * 1.75;
        else if (p < 0.8) angle = -0.5;
        else angle = -0.5 - ((p - 0.8) / 0.2) * 0.95;
        hand = { x: SHOULDER.x + 1 + Math.round(Math.cos(angle) * 4), y: SHOULDER.y + 1 + Math.round(Math.sin(angle) * 3) };
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
        hand = { x: 12, y: 7 }; pick = false; staff = false; gem = true;
        break;
      case 'sleep':
        oy += 1;
        break;
      default:
        oy -= Math.floor(t * 1.5) % 2 === 0 ? 0 : 0;
    }
    // Breathing while standing, so he never looks frozen; knocked back when hit.
    if (w.state === 'idle' || w.state === 'confused') oy += Math.floor(t * 1.5) % 2;
    if (w.hurtT > 0) ox -= Math.round((w.hurtT / 0.4) * 3);
    // Facing left mirrors the sprite, hand and angle.
    if (w.face < 0) {
      hand = { x: 15 - hand.x, y: hand.y };
      angle = Math.PI - angle;
      if (arms2) arms2 = { x: 15 - arms2.x, y: arms2.y };
    }
    return { ox, oy, legs, hand, angle, pick, staff, arms2, gem, face: w.face };
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
    this.drawTrophyWall();
    this.drawRock();
    this.drawMist();
    this.drawFlyer(this.angel);
    this.drawFlyer(this.molly);
    for (const m of this.sortedHeroes(0)) this.drawHero(m);
    this.drawDog();
    this.drawDwarf();
    this.drawWizard();
    this.drawParticles();
    this.drawDigging();
    this.drawWaves();
    this.drawFireballs(true);
    this.drawCreature();
    this.drawEnemyShots();
    this.drawFireballs();
    this.drawMissiles();
    this.drawBeams();
    this.drawElf();
    for (const m of this.sortedHeroes(1)) this.drawHero(m);
    this.drawCongrats();
    if (this.flashT > 0) this.flash(this.flashT / 0.35);
    const sh = this.shakeT > 0 ? (Math.floor(this.t * 40) % 2 ? 1 : -1) : 0;
    this.ctx.putImageData(this.img, sh, 0);
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

  // Matured blocks sit directly on the back cave wall, oldest first: the
  // first at the bottom-left at ground level, each new one stacked on top
  // of the previous, and a new column to the right when a column reaches
  // the top of the usable wall. Each spot shows its mounted head or the gem.
  // They are part of the back wall: timber, rock and characters stay in
  // front (pixels are drawn only where the back wall shows).
  drawTrophyWall() {
    const items = this.trophies;
    if (!items || !items.length) return;
    const cellW = 18, cellH = 16, left = 3;
    const bottom = this.floorY - 1, top = (this.ceil || 10) + 9;
    const rows = Math.max(1, Math.floor((bottom - top) / cellH));
    const K = this.keys.K, pal = this.pal;
    const { W, mid, fg } = this;
    const mod = (v, m) => ((Math.floor(v) % m) + m) % m;
    const midOff = mod(this.cam * 0.6, MID_W), fgOff = mod(this.cam * 1.6, FG_W);
    const wallPx = (x, y, c) => {
      x |= 0; y |= 0;
      if (x < 0 || y < 0 || x >= W || y >= this.floorY) return;
      if (mid[y * MID_W + ((midOff + x) % MID_W)] || fg[y * FG_W + ((fgOff + x) % FG_W)]) return;
      this.buf[y * W + x] = c;
    };
    const sprite = (rows2, ox, oy, colorOf) => {
      for (let y = 0; y < rows2.length; y++) for (let x = 0; x < rows2[y].length; x++) {
        const ch = rows2[y][x];
        if (ch === '.') continue;
        const c = colorOf(ch, x, y);
        if (c !== undefined) wallPx(ox + x, oy + y, c);
      }
    };
    const oldestFirst = [...items].reverse();
    oldestFirst.forEach((it, i) => {
      const col = Math.floor(i / rows), row = i % rows;
      const sx = left + col * cellW, sy = bottom - (row + 1) * cellH;
      if (sx + cellW > W) return;
      const cx = sx + cellW / 2;
      if (it.head != null && it.head >= 0 && it.head <= LEGENDARY) {
        // Shield plaque, then the head.
        for (let y = 0; y < 14; y++) for (let x = 0; x < 16; x++) {
          const dx = (x - 7.5) / 8, dy = (y - 5) / (y < 5 ? 6 : 9);
          const d = dx * dx + dy * dy;
          if (d <= 1) wallPx(cx - 8 + x, sy + y, d > 0.72 ? pal[12] : pal[9]);
        }
        const tier = it.head, base = creatureBody(tier, it.species, creatureColor(tier, it.variant));
        const C = hex32(base), c = hex32((base >> 1) & 0x7f7f7f);
        const dragon = tier === LEGENDARY && creatureSprite(tier, it.species).name === 'Dragon';
        const rowsS = dragon ? DRAGON_HEAD : creatureHead(tier, it.species);
        const w = rowsS[0].length, keys = this.creatureKeys();
        sprite(rowsS, Math.round(cx - w / 2), sy + 1 + (rowsS.length < 10 ? 2 : 0),
          (ch, x, y) => (ch === 'C' ? C : ch === 'c' ? c : ch === 'K' ? K : ch === 'R' ? pal[RAIN + ((x + y) & 15)] : keys[ch]));
      } else {
        const pulse = Math.floor(this.t * 4);
        sprite(TROPHY, Math.round(cx - 3.5), sy + 4, (ch, x, y) => {
          if (ch === 'Y') return this.gold[0];
          const colr = pal[RAIN + ((x + y + i + pulse) & 15)];
          return ch === 'g' ? pal[GLOW + ((x + y + i) & 15)] : colr;
        });
      }
    });
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
    const hurtFlash = this.wiz.hurtT > 0 && Math.floor(this.t * 20) % 2;
    const colorOf = (ch, x, y) => {
      if (hurtFlash && ch !== 'K') return this.white;
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
    if (g.staff) this.drawStaff(g.ox + g.hand.x, g.oy + g.hand.y, g.angle);
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
    if (this.wiz.confused) this.question(g.ox + (mirror ? -8 : 14), g.oy - 12);
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
    if (!cr || cr.tier < 0 || cr.tier > LEGENDARY) return;
    const tier = cr.tier;
    const dragon = tier === LEGENDARY;
    const scale = tier >= 2 ? 2 : 1;
    const base = creatureBody(tier, cr.species, creatureColor(tier, cr.variant));
    const C = hex32(base), c = hex32(((base >> 1) & 0x7f7f7f));
    const rows = creatureSprite(tier, cr.species).rows, keys = this.creatureKeys();
    const enter = Math.min(1, cr.t / 0.6), leave = cr.leaving ? Math.min(1, cr.leaveT / 0.6) : 0;
    const hop = Math.abs(Math.sin(cr.t * (dragon ? 3 : 5))) * (dragon ? 6 : 4);
    const w = rows[0].length * scale, h = rows.length * scale;
    // Hit reaction: knocked back, and a white flash on stronger hits.
    const hit = cr.hitT > 0 ? cr.hitT / 0.3 : 0;
    const flash = hit > 0.45 && (cr.hitS > 0.3 || hit > 0.8);
    const x0 = Math.round(this.W * 0.83 - w / 2 + (1 - enter) * 60 + leave * 80 + hit * (cr.knock || 0) + (cr.lungeX || 0));
    const air = (cr.airT > 0 ? Math.sin((cr.airT / 0.55) * Math.PI) * (cr.airH || 12) : 0) + (cr.lungeY || 0); // knocked up by a shockwave, or leaping in
    const y0 = Math.round(this.floorY + 12 - h - hop + (hit > 0 ? -Math.round(hit * 2) : 0) - air);
    const Cc = flash ? this.white : C, cc = flash ? this.white : c;
    const colorOf = (ch, x, y) => (ch === 'C' ? Cc : ch === 'c' ? cc : ch === 'K' ? this.keys.K : ch === 'R' ? this.pal[RAIN + ((x + y) & 15)] : flash ? this.white : keys[ch]);
    for (let y = 0; y < rows.length; y++) for (let x = 0; x < rows[0].length; x++) {
      const ch = rows[y][x];
      if (ch === '.') continue;
      const col = colorOf(ch, x, y);
      for (let sy = 0; sy < scale; sy++) for (let sx = 0; sx < scale; sx++) this.px(x0 + x * scale + sx, y0 + y * scale + sy, col);
    }
  }

  creatureKeys() { return this.ck || (this.ck = Object.fromEntries(Object.entries(CREATURE_KEYS).map(([k, v]) => [k, hex32(v)]))); }

  drawDigging() {
    const list = this.digging;
    if (!list || !list.length || this.congrats) return;
    const b = list[0];
    const x0 = Math.round(this.W * 0.62), y0 = this.floorY + 2;
    // A glowing gem block half-buried in the floor.
    const pulse = Math.floor(this.t * 6);
    for (let y = 0; y < 9; y++) for (let x = 0; x < 9; x++) {
      const edge = x === 0 || y === 0 || x === 8 || y === 8;
      this.px(x0 + x, y0 + y, edge ? this.gold[(x + y + pulse) % 2 ? 0 : 1] : this.pal[RAIN + ((x + y + pulse) & 15)]);
    }
    for (let dy = -3; dy <= 11; dy++) for (let dx = -3; dx <= 11; dx++) {
      const d = Math.hypot(dx - 4, dy - 4);
      if (d > 6 && d < 7.5 && bayer(x0 + dx, y0 + dy) < 0.5) this.px(x0 + dx, y0 + dy, this.pal[GLOW + ((dx + pulse) & 15)]);
    }
    // Progress bar: confirmations toward maturity.
    const frac = Math.max(0, Math.min(1, b.conf / b.maturity));
    for (let x = -1; x < 12; x++) { this.px(x0 - 1 + x, y0 + 11, this.keys.K); this.px(x0 - 1 + x, y0 + 14, this.keys.K); }
    for (let x = 0; x < 11; x++) for (let y = 12; y < 14; y++) this.px(x0 - 1 + x, y0 + y, x < Math.round(frac * 11) ? this.pal[RAIN + ((x + pulse) & 15)] : this.pal[24]);
  }

  // Confirmed block: a small rainbow with "CONGRATULATIONS WIZARD!".
  drawCongrats() {
    const cg = this.congrats;
    if (!cg) return;
    const g = this.pose();
    const cx = g.ox + 8, top = Math.max(14, g.oy - 26); // arc spans top+4..top+20
    const grow = Math.min(1, cg.t / 0.8);
    for (let band = 0; band < 6; band++) {
      const r = Math.round((12 - band) * grow) + 4;
      const col = this.pal[RAIN + ((band * 2 + Math.floor(this.t * 12)) & 15)];
      for (let a = 0; a <= 64; a++) {
        const th = Math.PI + (a / 64) * Math.PI;
        this.px(cx + Math.cos(th) * r, top + 20 + Math.sin(th) * r, col);
      }
    }
    const text = 'CONGRATULATIONS WIZARD!';
    const tw = textWidth(text);
    let x = Math.max(2, Math.min(this.W - tw - 2, cx - Math.round(tw / 2)));
    const y = top - 7 + Math.round(Math.sin(cg.t * 4)); // 7-px text ending 3 px above the arc
    [...text].forEach((ch, i) => {
      const col = this.pal[RAIN + ((i + Math.floor(this.t * 16)) & 15)];
      drawTextBuf(this.buf, this.W, this.H, ch, x, y, col, this.keys.K);
      x += textWidth(ch) + 1;
    });
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
