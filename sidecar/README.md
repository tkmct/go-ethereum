# UBT Sidecar Module

This module hosts the self-contained UBT sidecar implementation. Existing
packages should only call into the sidecar through a small interface; all UBT
conversion, update queueing, and state access live here.

## Dataflow: Conversion Phase

```
┌────────────────────────────┐
│   Canonical Block Import   │
└────────────────────────────┘
               │
               ▼
┌────────────────────────────┐
│     MPT State Execution     │
│   CommitWithUpdate* (MPT)   │
└────────────────────────────┘
         │                    │
         │ (enqueue updates)  │
         ▼                    ▼
┌────────────────────────────┐   ┌──────────────────────────────────┐
│  sidecar/UBTSidecar         │   │   sidecar Update Queue           │
│  Converting (MPT→UBT)       │   │  (StateUpdate snapshots)         │
└────────────────────────────┘   └──────────────────────────────────┘
         │
         ▼
┌────────────────────────────┐
│  Iterate MPT + Preimages    │
│  Build UBT nodes + root     │
└────────────────────────────┘
         │
         ▼
┌────────────────────────────┐
│  Sidecar triedb (UBT nodes) │
│  PathScheme + IsVerkle=true │
└────────────────────────────┘
         │
         ▼
┌────────────────────────────┐
│  Replay queued updates      │
│  until head, then Ready     │
└────────────────────────────┘
```

Notes:
- Missing preimages are a hard error (conversion fails, sidecar becomes stale).
- During conversion, UBT RPCs must return “sidecar not ready”.

## Dataflow: Steady State

```
┌────────────────────────────┐
│   Canonical Block Import   │
└────────────────────────────┘
               │
               ▼
┌────────────────────────────┐
│     MPT State Execution     │
│   CommitWithUpdate* (MPT)   │
└────────────────────────────┘
         │                    │
         │                    ▼
         │        ┌────────────────────────────┐
         │        │   sidecar/UBTSidecar       │
         │        │   ApplyStateUpdate (UBT)   │
         │        └────────────────────────────┘
         │                    │
         │                    ▼
         │        ┌────────────────────────────┐
         │        │  Sidecar triedb (UBT nodes) │
         │        └────────────────────────────┘
         │
         ▼
┌────────────────────────────┐
│ Existing RPCs (MPT-backed)  │
│ eth_get* / debug_*          │
└────────────────────────────┘

UBT-specific debug RPCs read from sidecar:
┌────────────────────────────┐
│ debug_getUBTProof           │
│ debug_executionWitnessUBT   │
│ debug_getUBTState           │
└────────────────────────────┘
```

Notes:
- Existing RPC methods remain MPT-backed.
- UBT data is exposed only via UBT-specific debug RPCs.

## Dataflow: Reorg Handling

```
┌────────────────────────────┐
│     Chain Reorg Detected    │
└────────────────────────────┘
               │
               ▼
┌────────────────────────────┐
│  Find canonical ancestor    │
│  (block hash + height)      │
└────────────────────────────┘
               │
               ▼
┌────────────────────────────┐
│  Lookup UBTBlockRoot(hash)  │
└────────────────────────────┘
        │                │
        │ found          │ missing
        ▼                ▼
┌────────────────────────────┐   ┌────────────────────────────┐
│ triedb.Recoverable(root)?   │   │  Mark sidecar stale         │
└────────────────────────────┘   └────────────────────────────┘
        │                │
        │ yes            │ no
        ▼                ▼
┌────────────────────────────┐   ┌────────────────────────────┐
│ triedb.Recover(root)        │   │  Mark sidecar stale         │
└────────────────────────────┘   └────────────────────────────┘
               │
               ▼
┌────────────────────────────┐
│ ApplyStateUpdate for new    │
│ canonical blocks            │
└────────────────────────────┘
```

Notes:
- Shallow reorgs recover via pathdb state history.
- Deep reorgs (or missing roots) mark sidecar stale and require reconversion.
