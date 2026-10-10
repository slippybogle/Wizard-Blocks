// Creatures: each rarity tier has its own species (20 / 15 / 10 / 7 / 5),
// later ones rarer; the Dragon is the rarest Legendary. Facing left.
// C = body (the colour variant), c = its shade; the other keys are fixed
// accents (see CREATURE_KEYS). Names and order must match ui.Species.
export const SPECIES = [
  [ // Common
    { name: "Cave Mite", rows: [
      '...KKKKKK...', '..KCCCCCCK..', '.KCCCCCCCCK.', 'KCCWKCCWKCCK',
      'KCCWKCCWKCCK', 'KCCCCCCCCCCK', 'KcCCCKKCCCcK', '.KccCCCCccK.',
      '..KcK..KcK..', '..KK....KK..',
    ] },
    { name: "Rat", rows: [
      '.KK.........', 'KCCK........', 'KCWCKKKKK...', 'PCCCCCCCCK..',
      '.KCCCCCCCCK.', '..KcCCCCCcKK', '...KcKKKcK.K', '...KK...KK..',
    ] },
    { name: "Beetle", rows: [
      '...K....K...', '....K..K....', '..KKKKKKKK..', '.KCCCKKCCCK.',
      'KWKCCKKCCCCK', 'KCCcCKKCcCCK', 'KCCCcKKcCCCK', '.KCCCKKCCCK.',
      'K.KKKKKKKK.K', '.K.K.K.K.K..',
    ] },
    { name: "Slime", rows: [
      '....KKKK....', '...KCWCCK...', '..KCWCCCCK..', '.KCCCCCCCCK.',
      '.KCKWCCKWCK.', 'KCCKKCCKKCCK', 'KCCCCKKCCCCK', 'KcCCCCCCCCcK',
      '.KKKKKKKKKK.',
    ] },
    { name: "Worm", rows: [
      '.KKK........', 'KCWCK..KKK..', 'KCCCCKKCCCK.', '.KcCCCCCcCCK',
      '..KKcCCcKKCK', '....KKKK..KK',
    ] },
    { name: "Snail", rows: [
      '.K.K........', '.K.K.KKKKK..', '.KCK.KYYYYK.', 'KCWCKYKKKYYK',
      'KCCCKYKYKYYK', '.KCCKYYKYYYK', '.KCCCKYYYYK.', 'KcCCCCKKKKK.',
      'KKKKKKKKKKK.',
    ] },
    { name: "Moth", rows: [
      'K..........K', '.K........K.', 'KKKK.KK.KKKK', 'KCCCKWWKCCCK',
      'KCcCKKKKCcCK', 'KCCCKCCKCCCK', '.KCCKCCKCCK.', '..KKKCCKKK..',
      '....KccK....', '.....KK.....',
    ] },
    { name: "Spider", rows: [
      '....KKKK....', 'K..KCCCCK..K', '.KKCWKWKCKK.', 'K.KCCCCCCK.K',
      '.KKcCCCCcKK.', 'K..KcCCcK..K', '.K..KKKK..K.', 'K..K....K..K',
    ] },
    { name: "Mole", rows: [
      '...KKKKK....', '..KCCCCCKK..', '.KCKCCCCCCK.', 'KPCCCCCCCCCK',
      '.KCCCCCCCCCK', 'KwKcCCCCCcKK', 'KwwKcccccK.K', '.KKKKKKKKK..',
    ] },
    { name: "Frog", rows: [
      '.KKK....KKK.', 'KWKWK..KWKWK', 'KCCCKKKKCCCK', 'KCCCCCCCCCCK',
      'KCrrrrrrrrCK', '.KCCCCCCCCK.', 'KCcKCCCCKcCK', 'KKKKKKKKKKKK',
    ] },
    { name: "Bat Pup", rows: [
      'K...KKKK...K', 'KK.KCCCCK.KK', 'KCKCWCCWCKCK', 'KCCKCCCCKCCK',
      '.KCCKCCKCCK.', '..KK.KK.KK..', '.....KK.....',
    ] },
    { name: "Crab", rows: [
      'KK........KK', 'KwK..KK..KwK', '.KCKWKKWKCK.', '..KCCCCCCK..',
      '.KCCCCCCCCK.', 'KCcCCCCCCcCK', '.KKcccccKKK.', 'K.K.K..K.K.K',
    ] },
    { name: "Grub", rows: [
      '..KKKKKKK...', '.KCCwCCwCK..', 'KWKCCwCCwCK.', 'KCCCCwCCwCCK',
      '.KcccwccwccK', '..KKKKKKKKK.',
    ] },
    { name: "Centipede", rows: [
      '.KK.........', 'KCWK.KK.KK..', 'KCCCKCCKCCKK', '.KCCCCCCCCCK',
      '..KKcKKcKKcK', '..K.K.K.K.K.', '.K.K.K.K.K.K',
    ] },
    { name: "Gecko", rows: [
      '.KKK......K.', 'KCWCK....KCK', 'KCCCCKKKKCK.', '.KCCCCCCCK..',
      'K.KcCCCcK.K.', '.K.K.KK.K.K.',
    ] },
    { name: "Leech", rows: [
      '....KKKK....', '..KKCCCCKK..', '.KCCcCCcCCK.', 'KrKCCCCCCCCK',
      'KrKcCcCcCcCK', '.KKKKKKKKKK.',
    ] },
    { name: "Glow Shroom", rows: [
      '...KKKKKK...', '..KGGCGGGK..', '.KGCGGGCGGK.', 'KGGGGCGGGGGK',
      'KKKKKKKKKKKK', '...KWKKWK...', '...KwwwwK...', '..KKwwwwKK..',
      '..KKKKKKKK..',
    ] },
    { name: "Scorpion", rows: [
      '.......KK...', '......KCCK..', '.......KYK..', '.......KCK..',
      'KK....KCK...', 'KCK.KKCCK...', '.KCKCWCCCK..', '.KCCCCCCCCK.',
      '..KcCCCCcK..', '.K.K.KK.K.K.',
    ] },
    { name: "Firefly Swarm", rows: [
      '.Y.....G....', 'YGY...GYG...', '.Y..K..G..Y.', '...KCK...YGY',
      '..G.K..Y..Y.', '.GYG..YGY...', '..G....Y..G.', '......K..GYG',
      '.....KCK..G.', '......K.....',
    ] },
    { name: "Crystal Tick", rows: [
      '.....B......', '....BWB.....', '...KBBBK....', '..KCCBCCK...',
      '.KCWCBCWCK..', 'KCCCBBBCCCK.', 'KcCCCBCCCcK.', '.KKcCCCcKK..',
      'K.K.K.K.K.K.',
    ] },
  ],
  [ // Uncommon
    { name: "Rock Bat", rows: [
      'K...KKKKKK...K', 'KK.KCCCCCCK.KK', 'KCKCCWKKWCCKCK', 'KCCKCCCCCCKCCK',
      'KCcCKCrrCKCcCK', '.KCcKCCCCKcCK.', '..KK.KccK.KK..', '......KK......',
    ] },
    { name: "Giant Rat", rows: [
      '.KK...........', 'KCCK..........', 'KCWCKKKKKK....', 'PCCCCCCCCCKK..',
      '.KCCCCCCCCCCK.', '.KCCCCCCCCCCCK', '..KcCCCCCCCcKK', '...KcK.KKKcK.K',
      '...KKK...KKK.K',
    ] },
    { name: "Mud Crawler", rows: [
      '.K..K.........', '.W..W.........', '.K..K.........', 'KCCCCKKKKK....',
      'KCrCCCCCCCKK..', 'KCCCCcCCcCCCK.', '.KcCCCCCCCCcK.', '..KKcccccccKK.',
      '...KKKKKKKK...',
    ] },
    { name: "Fungal Toad", rows: [
      '...KGK..KGK...', '..KGGGKKGGGK..', '..KKKKKKKKKK..', '.KWKCCCCCCKWK.',
      '.KCCCCCCCCCCK.', 'KCrrrrrrrrrrCK', 'KCCCCCCCCCCCCK', 'KcCKCCCCCCKCcK',
      'KKKKKKKKKKKKKK',
    ] },
    { name: "Bone Snake", rows: [
      '.KKKK.........', 'KwKCwK........', 'KwwwwwK.......', '.KKrrK........',
      '...KwK...KKK..', '....KwK.KwwwK.', '.....KwKwK.KwK', '......KwwK..KK',
      '.......KK.....',
    ] },
    { name: "Kobold", rows: [
      '....KKK.......', '...KCCCK......', '..KCWKCCK...Kw', '.KCCCCCCK..Kw.',
      '..KKCCCKK.Kw..', '...KCCCCKKw...', '..KCrrrrCKK...', '..KCCCCCCK....',
      '...KCK.KCK....', '...KKK.KKK....',
    ] },
    { name: "Rust Beetle", rows: [
      'K.............', '.KK...........', '..KK.KKKKKK...', '..KCKOOOOOOK..',
      '.KWKOOOOOOOOK.', 'KCCCKOOOOOOOOK', '.KCCKOCOOOOCOK', '..KKKKKKKKKKK.',
      '..K.K.K.K.K.K.',
    ] },
    { name: "Cave Owl", rows: [
      '..K........K..', '..KK.KKKK.KK..', '..KCKCCCCKCK..', '..KWWKCCKWWK..',
      '..KWKKYYKKWK..', '..KCCCKKCCCK..', '.KCcCCCCCCcCK.', '.KCccCCCCccCK.',
      '..KCcCCCCcCK..', '...KKYKKYKK...',
    ] },
    { name: "Imp", rows: [
      '.K......K.....', '.KK.KK.KK.....', '..KCCCCCK.....', '..KYKCYKK..KK.',
      '..KCCCCCK.KCCK', '.KKKrrrKKKCK..', 'KCKCCCCCKCK...', 'K.KCCCCCK.....',
      '..KCK.KCK.....', '..KKK.KKK.....',
    ] },
    { name: "Gel Cube", rows: [
      'KKKKKKKKKKKK', 'KCCCCCCCCCCK', 'KCWCCCCCCWCK', 'KCKCCCCCCKCK',
      'KCCCCwCCCCCK', 'KCCCCCCCwCCK', 'KCwCCKKKCCCK', 'KCCCCCCCCCCK',
      'KcccccccccCK', 'KKKKKKKKKKKK',
    ] },
    { name: "Stone Turtle", rows: [
      '....KKKKKK....', '...KwDwwDwK...', '..KwwwDwwwwK..', 'KKKwDwwwDwwwK.',
      'KCWKwwDwwwDwK.', 'KCCCKKKKKKKKKK', '.KCCKCCCCCCK..', '..KKKCK..KCK..',
      '....KKK..KKK..',
    ] },
    { name: "Ice Lizard", rows: [
      '.KKK..........', 'KCWCK...B.B...', 'KCCCCKKKBKBK..', '.KrCCCCCCCCCK.',
      '..KKCCCCCCCCCK', '...KCK.KCK.KCK', '...KK..KK...KK',
    ] },
    { name: "Ghost", rows: [
      '....KKKKK.....', '...KCCCCCK....', '..KCCCCCCCK...', '..KCKKCKKCK...',
      '..KCKKCKKCK...', '..KCCCCCCCK...', '..KCCKKKCCK...', '..KCCCCCCCK...',
      '..KCCCCCCCK...', '..KCKCKCKCK...', '..K.K.K.K.K...',
    ] },
    { name: "Fire Salamander", rows: [
      '.KKK.....O....', 'KCWCK...OYO...', 'KCCCCKKKOYOK..', '.KCCCCCCCCCCK.',
      '..KKcOcOcOCCCK', '...KCK.KCK..KK', '...KK..KK.....',
    ] },
    { name: "Mimic Chest", rows: [
      '..KKKKKKKKKK..', '.KCCCCCCCCCCK.', '.KCYCCCCCCYCK.', '.KKKKKKKKKKKK.',
      '.KWKWKWKWKWKK.', '.KrrrrrrrrrrK.', '.KWKWKWKWKWKK.', '.KCCCCYYCCCCK.',
      '.KCCCCYYCCCCK.', '.KcccccccccCK.', '..KKKKKKKKKK..',
    ] },
  ],
  [ // Rare
    { name: "Shadow Goblin", rows: [
      '..K.KKKK.K....', '..KKCCCCKK....', '.KCCYKCYKCK...', '..KCCCCCCK....',
      '...KCrrCK...K.', '..KKKCCKKK.KwK', '.KCKCCCCKCKKw.', '.KCKDDDDKCKw..',
      '..KKDDDDKKw...', '...KCK.KCK....', '...KKK.KKK....',
    ] },
    { name: "Cave Troll", rows: [
      '....KKKKK.....', '...KCCCCCK....', '..KCYKCYKCK...', '..KCCCCCCCK...',
      '..KCwCCCwCK...', '.KKKCCCCCKKK..', 'KCCKCCCCCKCCK.', 'KCCKcCCCcKCCK.',
      'KCKKcccccKKCK.', '.K.KCCKCCK.K..', '...KCK.KCK....', '..KKKK.KKKK...',
    ] },
    { name: "Crystal Spider", rows: [
      '.....KKKK.....', '..K.KBBBBK.K..', '.K.KBWBBWBK.K.', 'K.KCBBBBBBCK.K',
      '.KKCCBBBBCCKK.', 'K.KCCCBBCCCK.K', '.K.KCCCCCCK.K.', 'K...KKKKKK...K',
      '.K..........K.',
    ] },
    { name: "Wraith", rows: [
      '....KKKKK.....', '...KCCCCCK....', '..KCCKKKCCK...', '..KCKYKYKCK...',
      '..KCCKKKCCK...', '.KCCCCCCCCCK..', 'KwKCcCCCcCKwK.', '.K.KCcCcCK.K..',
      '...KCCcCCK....', '...KCKCKCK....', '...K.K.K.K....',
    ] },
    { name: "Gargoyle", rows: [
      'K............K', 'KK..K....K..KK', 'KCK.KKKKKK.KCK', 'KCCKCYCCYCKCCK',
      'KCCCKCCCCKCCCK', '.KCCKCwwCKCCK.', '..KKCCCCCCKK..', '...KCcCCcCK...',
      '...KCCKKCCK...', '..KwK....KwK..', '..KKK....KKK..',
    ] },
    { name: "Basilisk", rows: [
      '.KK.K.........', 'KYKKYK........', 'KCCCCK........', 'KCYKCCK.......',
      'KrrCCCK.......', '.KKKCCK..KKK..', '....KCCKKCCCK.', '....KCcCCCcCCK',
      '.....KCCcCCKCK', '......KKKKK.KK',
    ] },
    { name: "Harpy", rows: [
      '...KKK........', '..KYYYK.......', '..KwYwK...KK..', '..KwwwK..KCCK.',
      '.KKKwKKKKCCK..', 'KCCCCCCCCCK...', '.KCCCCCCCK....', '..KCCCCCK.....',
      '...KYKYK......', '...K.K.K......',
    ] },
    { name: "Myconid Lord", rows: [
      '..KKKKKKKKK...', '.KCCGCCCGCCK..', 'KCGCCCGCCCGCK.', 'KKKKKKKKKKKKK.',
      '...KwYwYwK....', '...KwwwwwK....', '..KKwwwwwKK...', '.KwKwwwwwKwK..',
      '.K.KwwwwwK.K..', '...KwwKwwK....', '...KKK.KKK....',
    ] },
    { name: "Ogre", rows: [
      '....KKKKK.....', '...KCCCCCK....', '..KCYCCYCCK...', '..KCCCCCCCK...',
      '..KCwrrrwCK...', '.KKCCCCCCCKK..', 'KCCKOOOOOKCCK.', 'KCCKOOOOOKCCK.',
      'KCKKOOOOOKKCK.', '.K.KCCKCCK.K..', '...KCK.KCK....', '..KKKK.KKKK...',
    ] },
    { name: "Lich Apprentice", rows: [
      '....KYYYK.....', '...KCCCCCK....', '..KCwwwwwCK...', '..KCwKwKwCK...',
      '..KCwwwwwCK...', '.KCCCwKwCCCK.G', 'KCCCCCCCCCCKGG', '.KwwCCCCCwwKw.',
      '..KCCCCCCCKw..', '..KCcCcCcCKw..', '..KCCCCCCCK...', '..KKKKKKKKK...',
    ] },
  ],
  [ // Epic
    { name: "Lava Golem", rows: [
      '...KK......KK...', '...KYK....KYK...', '....KKKKKKKK....', '...KCCCCCCCCK...',
      '...KCOKCCOKCK...', '...KCCCCCCCCK...', '.KKKCOOOOOOCKKK.', 'KCCCKCCOOCCKCCCK',
      'KCCKCCCCCCCCKCCK', 'KCOKCOCCCCOCKOCK', 'KCCKCCCCCCCCKCCK', '.KK.KCCKKCCK.KK.',
      '....KCCK.KCCK...', '...KKKKK.KKKKK..',
    ] },
    { name: "Minotaur", rows: [
      '.KK........KK...', 'KwwK......KwwK..', '.KwwKKKKKKwwK...', '..KKCCCCCCKK....',
      '...KCYCCYCK.....', '...KCCCCCCK.....', '...KKwKKwKK.....', '.KKKCCCCCCKKK...',
      'KCCKCCCCCCKCCK..', 'KCCKCCcCCCKCCK.K', 'KCKKOOOOOOKKCKKw', '.K.KCCKKCCK.KKw.',
      '...KCCK.KCCK.w..', '..KKKK...KKKK...',
    ] },
    { name: "Cyclops", rows: [
      '.....KKKKKK.....', '....KCCCCCCK....', '...KCCKKKKCCK...', '...KCKWWKWKCK...',
      '...KCKWKKWKCK...', '...KCCKKKKCCK...', '...KCCrrrrCCK...', '..KKKCCCCCCKKK..',
      '.KCCKOOOOOOKCCK.', '.KCCKOOOOOOKCCK.', '.KCKKCCCCCCKKCK.', '..K.KCCKKCCK.K..',
      '....KCCK.KCCK...', '...KKKKK.KKKKK..',
    ] },
    { name: "Frost Giant", rows: [
      '....BBBBBB......', '...KwwwwwwK.....', '...KCBCCBCK.....', '...KCCCCCCK.....',
      '...KwwwwwwK.....', '...KwwwwwwK.....', '.KKKCwwwwCKKK...', 'KCCKBBBBBBKCCK..',
      'KCCKBBBBBBKCCK..', 'KCKKCCCCCCKKCK..', '.K.KCCKKCCK.K...', '...KCCK.KCCK....',
      '..KKKKK.KKKKK...',
    ] },
    { name: "Gloom Eye", rows: [
      '.K...K..K...K...', '.KC..KCKC..KC...', '..K.K.K.K.K.....', '...KKKKKKKK.....',
      '..KCCCCCCCCK....', '.KCCKKKKKKCCK...', '.KCKWWWWWWKCK...', '.KCKWWKKWWKCK...',
      '.KCKWWKKWWKCK...', '.KCKWWWWWWKCK...', '.KCCKKKKKKCCK...', '..KCrrrrrrCK....',
      '...KCCCCCCK.....', '....KKKKKK......',
    ] },
    { name: "Chimera", rows: [
      'K.K...........K.', 'KYK..........KCK', 'KYYK..KKKK..KCK.', 'KCWYK.KCCCK.KK..',
      'KCCCYKKCCCCKK...', '.KrCCCCCCCCCCK..', '..KCCCCCCCCCCCK.', '..KCCOOCCCCCCCK.',
      '...KCOOCCCCCCK..', '...KCK.KCK.KCK..', '...KKK.KKK.KKK..',
    ] },
    { name: "Bone Colossus", rows: [
      '.....KKKKKK.....', '....KwwwwwwK....', '...KwKCwwCKwK...', '...KwwwwwwwwK...',
      '....KwKwKwKK....', '..KKKKwwwwKKKK..', '.KwwKwKwwKwKwwK.', '.KwKKwwwwwwKKwK.',
      '.KwK.KwKKwK.KwK.', '.KCK.KwwwwK.KCK.', '..K..KwKKwK..K..', '.....KwK.KwK....',
      '....KKKK.KKKK...',
    ] },
  ],
  [ // Legendary
    { name: "Void Serpent", rows: [
      '..KKK...............', '.KCCCK..............', 'KCYKCCK.............', 'KCCCCCK.............',
      'KrrKCCK....KKKK.....', '.KK.KCCK..KCCCCK....', '....KCcCKKCcKKCCK...', '.....KCCCCcK..KCCK..',
      '......KKKKK...KCCK..', '..............KCcK..', '.....KKKK....KCCK...', '....KCCCCKKKKCcK....',
      '...KCcKKCCCCCCK.....', '....KK..KKKKKK......',
    ] },
    { name: "Cave Kraken", rows: [
      '.......KKKKKK.......', '......KCCCCCCK......', '.....KCCCCCCCCK.....', '.....KCYKCCYKCK.....',
      '.....KCCCCCCCCK.....', '......KCCCCCCK......', '....KKCKCKKCKCKK....', '...KCKCKCKKCKCKCK...',
      '..KCK.KCK..KCK.KCK..', '.KCK..KCK..KCK..KCK.', '.KCK...KCK.KCK...KCK', '..KK....KK..KK...KK.',
    ] },
    { name: "Phoenix", rows: [
      '.......KYK..........', '......KYOYK.........', '.....KCCCCK.........', '....KCWKCCCK........',
      '...YKCCCCCK....KK...', '..KYYKCCCK....KOOK..', '.KOOYYKCCCKKKKOYYOK.', 'KOYYOOYKCCCCCOYYOK..',
      '.KOOYYOOKCCCCYYOK...', '..KKOOYYOKCCCOOK....', '....KKOOYYKCCK......', '......KKKKYKK.......',
      '..........YOY.......', '...........Y........',
    ] },
    { name: "Demon Lord", rows: [
      '..K............K....', '..KK..........KK....', '...KK.KKKKKK.KK.....', '....KKCCCCCCKK......',
      '....KCYKCCYKCK......', 'K...KCCCCCCCCK....K.', 'KK..KCrWrWrrCK...KK.', 'KCK.KKCCCCCCKK..KCK.',
      'KCCKKCCCCCCCCKKKCCK.', 'KCCCKDDCCCCDDKCCCCK.', '.KCCKDDDDDDDDKCCK...', '..KKKCCDDDDCCKKK....',
      '....KCCK..KCCK......', '....KCCK..KCCK......', '...KKKKK..KKKKK.....',
    ] },
    { name: "Dragon", rows: [
      '..........KK....KK..', '.........KRRK..KRRK.', '..KK....KRRRRKKRRRRK', '.KYK...KRRRRRRRRRRK.',
      'KCCCK..KRRRRRRRRK...', 'KCWKCK..KRRRRRRK....', 'KCCCCCKKKCCCCCK.....', '.KccCCCCCCCCCCCK....',
      '..KKKcCCCCCCCCCCK...', '.....KcccCCCCCCCCKK.', '......KcccccCCCCCCCK', '.......KCK...KCK.KK.',
      '.......KCK...KCK....', '......KKK...KKK.....',
    ] },
  ],
];

// Fixed accent colours (C and c come from the colour variant).
export const CREATURE_KEYS = {
  K: 0x120c1c, W: 0xffffff, Y: 0xffd23f, O: 0xff9a3a, r: 0xe0402a, G: 0x7dff7a,
  B: 0x9fd8ff, w: 0xe8e0c8, D: 0x2a2340, P: 0xff9ac0,
};

// The creature for a tier and species. A head mounted before species
// existed has none: it was the tier's first creature, or the Dragon.
export function creatureSprite(tier, species) {
  const list = SPECIES[tier] || SPECIES[0];
  const i = species == null ? (tier === 4 ? list.length - 1 : 0) : species;
  return list[i] || list[0];
}

// Its head for the trophy wall: the top rows, trimmed to what is drawn.
const heads = new Map();
export function creatureHead(tier, species) {
  const sp = creatureSprite(tier, species);
  if (heads.has(sp)) return heads.get(sp);
  let rows = sp.rows.slice(0, Math.min(9, Math.ceil(sp.rows.length * 0.65)));
  const used = (x) => rows.some((r) => r[x] !== '.');
  let a = 0, b = rows[0].length - 1;
  while (a < b && !used(a)) a++;
  while (b > a && !used(b)) b--;
  rows = rows.map((r) => r.slice(a, b + 1));
  heads.set(sp, rows);
  return rows;
}

// Body colour. Pale round blobs (a light Cave Mite, Slime, Gel Cube or
// Ghost) would pass for Molly, so those colours are darkened: the reserved
// characters are never copied by a creature.
const BLOBS = [[0, 3], [9, 12]]; // per tier: species indexes
export function creatureBody(tier, species, base) {
  const blob = (BLOBS[tier] || []).includes(species ?? 0);
  const luma = (0.3 * ((base >> 16) & 255) + 0.59 * ((base >> 8) & 255) + 0.11 * (base & 255)) / 255;
  if (!blob || luma < 0.62) return base;
  const f = 0.55;
  return (Math.round(((base >> 16) & 255) * f) << 16) | (Math.round(((base >> 8) & 255) * f) << 8) | Math.round((base & 255) * f);
}
