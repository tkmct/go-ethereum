# UBT Sidecar Implementation Plan (Refined v1)

This plan refines the original sidecar proposal to be **implementable, safe, and testable**.
It incorporates the latest validation plan and addresses gaps found during review.

---

## Current State (Already Implemented)

From `docker-ubt-test/UBT_VALIDATION_PLAN.md` the following are **already done**:

- ✅ RPC: `debug_getUBTProof`, `debug_executionWitnessUBT`, helpers `openBinaryTrie()`, `generateProofFromBinaryTrie()`
- ✅ Witness types: `PathNode`, `ExtUBTWitness`, conversions `NewWitnessFromExtWitness`, `NewWitnessFromUBTWitness`
- ✅ Stateless execution: `ExecuteStatelessWithPathDB(..., usePathDB bool)`
- ✅ Validation tool: dual-node architecture, Phase 0–5 structure, CLI flags

---

## Goals / Non-Goals

### Goals
- Maintain **MPT as consensus state** while keeping a **shadow UBT state** for RPC.
- Existing RPC methods must continue to return **MPT-backed** data; UBT data is **only** exposed via UBT-specific debug RPCs.
- Support `debug_executionWitnessUBT` and `debug_getUBTProof` without altering header roots.
- Add a new UBT-specific debug RPC to read state **from UBT** (no MPT fallback).
- Full sync only; no snap sync, no state root override.

### Non-Goals (v1)
- No snap sync conversion.
- No consensus root changes (no `CommitStateRootToHeader`).
- No soft-fallback to MPT if sidecar is enabled but not ready.

---

## Hard Preconditions (Enforced)

UBT sidecar MUST run with:
- `--syncmode=full`
- `--cache.preimages` (auto-enabled if missing)
- `--state.scheme=path` (required for MPT iteration)

**Missing preimages are a hard error.**
If any address or storage key preimage is missing during conversion or updates, the sidecar must fail and disable itself.

---

## Architecture Overview

The sidecar uses a **separate trie database** with a **Verkle-prefixed keyspace** to avoid collisions.

```
┌─────────────────────────────────────────────────────────────────────┐
│                        BlockChain                                   │
├─────────────────────────────────────────────────────────────────────┤
│  ┌────────────────────┐        ┌────────────────────┐              │
│  │  Primary MPT       │        │  Sidecar UBT       │              │
│  │  triedb.Database   │        │  triedb.Database   │              │
│  │  - PathScheme      │        │  - PathScheme +    │              │
│  │  - Consensus       │        │    IsVerkle=true   │              │
│  └────────────────────┘        └────────────────────┘              │
│           │                             │                           │
│           └──────────┬──────────────────┘                           │
│                      ▼                                              │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │                     ethdb.Database                           │  │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │  │
│  │  │ MPT Nodes   │  │ Preimages   │  │ UBT Nodes           │  │  │
│  │  │ Prefix: A/O │  │ secure-key- │  │ Prefix: v + A/O     │  │  │
│  │  └─────────────┘  └─────────────┘  └─────────────────────┘  │  │
│  └──────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
```

**Key decision**: Use `triedb.Config{IsVerkle: true}` for the sidecar triedb.
This routes UBT nodes into the `v`-prefixed namespace and avoids collisions with MPT pathdb nodes.

---

## Dataflow / System Architecture

```
                     ┌────────────────────────────────────┐
                     │            Block Import            │
                     │ (headers + txs, canonical ordering)│
                     └────────────────────────────────────┘
                                      │
                                      ▼
                     ┌────────────────────────────────────┐
                     │      MPT State Execution           │
                     │ CommitWithUpdate* (StateUpdate)    │
                     └────────────────────────────────────┘
                       │                       │
          (MPT-backed) │                       │ (sidecar hook)
     existing RPCs     ▼                       ▼
  ┌────────────────────────┐         ┌────────────────────────────┐
  │  eth/api_* (MPT only)  │         │        sidecar/            │
  │  eth_get* / debug_*    │         │  UBTSidecar (isolated)     │
  └────────────────────────┘         └────────────────────────────┘
                                              │
                          ┌───────────────────┴───────────────────┐
                          │                                       │
                          ▼                                       ▼
              ┌───────────────────────┐               ┌───────────────────────┐
              │ Converting (MPT→UBT)  │               │ Ready (ApplyStateUpdate)│
              │ iterate MPT + preimages│              │ update UBT per block   │
              └───────────────────────┘               └───────────────────────┘
                          │                                       │
                          └──────────────┬────────────────────────┘
                                         ▼
                         ┌────────────────────────────────────┐
                         │     Sidecar triedb (UBT nodes)     │
                         │   PathScheme + IsVerkle=true       │
                         └────────────────────────────────────┘
                                         │
                                         ▼
                         ┌────────────────────────────────────┐
                         │   debug_getUBTProof / debug_getUBTState │
                         │   debug_executionWitnessUBT             │
                         └────────────────────────────────────┘
```

**Flow notes:**
- **Normal block processing**: `CommitWithUpdate*` produces `StateUpdate`. If sidecar is
  **Ready**, `ApplyStateUpdate` updates UBT in `sidecar/`. Existing RPCs remain MPT-backed.
- **During conversion**: sidecar enqueues updates and replays after conversion completes.
- **Reorgs**: sidecar handles shallow reorgs using pathdb history; deep reorgs mark stale.
- **UBT RPCs**: only UBT-specific debug RPCs read from the sidecar UBT trie.

---

## Module Isolation (Required)

This feature must be **self-contained**:
- Create a top-level `sidecar/` directory and place **most** sidecar logic there.
- Keep changes in existing modules **minimal** (wiring + small hooks only).
- Existing modules should interact with the sidecar through a small, explicit interface.

**Implementation guidance:**
- Move conversion, update queue, reorg handling, and UBT state access into `sidecar/`.
- Expose a small API surface (e.g., `sidecar.UBTSidecar`) for:
  - lifecycle (`InitFromDB`, `Ready`, `Converting`, `Enabled`)
  - UBT root lookup (`GetUBTRoot`)
  - update application (`ApplyStateUpdate`, `EnqueueUpdate`)
  - UBT state reads (for the new debug RPC)
- Core/eth/debug should only delegate to this interface.

**Proposed minimal interface (for plan clarity):**
```
type UBTSidecar interface {
    Enabled() bool
    Ready() bool
    Converting() bool
    InitFromDB() error
    CurrentRoot() common.Hash
    GetUBTRoot(blockHash common.Hash) (common.Hash, bool)

    ApplyStateUpdate(block *types.Block, update *state.StateUpdate, db ethdb.Database) error
    EnqueueUpdate(block *types.Block, update *state.StateUpdate) error

    ReadAccount(root common.Hash, address common.Address) (UBTAccount, error)
    ReadStorage(root common.Hash, address common.Address, key common.Hash) (common.Hash, error)
}
```
`UBTAccount` should contain at least balance, nonce, and code hash (plus code size if needed).

---

## Sidecar State Machine

```
Disabled  ->  Converting  ->  Ready
   ^              |             |
   |              v             v
   +---------- Stale/Error <----+
```

- **Disabled**: sidecar not configured
- **Converting**: MPT → UBT conversion running (RPC should return “sidecar not ready”)
- **Ready**: UBT root available and kept in sync
- **Stale/Error**: unrecoverable error (missing preimages, deep reorg, etc). Requires reconversion

---

## Database Keys (new)

**File: `core/rawdb/schema.go`** (add):
```
UBTSidecarPrefix        = []byte("ubt-sidecar-")
UBTConversionProgressKey = []byte("ubt-conv-progress")
UBTCurrentRootKey       = []byte("ubt-current-root")
UBTBlockRootPrefix      = []byte("ubt-block-root-") // + blockHash
UBTUpdateQueuePrefix    = []byte("ubt-update-queue-") // + blockNum + blockHash
UBTUpdateQueueMetaKey   = []byte("ubt-update-queue-meta") // queue bounds/checkpoints
```

**File: `core/rawdb/accessors_ubt_sidecar.go`**:
```
func WriteUBTCurrentRoot(db ethdb.KeyValueWriter, root common.Hash, block uint64, blockHash common.Hash)
func ReadUBTCurrentRoot(db ethdb.KeyValueReader) (root common.Hash, block uint64, hash common.Hash, ok bool)
func WriteUBTBlockRoot(db ethdb.KeyValueWriter, blockHash common.Hash, root common.Hash)
func ReadUBTBlockRoot(db ethdb.KeyValueReader, blockHash common.Hash) (common.Hash, bool)
func WriteUBTConversionProgress(db ethdb.KeyValueWriter, p *ConversionProgress)
func ReadUBTConversionProgress(db ethdb.KeyValueReader) *ConversionProgress
func DeleteUBTConversionProgress(db ethdb.KeyValueWriter)
```

**Why blockHash keying?** Reorg-safe lookup without number collisions.

---

## Step 1: Configuration Flags

**File: `eth/ethconfig/config.go`**
```
UBTSidecar            bool `toml:",omitempty"`
UBTSidecarAutoConvert bool `toml:",omitempty"` // auto-convert after FULL sync
```

**File: `cmd/utils/flags.go`**
```
ubt.sidecar
ubt.sidecar.autoconvert   // after full sync only
```

**Enforced constraints (fatal if violated):**
```
if cfg.UBTSidecar {
    if cfg.SyncMode != downloader.FullSync { Fatalf(...) }
    if cfg.StateScheme != rawdb.PathScheme { Fatalf(...) }
    if !cfg.Preimages { cfg.Preimages = true; log.Warn(...) }
}
```

---

## Step 2: UBTSidecar Type (`sidecar/ubt_sidecar.go`)

Key fields:
- `triedb` uses `IsVerkle=true` to isolate UBT nodes.
- `ready`, `converting`, `stale` flags.
- `currentRoot`, `currentBlock`, `currentHash`.
- Optional code-size cache (avoid repeated code reads).

Expose:
```
Enabled() bool          // Ready and not stale
Ready() bool
Converting() bool
CurrentRoot() common.Hash
InitFromDB()            // loads current root/block if present
```

---

## Step 3: MPT → UBT Conversion

### Mandatory: missing preimages = hard error
If any address or storage slot preimage is missing, conversion **fails and sidecar becomes stale**.

### Implementation outline
```
ConvertFromMPT(stateRoot, blockNum, blockHash, mptDB, chainDB)
```

Conversion algorithm:
1. Mark `converting=true`; persist `ConversionProgress` (for visibility).
2. Create MPT account iterator from `mptDB.AccountIterator(stateRoot, seek)`.
3. For each account:
   - Read address preimage (`rawdb.ReadPreimage`) or **fail**.
   - Decode account: `types.FullAccount(accountBlob)`.
   - Resolve code length:
     - if CodeHash == EmptyCodeHash -> 0
     - else read code via `rawdb.ReadCode` (must exist) and use len(code)
   - `bt.UpdateAccount(addr, account, codeLen)`
   - If code present: `bt.UpdateContractCode(addr, codeHash, code)`
   - If storage root != empty: iterate storage and update slots
     - slot hash → preimage (must exist)
     - slot value as raw 32-byte value
4. After iteration, if `accIt.Error()` (or any `storageIt.Error()`) is non-nil,
   abort conversion and restart from the latest canonical head.
5. Commit:
   - `root, nodeset := bt.Commit(false)`
   - `triedb.Update(root, types.EmptyBinaryHash, blockNum, nodeset.Flatten(), nil)`
6. Store `UBTCurrentRoot` + `UBTBlockRoot(blockHash)`.
7. Clear conversion progress, mark `ready=true`.

**Note:** Conversion is atomic. No partial resume is supported in v1.
If conversion fails, the node must re-run conversion.

### Background conversion + catch-up (non-blocking)
Conversion can run in the background so the client keeps processing blocks, but
the sidecar must **buffer updates** and **catch up** before it becomes ready.

**Flow (recommended):**
1. When conversion starts, capture a fixed anchor:
   - `convRoot`, `convBlockNum`, `convBlockHash`
2. Set `sidecar.ready=false` and reject UBT RPCs during conversion.
3. While converting, for each committed block:
   - Persist a **serialized UBT update** (see below) into a **UBT update queue**
     (e.g. `UBTUpdateQueuePrefix + blockNum + blockHash`).
4. After conversion completes:
   - Initialize UBT at `convRoot`, then **replay queued updates** in order
     until reaching current head.
   - Verify parent hash continuity; if mismatch, drop non-canonical entries.
5. If queue overflows or any update is missing/corrupt → mark stale and
   require reconversion.

**Notes:**
- `triedb.Reference` is **not available** for PathDB. If the conversion iterator
  becomes stale (`it.Error() != nil`), abort conversion and restart from the
  latest canonical head.
- This avoids blocking client progress but still consumes CPU/disk; add optional
  throttling (sleep/yield every N accounts) to limit impact.
- Reorg handling still applies; if a reorg crosses `convBlockHash`, abort
  conversion and restart from the new canonical head.

### UBT Update Queue format (required for non-blocking mode)

Persist only the data needed to replay updates into UBT and to build sidecar
state history:

```
type UBTUpdate struct {
  BlockNum      uint64
  BlockHash     common.Hash
  ParentHash    common.Hash
  RawStorageKey bool

  Accounts       map[common.Hash][]byte          // slim RLP
  AccountsOrigin map[common.Address][]byte       // slim RLP (origin)
  Storages       map[common.Hash]map[common.Hash][]byte
  StoragesOrigin map[common.Address]map[common.Hash][]byte
  Codes          map[common.Address][]byte       // code blob for changed code
}
```

- Serialize as RLP.
- Enqueue only while `converting=true`.
- On replay, **skip entries not in canonical chain** using block hash checks.

---

## Step 4: Post-Conversion Verification (Required)

After conversion completes, we must **prove** that the UBT sidecar is consistent
with the canonical MPT state **before** relying on it for RPC.

### Verification checklist (must pass)

1. **Sidecar readiness**
   - Call `debug_getUBTProof` for an anchor block (safe/finalized).
   - Expect **success** and non-zero `UbtRoot`.
   - If RPC fails or `UbtRoot` is zero → conversion invalid.

2. **UBT root consistency**
   - Call `debug_getUBTProof` for **N different addresses** at the same block.
   - All responses must return the **same `UbtRoot`**.
   - Mismatch → conversion invalid.

3. **Account sampling vs local MPT (no reference node)**
   - Use local `debug_accountRange` at the same anchor block (MPT-backed).
   - Sample at least **N=1,000** accounts (configurable).
   - Compare MPT vs UBT results for the same accounts:
     - balance
     - nonce
     - code **hash** (and code size if available)
   - Any mismatch → conversion invalid.

4. **Storage sampling vs local MPT (no reference node)**
   - For each sampled contract account, take **K slots** using local
     `debug_storageRangeAt` (MPT-backed).
   - Compare MPT vs UBT results for the same slots.
   - Any mismatch → conversion invalid.

5. **Witness sanity (optional but recommended)**
   - For a single block `B`, run:
     - `debug_executionWitnessUBT(B)`
     - `ExecuteStatelessWithPathDB(..., usePathDB=true)`
   - Compare computed root to `UbtRoot` (from `debug_getUBTProof`).
   - Failure indicates conversion or RPC mismatch.

### Enforcement
- If any check fails: **mark sidecar stale** and require reconversion.
- Only after checks pass should sidecar be considered `Ready`.

---

## Step 5: Incremental Block Updates (Core integration)

**Do NOT add ad-hoc `ExtractStateChanges`.**
Use the existing `state.StateUpdate` produced by `CommitWithUpdate*`.

**Required accessor work:** `state.StateUpdate` has unexported fields. Add
exported accessors (or a conversion helper) in `core/state` to read:
`Accounts`, `AccountsOrigin`, `Storages`, `StoragesOrigin`, `RawStorageKey`,
and `Codes`. Sidecar must not rely on unexported fields.

### Hook point
In `core/blockchain.go` after successful `CommitWithUpdate*`, call:
```
if bc.ubtSidecar != nil {
    if bc.ubtSidecar.Ready() {
        if err := bc.ubtSidecar.ApplyStateUpdate(block, update, bc.db); err != nil {
            // mark stale + log
        }
    } else if bc.ubtSidecar.Converting() {
        // enqueue UBTUpdate for catch-up
        bc.ubtSidecar.EnqueueUpdate(block, update)
    }
}
```

**Non-blocking conversion requirement:** when `ubt.sidecar` is enabled,
`CommitWithUpdate` must always be used (not `*NoCode`) so sidecar receives
full updates even before it is ready.

### ApplyStateUpdate (`sidecar/ubt_sidecar.go`)
For each mutated account in `update`:

- Use `AccountsOrigin` keys (addresses) as the primary iteration source.
  Compute `addrHash := crypto.Keccak256Hash(addr.Bytes())` to locate the
  new account data in `Accounts`.
- Decode slim account with `types.FullAccount(data)`.
- Compute `codeLen`:
  - if `Codes[addr]` exists → len(code)
  - else if CodeHash empty → 0
  - else read code by hash from chainDB (must exist)

- **Account updates:**
  - `bt.UpdateAccount(addr, account, codeLen)`
  - If code changed: `bt.UpdateContractCode(addr, codeHash, code)`

- **Storage updates:**
  - `Storages[addrHash]` contains slot key (raw or hash) → RLP-encoded value
  - If `RawStorageKey` is true: use slot keys directly
  - Else: slot keys are hashes → resolve with `rawdb.ReadPreimage`
    (missing preimage → error)
  - Decode value to 32 bytes (prefix-zero-trimmed RLP)
  - If value == nil → `bt.DeleteStorage(addr, key)`
  - Else → `bt.UpdateStorage(addr, key, value)`

- **Account deletions:**
  - `Accounts[addrHash] == nil`
  - delete all storage slots listed in `Storages` (must include full wipe)
  - delete contract code chunks (new helper required)
  - write a deleted marker for account (zero basic data + zero code hash)

- **Commit**
  - `newRoot, nodeset := bt.Commit(false)`
  - Build `triedb.StateSet` from the same update data and pass it to
    `triedb.Update(newRoot, oldRoot, blockNum, nodeset.Flatten(), stateSet)`.
  - Persist `UBTCurrentRoot` + `UBTBlockRoot(blockHash)`.

### Required helper additions
- `bintrie.DeleteContractCode(addr, codeSize)` (or equivalent)
- Minimal delete marker for account metadata (set BasicData + CodeHash leaves to zero)

### RLP decoding for storage values
`Storages` values are RLP of trimmed bytes. Decode to raw 32-byte value before updating UBT.

---

## Step 6: Reorg Handling (Opinion & Plan)

**Opinion:** We should support shallow reorgs using pathdb state history. Deep reorgs should mark sidecar stale and require reconversion.

### Implementation
- Add a hook in `bc.reorg` to notify the sidecar.
- Determine the new canonical ancestor and its UBT root:
  - look up `UBTBlockRoot(ancestorHash)`
  - if missing → stale
- If sidecar triedb reports `Recoverable(root)`:
  - `sidecar.triedb.Recover(root)`
  - set `currentRoot/currentBlock/currentHash` to ancestor
  - apply `ApplyStateUpdate` for each new-chain block (already invoked during reorg block insertion)
- If not recoverable or root missing:
  - mark sidecar stale; require reconversion

**Config note:** `StateHistory` in pathdb controls recover depth. To make
Recover work, `ApplyStateUpdate` must pass a populated `triedb.StateSet`
into `triedb.Update` so sidecar histories are recorded.

---

## Step 7: RPC Integration

`DebugAPI.openBinaryTrie()` behavior:
- If sidecar is enabled **and ready**, use sidecar triedb.
- If sidecar is enabled but not ready, return explicit error (no fallback).
- If sidecar is disabled, use existing main triedb behavior **only** for non‑UBT paths.

This prevents accidentally serving MPT when UBT is expected.

**Important:** in sidecar mode, `debug_getUBTProof` must open the trie with the
sidecar UBT root for the target block (not `header.Root`). Use:
`ubtRoot := sidecar.GetUBTRoot(blockHash)` and pass that root to
`openBinaryTrie`.

**New debug RPC (required):** add a UBT-specific state getter (e.g. `debug_getUBTState`).
- Reads account/storage **from UBT** and returns values without consulting MPT.
- Must return explicit error if sidecar is disabled or not ready (no fallback).
- Use the sidecar UBT root for the target block (not `header.Root`).

---

## Step 8: CLI / Conversion Commands

### Manual (recommended)
```
geth --syncmode full --state.scheme=path --cache.preimages --ubt.sidecar
geth ubt convert --datadir <...>
```

### Auto-convert (optional)
```
geth --syncmode full --state.scheme=path --cache.preimages --ubt.sidecar --ubt.sidecar.autoconvert
```

Auto-convert triggers after full sync **only when sidecar is not ready**.
If chain head advances during conversion, sidecar catches up by replaying the
queued `state.StateUpdate` entries before becoming ready.

---

## Alignment With Validation Plan

The validation plan currently assumes UBT root in header for UBT mode.
**For sidecar mode:**
- Header root remains MPT.
- UBT root must be obtained via `debug_getUBTProof` or sidecar root accessor.
- `CommitStateRootToHeader` should remain **false**.

Update validation instructions accordingly when testing sidecar nodes.

---

## Critical Files to Modify

| File | Changes |
|------|---------|
| `eth/ethconfig/config.go` | Add UBTSidecar + AutoConvert flags |
| `cmd/utils/flags.go` | Add CLI flags + enforce full sync/path scheme/preimages |
| `sidecar/*` | New sidecar module: conversion, updates, reorg, UBT state reads |
| `core/rawdb/schema.go` | Add sidecar key prefixes |
| `core/rawdb/accessors_ubt_sidecar.go` | New accessors |
| `core/blockchain.go` | Hook ApplyStateUpdate + reorg hook |
| `eth/api_debug.go` | Use sidecar triedb when ready |
| `trie/bintrie/*` | Add delete contract code helper |

---

## Verification Plan

### Unit Tests
- Conversion fails if any preimage missing
- ApplyStateUpdate handles:
  - balance/nonce changes with unchanged code
  - storage update/delete (RLP decode)
  - account deletion (storage wipe + code delete)

### Integration Tests
- Full sync with sidecar enabled on testnet
- Run validator (`docker-ubt-test/cmd/validate`) against sidecar node
- Compare UBT RPC output vs **local MPT** (no reference node)

---

## Usage Example

```
# Full sync with required flags
geth --syncmode full \
     --state.scheme=path \
     --cache.preimages \
     --ubt.sidecar

# After sync, run conversion
geth ubt convert --datadir /path/to/chaindata

# UBT RPC now available
# - debug_executionWitnessUBT
# - debug_getUBTProof
# - debug_getUBTState
```

---

## Key Differences: StateUseUBT vs UBT Sidecar

| Aspect | StateUseUBT (current) | UBT Sidecar (refined) |
|--------|-----------------------|------------------------|
| Consensus root | UBT root | MPT root (consensus) |
| Sync mode | Full only | Full only |
| State scheme | Path only | Path only |
| Preimages | Required | Required (hard error if missing) |
| Both states | No | Yes (MPT + UBT) |
| Reorg handling | depends | recover if possible, else stale |
| Header root override | Required | Not used |
