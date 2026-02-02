# UBT Sidecar Module

This module hosts the self-contained UBT sidecar implementation. Existing
packages should only call into the sidecar through a small interface; all UBT
state access and updates live here.

## Dataflow: Genesis Seed

```
┌────────────────────────────┐
│     Genesis Initialization  │
│ (alloc + canonical hash 0)  │
└────────────────────────────┘
               │
               ▼
┌────────────────────────────┐
│  sidecar/UBTSidecar         │
│  Build from genesis alloc   │
└────────────────────────────┘
               │
               ▼
┌────────────────────────────┐
│  Sidecar triedb (UBT nodes) │
│  PathScheme + IsVerkle=true │
└────────────────────────────┘
```

Notes:
- Missing genesis state spec is a hard error (sidecar becomes stale).
- UBT RPCs are available once the genesis seed is complete.

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
