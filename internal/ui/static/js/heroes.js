// Generated 16-bit party members for miners without a hand-made character.
// Everything is seeded from the miner's name, so the same miner looks the
// same on every screen and after restarts. Built from layers: race body,
// hair/beard, outfit, weapon and colours.

export const RACES = ['human', 'elf', 'dwarf', 'darkelf', 'halfling', 'halforc', 'gnome'];
export const RACE_NAMES = { human: 'Human', elf: 'Elf', dwarf: 'Dwarf', darkelf: 'Dark Elf', halfling: 'Halfling', halforc: 'Half-Orc', gnome: 'Gnome' };
// Weapon -> class name and how it attacks.
export const WEAPONS = {
  sword: { cls: 'Fighter', attack: 'slash' }, axe: { cls: 'Berserker', attack: 'slash' },
  mace: { cls: 'Cleric', attack: 'slash' }, hammer: { cls: 'Warden', attack: 'slash' },
  spear: { cls: 'Lancer', attack: 'thrust' }, dagger: { cls: 'Rogue', attack: 'dagger' },
  bow: { cls: 'Ranger', attack: 'arrow' }, staff: { cls: 'Mage', attack: 'bolt' },
};

// FNV-1a, then mulberry32: a small deterministic PRNG per miner.
function seedOf(s) {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619); }
  return h >>> 0;
}
function prng(seed) {
  let a = seed;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const pick = (r, list) => list[Math.floor(r() * list.length)];
const shade = (c, f = 0.62) => (Math.round(((c >> 16) & 255) * f) << 16) | (Math.round(((c >> 8) & 255) * f) << 8) | Math.round((c & 255) * f);

// Bodies, facing right. S skin, s skin shade, E eye, O/o outfit, T trim,
// P pants, B boots. head: outline top row and inner x range; hand: weapon hand.
const BODIES = {
  tall: {
    rows: [
      '............', '............',
      '..KKKKKKK...', '..KSSSSSK...', '..KSSSSEK...', '..KSSSSSK...', '..KSSSsSK...', '...KSSSK....',
      '..KOOOOOK...', '.KOOOOOOOK..', '.KOOOTOOOK..', '.KOOOOOOOK..', '.KoOOOOOoK..', '.KSOOOOOSK..',
      '..KTTTTTK...', '..KPPPPPK...', '..KPPKPPK...', '..KPPKPPK...', '..KPPKPPK...', '..KBBKBBK...',
      '.KBBBKBBBK..', '.KKKKKKKKK..',
    ],
    head: { top: 2, x0: 3, x1: 7, eye: 4, mouth: 6 }, hand: { x: 8, y: 13 }, chest: 9, belt: 14,
  },
  stout: {
    rows: [
      '............',
      '...KKKKKK...', '..KSSSSSSK..', '..KSSSSESK..', '..KSSSSSSK..', '..KSSssSSK..',
      '.KOOOOOOOOK.', 'KOOOOOOOOOOK', 'KOOOOTOOOOOK', 'KoOOOOOOOOoK', 'KSOOOOOOOOSK',
      '.KTTTTTTTTK.', '.KPPPPPPPPK.', '.KPPPKKPPPK.', '.KPPK..KPPK.', '.KBBK..KBBK.',
      'KBBBK..KBBBK', 'KKKKK..KKKKK',
    ],
    head: { top: 1, x0: 3, x1: 8, eye: 3, mouth: 5 }, hand: { x: 10, y: 10 }, chest: 7, belt: 11,
  },
  small: {
    rows: [
      '............',
      '...KKKKKK...', '..KSSSSSSK..', '..KSSSSESK..', '..KSSSSSSK..', '..KSSSsSSK..', '...KSSSSK...',
      '..KOOOOOOK..', '.KOOOTOOOOK.', '.KSOOOOOOSK.', '..KTTTTTTK..',
      '..KPPKKPPK..', '..KPK..KPK..', '..KBK..KBK..', '..KKK..KKK..',
    ],
    head: { top: 1, x0: 3, x1: 8, eye: 3, mouth: 5 }, hand: { x: 9, y: 9 }, chest: 8, belt: 10,
  },
};
const RACE_BODY = { human: 'tall', elf: 'tall', darkelf: 'tall', halforc: 'tall', dwarf: 'stout', halfling: 'small', gnome: 'small' };

const SKIN = {
  human: [0xf6d0a8, 0xe0b088, 0xc08a5e, 0x8d5a3a, 0x5e3a24], elf: [0xfbe0c4, 0xf2d2b0, 0xe8c09a],
  darkelf: [0x6a5a8a, 0x544a74, 0x7a6aa0], dwarf: [0xf1c79a, 0xd9a77e, 0xb07850],
  halfling: [0xf6d0a8, 0xe0b088, 0xc08a5e], halforc: [0x7fae5a, 0x6a9a4a, 0x8fb87a], gnome: [0xfbe0c4, 0xf1c79a, 0xe0b088],
};
const HAIR = {
  human: [0x2a1a12, 0x6a3f1f, 0xe8c060, 0xc0442a, 0xb0b0b8], elf: [0xf2d27a, 0xe8e8f0, 0xa04a2a, 0x2a1a12],
  darkelf: [0xf0f0f8, 0xc8c8d8, 0x9a7ad0], dwarf: [0xc0442a, 0x6a3f1f, 0x2a1a12, 0xa0a0a8, 0xe07a2a],
  halfling: [0x6a3f1f, 0xa04a2a, 0xe8c060], halforc: [0x1a1a1a, 0x4a3020, 0x707078],
  gnome: [0xf07ab0, 0xf0f0f8, 0xf0902a, 0x5a9af0, 0x6ad06a],
};
const CLOTH = [0xc0392b, 0x2f6fd6, 0x3fae4a, 0x8a3fd1, 0x2aa9a0, 0xe0802a, 0xe8c84a, 0x7a8090, 0x8a5a30, 0x3a3a4a, 0xe8e8f0, 0xe06aa0];
const RACE_WEAPONS = {
  human: ['sword', 'sword', 'spear', 'mace', 'bow', 'staff', 'axe'], elf: ['bow', 'bow', 'sword', 'staff', 'spear'],
  darkelf: ['dagger', 'dagger', 'sword', 'staff', 'bow'], dwarf: ['axe', 'axe', 'hammer', 'hammer', 'mace'],
  halfling: ['dagger', 'dagger', 'bow', 'sword'], halforc: ['axe', 'axe', 'hammer', 'mace', 'spear'],
  gnome: ['staff', 'staff', 'dagger', 'hammer'],
};
const HAIR_STYLES = ['short', 'long', 'mohawk', 'bald', 'ponytail', 'spiky'];

// The reserved hand-made characters are never copied: a generated look that
// could pass for Ernie, Chester, Ash or Kaido is re-rolled (deterministically).
const BLONDE = [0xf2d27a, 0xe8c060], GREEN = 0x3fae4a, BRONZE = 0xe0a75a, REDS = [0xc0442a, 0xe07a2a];
const WHITES = [0xb0b0b8, 0xa0a0a8, 0xe8e8f0, 0xf0f0f8, 0xc8c8d8];
function looksReserved(h) {
  if (h.race === 'elf' && h.weapon === 'bow' && (BLONDE.includes(h.hair) || h.cloth === GREEN)) return true; // Ash
  if (h.race === 'dwarf' && (h.cloth === BRONZE || (h.weapon === 'hammer' && REDS.includes(h.hair)))) return true; // Chester
  if (h.weapon === 'staff' && (h.beard || h.style === 'hat') && WHITES.includes(h.hair)) return true; // Ernie
  if (h.outfit === 'robe' && WHITES.includes(h.cloth) && BLONDE.concat([0xc8902a]).includes(h.hair)) return true; // Kaido
  return false;
}

// Builds a miner's character. race: from the stratum password, or null for
// a seeded random one.
export function buildHero(name, race) {
  for (let k = 0; k < 12; k++) {
    const h = rollHero(k ? `${name}#${k}` : String(name || ''), race);
    if (!looksReserved(h)) return h;
  }
  return rollHero(`${name}#plain`, race === 'elf' || race === 'dwarf' ? 'human' : race);
}

function rollHero(seedName, race) {
  const r = prng(seedOf(seedName));
  const rolledRace = pick(r, RACES);
  race = RACES.includes(race) ? race : rolledRace;
  const body = BODIES[RACE_BODY[race]];
  const g = body.rows.map((row) => [...row]);
  const w = g[0].length, h = g.length;
  const set = (x, y, ch) => { if (y >= 0 && y < h && x >= 0 && x < w) g[y][x] = ch; };
  const at = (x, y) => (y >= 0 && y < h && x >= 0 && x < w ? g[y][x] : '.');
  const { top, x0, x1, eye, mouth } = body.head;

  const skin = pick(r, SKIN[race]), hair = pick(r, HAIR[race]);
  const outfit = pick(r, race === 'dwarf' ? ['plate', 'plate', 'tunic'] : ['tunic', 'robe', 'leather', 'plate', 'cloak']);
  const weapon = pick(r, RACE_WEAPONS[race]);
  let cloth = pick(r, CLOTH), trim = pick(r, [0xe8b04a, 0xe8e8f0, 0x3a2a1a, 0xc0392b]);
  if (outfit === 'plate') { cloth = pick(r, [0xc9d1dc, 0xb0b8c8, 0xe0a75a]); trim = 0xffe066; }
  if (outfit === 'leather') { cloth = pick(r, [0x8a5a30, 0x6a4020, 0xa06a3a]); trim = 0x3a2410; }
  if (outfit === 'cloak') cloth = pick(r, [0x2a2a3a, 0x3a2a4a, 0x2a3a2a, 0x4a2020]);
  const pants = outfit === 'robe' ? cloth : pick(r, [0x4a3a2a, 0x2a2a3a, 0x5a4a3a, 0x3a4a5a]);
  const boots = pick(r, [0x3a2410, 0x2a1a12, 0x5a3a20]);

  // Robe: skirt down to the boots.
  if (outfit === 'robe') for (let y = body.belt + 1; y < h; y++) for (let x = 0; x < w; x++) if (at(x, y) === 'P' || (at(x, y) === 'K' && at(x - 1, y) === 'P' && at(x + 1, y) === 'P')) set(x, y, 'O');
  // Plate: shoulder pads.
  if (outfit === 'plate') { set(x0 - 2, body.chest, 'T'); set(x1 + 2, body.chest, 'T'); }

  // Hair (or a hood for cloaks).
  let style = race === 'dwarf' ? pick(r, ['short', 'bald', 'long', 'mohawk']) : pick(r, HAIR_STYLES);
  const hood = outfit === 'cloak' && r() < 0.7;
  if (hood) {
    for (let x = x0 - 1; x <= x1 + 1; x++) set(x, top, 'O');
    for (let x = x0; x <= x1 - 1; x++) set(x, top - 1, 'K');
    for (let y = top + 1; y <= mouth + 1; y++) { set(x0 - 1, y, 'O'); set(x0, y, 'o'); }
    style = 'hood';
  } else if (style !== 'bald') {
    for (let x = x0 - 1; x <= x1 + 1; x++) set(x, top, 'H');
    for (let x = x0; x <= x1 - 1; x++) set(x, top + 1, x <= x0 + 1 ? 'H' : at(x, top + 1));
    if (style === 'short' || style === 'long' || style === 'ponytail') { for (let x = x0; x <= x1; x++) set(x, top - 1, 'H'); for (let x = x0 + 1; x < x1; x++) set(x, top - 2, 'K'); }
    if (style === 'long') for (let y = top + 1; y <= top + 7; y++) { set(x0 - 1, y, 'H'); set(x0, y, 'H'); }
    if (style === 'ponytail') for (let y = top + 1; y <= top + 4; y++) set(x0 - 2, y, 'H');
    if (style === 'mohawk') for (let x = x0; x <= x1 - 1; x++) { set(x, top - 1, 'H'); set(x, top - 2, x % 2 ? 'H' : 'K'); }
    if (style === 'spiky') for (let x = x0 - 1; x <= x1; x += 2) { set(x, top - 1, 'H'); set(x + 1, top - 1, 'H'); set(x, top - 2, 'H'); }
  }

  // Beard: always on dwarves, sometimes on humans, half-orcs and gnomes.
  const beard = race === 'dwarf' || ((race === 'human' || race === 'halforc' || race === 'gnome') && r() < 0.4);
  if (beard) {
    const long = race === 'dwarf' ? 5 + Math.floor(r() * 3) : 1 + Math.floor(r() * 2);
    for (let y = mouth; y <= mouth + long; y++) for (let x = x0 + 1; x <= x1; x++) {
      if (y === mouth && x === x1 - 1) continue; // mouth stays open
      if (y > mouth + 1 && (x === x0 + 1 || x === x1) && y > mouth + long - 2) continue; // taper
      set(x, y, 'H');
    }
  }

  // Race features.
  if (race === 'elf' || race === 'darkelf') { set(x0 - 1, eye, 'S'); set(x0 - 2, eye - 1, 'S'); set(x0 - 2, eye, 'K'); }
  if (race === 'halforc') { set(x1, mouth, 'W'); set(x1 - 2, mouth, 'W'); }
  if (race === 'gnome') { set(x1 + 1, eye + 1, 's'); set(x1 + 2, eye + 1, 'K'); }
  if (race === 'gnome' && style === 'bald') { // pointy hat
    for (let k = 0; k < 4; k++) for (let x = x0 + k; x <= x1 - k; x++) set(x, top - k, k === 0 ? 'T' : 'O');
    style = 'hat';
  }
  if (race === 'halfling') { for (let y = h - 3; y < h - 1; y++) for (let x = 0; x < w; x++) if (at(x, y) === 'B') set(x, y, 'S'); } // bare feet

  const keys = {
    K: 0x120c1c, S: skin, s: shade(skin, 0.82), E: race === 'darkelf' ? 0xff4d6d : 0x120c1c, W: 0xffffff,
    H: hair, O: cloth, o: shade(cloth), T: trim, P: pants, B: boots,
  };
  const info = WEAPONS[weapon];
  return {
    rows: g.map((row) => row.join('')), keys, w, h, hand: body.hand, race, weapon, outfit, style, hair, cloth, beard,
    attack: info.attack, cls: info.cls, label: `${RACE_NAMES[race]} ${info.cls}`,
    accent: pick(r, [0x9ff0ff, 0xffe066, 0xff7ad9, 0x7dff7a, 0xff9a3a]),
  };
}
