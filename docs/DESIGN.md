# Wizard-Blocks engine — design

Solo-mining Stratum V1 server for BTC and BCH. One process serves one coin and
talks to one local full node (Bitcoin Core / Bitcoin Cash Node). Go, standard
library only (no third-party modules): SHA-256, JSON, HTTP, `log/slog` and the
ZMQ (ZMTP 3.0) subscriber are all stdlib-based, which keeps the binary static and the
audit surface small.

## Data flow

```
 bitcoind/BCHN ──ZMQ hashblock──┐
        │  ▲                    ▼
        │  │ RPC          ┌───────────┐  Work (template+generation)  ┌──────────────┐
        │  └──────────────│ work.Mgr  │─────────────────────────────▶│ stratum.Srv  │◀──TCP── miners
        │   GBT/submit    │ (poll,    │◀── block candidate ──────────│ sessions,    │
        │                 │  refresh) │                              │ vardiff      │
        │                 └───────────┘                              └──────────────┘
        │                       │ submitblock → getblockheader verify        │ shares
        ▼                       ▼                                            ▼
                         stats.Collector  ─────── HTTP: /stats (JSON), /metrics, /healthz
```

1. **Startup**: load config → connect RPC → check coin/chain identity
   (`getnetworkinfo.subversion`, `getblockchaininfo.chain`) → decode payout
   address locally **and** with `validateaddress`; both scriptPubKeys must be
   identical or the engine refuses to start → wait until node is synced
   (`blocks == headers` and `!initialblockdownload`; regtest exempt from IBD).
2. **Templates** (`work.Manager`): `getblocktemplate` (`{"rules":["segwit"]}`
   on BTC, `{}` on BCH). Each template is verified before use: every tx's
   txid (and wtxid on BTC) is recomputed from `data`; BTC witness commitment is
   recomputed (BIP141) and must equal `default_witness_commitment`; BCH tx
   order must be CTOR (sorted ascending by txid), re-sorted if not. New tip is
   detected by ZMQ `hashblock` (instant) and by polling `getbestblockhash`
   (fallback, default 1 s). Mempool refresh every N s (default 30) without
   `clean_jobs`.
3. **Jobs**: one *generation* per prevhash. A job = template + coinbase built
   for one payout script (fixed mode → one job per template; miner mode →
   lazily one per distinct miner address). Jobs carry coinb1/coinb2, the
   stratum merkle branch, and everything needed to assemble a full block.
4. **Shares** (`stratum.Session`): rebuild coinbase(en1‖en2) → txid → merkle
   root (branch) → 80-byte header with negotiated version-rolling mask →
   SHA256d. If hash ≤ network target: assemble block and submit **before**
   anything else (async, retried on transport errors), then verify with
   `getblockheader` (confirmations ≥ 1). A block is `pending` until then and
   only `accepted` blocks count as found. Then share target check, duplicate
   check (keyed by header hash per generation), stats, vardiff.

## Packages

| package | role |
|---|---|
| `internal/bitcoin` | sha256d, varints, tx parsing (txid/wtxid), merkle root/branch, witness commitment, header, compact bits ↔ target ↔ difficulty, BIP34 height script |
| `internal/address` | base58check, bech32/bech32m (BIP173/350), CashAddr (incl. P2SH32, token-aware types) → scriptPubKey |
| `internal/node` | JSON-RPC client (user/pass or cookie), ZMTP 3.0 SUB client for `hashblock` |
| `internal/work` | coinbase builder, template verification, job manager, block assembly & submission |
| `internal/stratum` | TCP server, sessions, protocol, vardiff, rate limits |
| `internal/stats` | counters, per-worker hashrate windows, JSON + Prometheus HTTP |
| `internal/config` | JSON config + env overrides + validation |
| `internal/engine` | wiring, startup checks, graceful shutdown, saved difficulty settings |
| `internal/ui` | web UI: embedded static files, `/api/state`, SSE `/api/events`, hashrate history, node detail polling, authenticated settings API |
| `internal/testminer` | CPU Stratum V1 miner with version rolling (test harness + `cmd/testminer`) |
| `test/integration` | regtest harness (Docker) — build tag `integration` |

## Coinbase layout

```
version(4)=1 | vin=1 | prevout=00×32,ffffffff | scriptSig_len |
scriptSig = BIP34 height push | push(tag [+pad]) | push_{en1+en2}( en1(4) | en2(N) )
sequence=ffffffff | outputs | locktime=0
```
* coinb1 ends with the extranonce push opcode; coinb2 starts at `sequence`.
* BIP34 height uses `CScript() << height` semantics: OP_1..OP_16 for 1..16,
  otherwise minimal CScriptNum push (matters on regtest).
* scriptSig ≤ 100 bytes (consensus); tag is truncated/rejected at config time.
* BCH: coinbase padded to ≥ 100 bytes (BCH min tx size since 2018-11).
* Outputs: payout = `coinbasevalue` (subsidy + all fees) to the payout script;
  BTC adds the BIP141 commitment output (value 0) when the template has one and
  the block serialization gives the coinbase the 32-byte zero witness reserved value.

## Stratum details

* `mining.subscribe` → `[[["mining.set_difficulty",id],["mining.notify",id]], en1, en2_size]`.
* `mining.configure` (BIP310) → version-rolling mask = miner mask ∧ server mask
  (default `1fffe000`, BIP320). Submitted bits outside the mask are rejected.
  Implementations disagree on applying the bits (checked against their sources):

  | interpretation | header version | used by |
  |---|---|---|
  | `bip310` | `(job & ~mask) \| bits` | BIP310 text |
  | `xor` | `job ^ bits` | public-pool; ESP-Miner (Bitaxe/NerdQaxe) submits `rolled ^ job` |
  | `or` | `job \| bits` | ckpool (SV1); cgminer/bmminer (Antminer) submit the OR'd bits |

  They are identical whenever the job version has no bits inside the mask
  (normal mainnet, `0x20000000`). When a template signals a BIP9 deployment
  inside the mask (regtest `testdummy`, bit 28) they differ; the server
  evaluates each distinct candidate and keeps the lowest hash. Each candidate
  keeps the job's bits outside the mask, so any one meeting the target is a
  valid block. The interpretation used is logged per share and stored per
  block (`version_interpretation`, `template_version`, `block_version`).
* `mining.authorize`: `fixed` mode — any username, blocks pay the config
  address; `miner` mode — username must be `<address>[.<worker>]`, validated
  locally + by node, blocks pay that address. Password `d=<diff>` sets start difficulty.
* `mining.suggest_difficulty`, `mining.extranonce.subscribe` (ack; en1 never changes).
* Errors: 20 other, 21 job not found / stale, 22 duplicate, 23 low difficulty,
  24 unauthorized, 25 not subscribed.
* Share difficulty is capped at network difficulty so a block-solving hash is
  always submitted by the miner.
* Stale shares (old prevhash) are rejected, but if one still meets its own
  network target it is submitted anyway (could win an orphan race; costs nothing).

## Config schema (JSON; every field also settable by `WB_*` env vars)

```json
{
  "coin": "btc",                         // btc | bch
  "node": {
    "rpc_url": "http://127.0.0.1:8332",
    "rpc_user": "", "rpc_password": "", "rpc_cookie_file": "",
    "zmq_hashblock": "tcp://127.0.0.1:28334",   // "" disables ZMQ (polling only)
    "poll_interval_ms": 1000,
    "template_refresh_s": 30,
    "rpc_timeout_s": 30
  },
  "payout": {
    "mode": "fixed",                     // fixed | miner
    "address": "bc1q...",                // required in fixed mode
    "coinbase_tag": "/wizard-blocks/"
  },
  "stratum": {
    "listen": "0.0.0.0:1776",
    "public_port": 0,                    // port shown to miners if Docker maps another host port
    "extranonce2_size": 8,
    "version_rolling_mask": "1fffe000",
    "max_connections": 1024, "max_connections_per_ip": 64,
    "auth_timeout_s": 60, "idle_timeout_s": 600,
    "max_line_bytes": 16384, "msg_rate_per_s": 100, "msg_burst": 500
  },
  "vardiff": { "initial": 1024, "min": 1, "max": 1e15,
               "target_share_s": 10,             // VARDIFF_TARGET_SECONDS
               "fixed_diff": 0,                  // FIXED_DIFF: > 0 disables vardiff
               "retarget_s": 60, "variance_pct": 30 },
  "api": { "listen": "127.0.0.1:8080", "prometheus": true },
  "ui":  { "listen": "0.0.0.0:8420", "admin_password": "" },   // "" listen disables the UI
  "log": { "level": "info", "format": "json" },
  "data_dir": ""                         // persist found blocks / best diff if set
}
```

## Web UI (internal/ui)

The UI only reads engine state, apart from the authenticated difficulty settings.

- **Delivery**: vanilla JS modules, canvas and Web Audio, embedded with `go:embed`.
  - CSP is `default-src 'self'`: no third-party resources and no inline scripts or styles.
  - `/api/events` streams the full state once per second (Server-Sent Events).
- **Read-only stats added for it**:
  - per-job rounds: best share, summed share difficulty, network difficulty;
  - luck since the last found block (reset only when a block is confirmed accepted);
  - node detail polled every 10 s: peers, mempool, uptime, disk, network hashrate;
  - pool hashrate history at 1 h / 24 h / 7 d resolution, persisted;
  - block confirmations.
- **Celebration rule**: the client celebrates only when `blocks_found` increases
  *and* a block record newly reaches `accepted`. `pending` blocks only show a
  neutral notice. This is `events.js`, a pure function.
- **Creature rarity**: best share ÷ network difficulty. Luck percentile =
  exp(−S/D) × 100, with S = summed credited share difficulty and D = best share.
- **Difficulty settings**: `stratum.DiffSettings` sits behind an atomic pointer.
  Updates are validated, then re-applied to every session (`set_difficulty` plus a
  re-notify under a fresh job id) and persisted by the engine.
  - The settings API exists only on the UI listener.
  - It needs an HMAC-signed HttpOnly SameSite=Strict session from
    `ui.admin_password`, plus an `X-WB-Admin` header and same-origin check on changes.
  - Logins are rate-limited.

## Ambiguities and risks (and decisions)

1. **BIP310 `version_bits` semantics**: three interpretations in the wild
   (table above). Identical when `job & mask == 0`; otherwise all are
   evaluated and the lowest hash is kept. Tests assert the server always picks the
   exact header the test miner hashed, for miners in each of the three modes.
2. **Difficulty change timing** — stratum leaves it unspecified whether a new
   difficulty applies to the current job. We record the difficulty per
   (session, job) at notify time, accept `min(job diff, current diff)`, and re-notify
   the current work under a fresh job id after a vardiff change.
3. **BCHN refuses GBT** without peers / during IBD — even on regtest. The
   harness runs two connected BCHN nodes and mines one bootstrap block.
4. **Regtest target ≈ 2^255** — half of all hashes are blocks, so a miner/server
   hash mismatch could go unnoticed. The harness miner only submits hashes with
   ≥ 16 leading zero bits and the test asserts every accepted block hash has them.
5. **Rewards** — `coinbasevalue` is trusted from the node but cross-checked in
   tests against `subsidy(height) + getblockstats.totalfee`.
6. **Only one node** — no redundant submission path; submission is retried on
   transport errors and acceptance verified.
7. **Shares are statistics only** (solo); leniency on share difficulty never
   affects block validity — the network target check is independent.
8. **ZMQ** — own minimal ZMTP 3.0 NULL-mechanism SUB client (no libzmq/cgo);
   polling fallback always on.
