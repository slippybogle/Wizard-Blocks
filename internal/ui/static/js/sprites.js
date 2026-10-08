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

// Creature (12 x 10). C = body colour, c = shade, W = eye white.
export const CREATURE = [
  '...KKKKKK...',
  '..KCCCCCCK..',
  '.KCCCCCCCCK.',
  'KCCWKCCWKCCK',
  'KCCWKCCWKCCK',
  'KCCCCCCCCCCK',
  'KcCCCKKCCCcK',
  '.KccCCCCccK.',
  '..KcK..KcK..',
  '..KK....KK..',
];
export const CREATURE_HORNS = [
  '..K......K..',
  '.KYK....KYK.',
];
// Legendary tier: the dragon (20 x 14, facing left). R = rainbow wing.
export const DRAGON = [
  '..........KK....KK..',
  '.........KRRK..KRRK.',
  '..KK....KRRRRKKRRRRK',
  '.KYK...KRRRRRRRRRRK.',
  'KCCCK..KRRRRRRRRK...',
  'KCWKCK..KRRRRRRK....',
  'KCCCCCKKKCCCCCK.....',
  '.KccCCCCCCCCCCCK....',
  '..KKKcCCCCCCCCCCK...',
  '.....KcccCCCCCCCCKK.',
  '......KcccccCCCCCCCK',
  '.......KCK...KCK.KK.',
  '.......KCK...KCK....',
  '......KKK...KKK.....',
];

// One colour per rarity tier (index = tier).
export const CREATURE_COLORS = [
  0x8a8f99, // Common: Cave Mite
  0x7f6bd6, // Uncommon: Rock Bat
  0x3f4a8a, // Rare: Shadow Goblin
  0xff6a2a, // Epic: Lava Golem
  0xc0392b, // Legendary: the dragon
];
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
