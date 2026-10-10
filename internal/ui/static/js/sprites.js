// Sprite data. Each sprite is rows of characters; '.' is transparent and
// every other character is a palette key resolved by the renderer.
// 'R' marks the rainbow robe: it is coloured from the cave's cycling
// palette so the robe and the crystals shimmer in sync.

export const WIZARD_KEYS = {
  K: 0x120c1c, // outline
  H: 0x4b2a8c, // hat
  h: 0x2f1a60, // hat shade
  Y: 0xffe066, // hat star
  S: 0xf1c79a, // skin
  s: 0xc99a6e, // skin shade
  E: 0x120c1c, // eye
  B: 0xf2f3f8, // beard
  b: 0xb8bccb, // beard shade
  O: 0x3a2414, // boots
};

// 16 x 23 body (facing right), legs are separate (3 rows).
export const WIZARD_BODY = [
  '.......K........',
  '......KhK.......',
  '......KHhK......',
  '.....KHHYhK.....',
  '.....KHYYYK.....',
  '....KHHHYhhK....',
  '...KKHHHHHhhKK..',
  '..KhHHHHHHHHHhK.',
  '...KKSSSSSSKK...',
  '....KSSSSESSK...',
  '....KSSSSSSSsK..',
  '....BBSSSSSBBK..',
  '...BBBBBBBBBBb..',
  '..KRBBBBBBBBbRK.',
  '..KRRBBBBBBbRRK.',
  '.KRRRBBBBBBbRRRK',
  '.KRRRRBBBBbRRRRK',
  '.KRRRRRBBbRRRRRK',
  'KRRRRRRBBRRRRRRK',
  'KRRRRRRRbRRRRRRK',
  'KRRRRRRRRRRRRRRK',
  '.KRRRRRRRRRRRRK.',
  '..KKKKKKKKKKKK..',
];

// Blink variant of the eye row.
export const WIZARD_BLINK_ROW = '....KSSSSKSSK...';

export const WIZARD_LEGS = {
  stand: ['...KOOK..KOOK...', '...KOOK..KOOK...', '..KOOOK..KOOOK..'],
  walk1: ['....KOOKKOOK....', '...KOOK...KOOK..', '..KOOOK...KOOOK.'],
  walk3: ['...KOOK.KOOK....', '..KOOK....KOOK..', '.KOOOK....KOOOK.'],
  jump:  ['...KOOK..KOOK...', '..KOOK....KOOK..', '................'],
};

// Shoulder pixel (where the arm starts), relative to the body.
export const SHOULDER = { x: 11, y: 14 };

// The Dragon's mounted head for the trophy wall (12 x 10, facing left);
// other heads are the creature's top rows (creatures.js).
export const DRAGON_HEAD = [
  '.....KK.....',
  '....KYK..KK.',
  '...KCCK.KRK.',
  '..KCCCCKRRK.',
  '.KCWKCCCCK..',
  'KCCCCCCCCK..',
  'KCcCCCCCCCK.',
  '.KKKKccCCCK.',
  '..KWKWKcCK..',
  '...K.K.KK...',
]

// Colour variants per rarity tier (index = tier). The rarer the tier, the
// fewer variants (20/15/10/7/5); within a tier later variants are rarer.
// The server picks the variant (ui.variantFor), so counts must match
// ui.VariantCounts.
export const CREATURE_VARIANTS = [
  [ // Common: Cave Mite
    0x8a8f99, 0x9c8a72, 0x6f7a6a, 0xa39b8b, 0x7d6f86, 0x8c7a5b, 0x5f6b78, 0x9a8f7a, 0x7a8576, 0xb0a090,
    0x6e6658, 0x8f9fa6, 0x7b5e57, 0xa0a8b0, 0x66705f, 0x998877, 0x857a99, 0x6a7f8f, 0xa58f6f, 0xd8d0c0,
  ],
  [ // Uncommon: Rock Bat
    0x7f6bd6, 0x5fa8d3, 0x4fb39b, 0x9d6bd6, 0x6b8cff, 0x58c4a8, 0xb07fe0, 0x4f9fc9,
    0x77d18a, 0x8a7ad9, 0x5ac2c2, 0xa38ae0, 0x6fb0ff, 0x63d4b0, 0xe0e0ff,
  ],
  [ // Rare: Shadow Goblin
    0x3f4a8a, 0x2f6f8f, 0x6a2f8f, 0x1f7a5a, 0x8f2f5f, 0x2f3f9f, 0x5a1f7a, 0x0f6f7f, 0x7a3f1f, 0x101018,
  ],
  [ // Epic: Lava Golem
    0xff6a2a, 0xff3d7f, 0xc04dff, 0x2ad4ff, 0xffd12a, 0x39ff6a, 0xffffff,
  ],
  [ // Legendary: the dragon
    0xc0392b, 0x1e90ff, 0x8e44ad, 0x16a085, 0xf1c40f,
  ],
];
export const creatureColor = (tier, variant = 0) => {
  const v = CREATURE_VARIANTS[tier] || CREATURE_VARIANTS[0];
  return v[variant] ?? v[0];
};
export const LEGENDARY = 4;
export const BLOCK_TIER = 5;

// Trophy gem (7 x 7), G = gem (rainbow), g = gem shade, Y = gold rim.
export const TROPHY = [
  '..YYY..',
  '.YGGGY.',
  'YGGGGgY',
  'YGGGggY',
  '.YGggY.',
  '..YgY..',
  '...Y...',
];

// Question bubble (7 x 9) shown while a block awaits confirmation.
export const BUBBLE_Q = [
  '.KKKKK.',
  'KWWWWWK',
  'KWKKKWK',
  'KWWWKWK',
  'KWWKWWK',
  'KWWWWWK',
  'KWWKWWK',
  '.KKKKK.',
  '..K....',
];

// Minecart (18 x 9). M = metal, m = metal shade, O = ore (rainbow).
export const MINECART = [
  '..RRRR..RRR.RR....',
  '.KRRRRRRRRRRRRRK..',
  'KMMMMMMMMMMMMMMMK.',
  'KMmmmmmmmmmmmmmMK.',
  'KMmMmMmMmMmMmMmMK.',
  'KMmmmmmmmmmmmmmMK.',
  '.KMMMMMMMMMMMMMK..',
  '..KOOK.....KOOK...',
  '...KK.......KK....',
];

// The Rune Smith dwarf (20 x 24) who joins at 10 TH/s of pool hashrate.
// A/M/a = bronze armour, Y = gold, G = rune (glows), R/r/o = red beard.
export const DWARF_BODY = [
  '........KYYK........',
  '.......KYooYK.......',
  '....KKKKYYYYKKKK....',
  '....KAAAAAAAAAAK....',
  '....KMMMMMMMMMMK....',
  '....KSSSSSSSSSSK....',
  '....KSEWSSSSEWSK....',
  '....KSSSSssSSSSK....',
  '...KRRoSSSSSSoRRK...',
  '...KRRRRSSSSRRRRK...',
  '..KAKRRRRRRRRRRKAK..',
  '.KAAMRRRRRRRRRRMAAK.',
  'KAAMMRRRRRRRRRRMMAAK',
  'KAAMMKRRRGGRRRKMMAAK',
  'KSAMMKRRRGGRRRKMMASK',
  'KSKMMKRRK..KRRKMMKSK',
  '.K.KMKRrK..KrRKMMK.K',
  '...KYYYYYYYYYYYYK...',
  '...KAAAAAaaAAAAAK...',
  '...KMMMaK..KaMMMK...',
  '...KMMaK....KaMMK...',
  '..KKKKKK....KKKKKK..',
  '..KaaaaK....KaaaaK..',
  '..KKKKKK....KKKKKK..',
];

export const DWARF_KEYS = {
  K: 0x120c1c, S: 0xf1c79a, s: 0xc99a6e, E: 0x120c1c, W: 0xffffff,
  R: 0xd8462a, r: 0x9c2a16, o: 0xff7a3a, B: 0x6a3f1f, b: 0x3f2410,
  A: 0xe0a75a, M: 0xb07a35, a: 0x6b4720, Y: 0xffe066, H: 0xc88a3a, h: 0x6b4720,
};
export const RUNE_GLOW = [0x1b6fff, 0x39a0ff, 0x39d0ff, 0x9ff0ff, 0xffffff];

// The elf archer (14 x 24) who joins at 20 TH/s. Faces right.
// Y/y = blonde hair, G/g = green tunic, B = leather, E = green eyes.
export const ELF_BODY = [
  '....KKKKK.....',
  '...KYYYYYK....',
  '..KYYYYYYYK...',
  '..KYYYSSSSK...',
  '..KYYSSESEK...',
  'KKYYSSSSSSK...',
  '.KSYYSSssK....',
  '..KYYKSSK.....',
  '..KYYGGGGK....',
  '..KYGGGGGGK...',
  '..KYGgGGGSK...',
  '..KyGGgGGSK...',
  '..KyGGGGGK....',
  '...KBBBBBK....',
  '...KGGGGGK....',
  '..KGGGgGGGK...',
  '..KGGGGgGGK...',
  '..KgGGGGGgK...',
  '...KSK.KSK....',
  '...KSK.KSK....',
  '...KBK.KBK....',
  '...KBK.KBK....',
  '..KBBK.KBBK...',
  '..KKKK.KKKK...',
];
export const ELF_KEYS = {
  K: 0x120c1c, Y: 0xffe08a, y: 0xd9b04a, S: 0xf6d0a8, s: 0xd9a77e, E: 0x1d7a3a,
  G: 0x3fae4a, g: 0x23702e, B: 0x7a4a22, W: 0xffffff, b: 0x9a6233,
};

// The cosmic dog (20 x 13) who joins at 30 TH/s: made of the night sky.
// N = nebula (cycles), E = eye. Faces right; mouth at (19, 5).
export const DOG_BODY = [
  '..............KK.K..',
  '.............KNNKNK.',
  '............KNNNNNK.',
  '...........KNNNENNNK',
  'K..........KNNNNNNNK',
  'NK.........KNNNNNKKK',
  '.NK.KKKKKKKNNNNNK...',
  '..KNNNNNNNNNNNNNK...',
  '..KNNNNNNNNNNNNK....',
  '..KNNNNNNNNNNNNK....',
  '..KNNK.KNK.KNNK.....',
  '..KNK..KNK..KNK.....',
  '..KKK..KKK..KKK.....',
];
export const NEBULA = [0x140a33, 0x2a1466, 0x4b2aa8, 0x8a3fd1, 0xd14fb8, 0x3fa0ff];

// The angel (16 x 18), floating at the back. H = golden hair, W/w = wings,
// R/r = white robe, Y = gold sash. Halo drawn separately.
export const ANGEL_BODY = [
  '......KKKK......',
  '.....KHHHHK.....',
  '....KHSSSSHK....',
  '....KHSESESK....',
  '....KHSSSSSK....',
  '.WW..KSSSSK..WW.',
  'WWWWK.KSSK.KWWWW',
  'WwWWKRRRRRRKWWwW',
  '.WwWKRRRRRRSKWw.',
  '..WWKRRYRRRKWW..',
  '...WKRRRRRRKW...',
  '....KRRRRRRK....',
  '....KRRrRRRK....',
  '...KRRRrRRRRK...',
  '...KRRRRrRRRK...',
  '..KRRRRRRrRRRK..',
  '..KrRRRRRRRRrK..',
  '...KKKKKKKKKK...',
];
export const ANGEL_KEYS = {
  K: 0x120c1c, H: 0xf2c86a, S: 0xf6d0a8, E: 0x2a5bd7, W: 0xffffff, w: 0xc8d4f0,
  R: 0xf4f1ff, r: 0xb9b2e0, Y: 0xffd23f,
};

// Molly (17 x 14): the little white blob alien. Hovers at the back and
// zaps with a green ray.
export const MOLLY_BODY = [
  '.....KKKKKKK.....',
  '...KKCCCCCCCKK...',
  '..KCCCCCCCCCCCK..',
  '.KCCCCCCCCCCCCCK.',
  '.KCCCWKCCCCWKCCK.',
  'KCCCCWKCCCCWKCCCK',
  'KCCCCWKCCCCWKCCCK',
  'KCCCCCCCCCCCCCCCK',
  'KcCCCCCKKKCCCCCcK',
  'KccCCCCCKCCCCCccK',
  '.KccCCCCCCCCCccK.',
  '..KKccCKKKCccKK..',
  '...KccK...KccK...',
  '...KKKK...KKKK...',
];
export const MOLLY_KEYS = { K: 0x120c1c, C: 0xf4f1ff, c: 0xb9b2e0, W: 0xffffff };
