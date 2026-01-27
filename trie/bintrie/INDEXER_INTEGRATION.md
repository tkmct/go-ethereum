# UBT Data Sourcing for Indexers

This document describes how to source Unified Binary Trie (UBT) data to external indexer services that maintain flattened state databases. Indexers need to efficiently update their local databases for each new incoming block by applying state diffs.

## Overview

Indexers typically maintain a flattened key-value database where:
- **Keys**: 32-byte binary tree keys (as per EIP-7864)
- **Values**: 32-byte state values (account data, storage slots, code hashes, code chunks)

For each new block, indexers need:
1. **State Diffs**: A compact representation of what changed between the previous and current state
2. **Easy Application**: A straightforward way to apply these diffs to update their database

## Binary Tree Key Structure

All state data in UBT is accessed via 32-byte binary tree keys:

```
┌─────────────────────────────┬────────┐
│ Stem (31 bytes)             │ Suffix │
│ sha256([0...0][addr][key]) │ (1B)   │
└─────────────────────────────┴────────┘
```

**Key Types:**
- **BasicData** (suffix 0x00): Account nonce, balance, code size
- **CodeHash** (suffix 0x01): Contract code hash
- **Storage Slots** (suffix 0x02-0x3F): Storage slots 0-63
- **Code Chunks** (suffix 0x80-0xFF): Contract code chunks (31 bytes + metadata)

## Flattened State Format

The flattened state database stores all state as key-value pairs:

```go
type FlattenedState struct {
    // Map of binary tree key (32 bytes) -> value (32 bytes)
    State map[[32]byte][32]byte
}
```

**Example entries:**
```
Key: 0xabc...def (BasicData key for address 0x123...)
Value: 0x0000000000000001... (nonce=1, balance=..., codeSize=...)

Key: 0xabc...def01 (CodeHash key for same address)
Value: 0xdef...abc (keccak256 of contract code)

Key: 0xabc...def02 (Storage slot 0)
Value: 0x0000...0001 (storage value)
```

## State Diff Format

State diffs represent the changes between two consecutive block states. Each diff contains:

```go
type StateDiff struct {
    BlockNumber    uint64
    BlockHash      common.Hash
    ParentRoot     common.Hash  // State root before this block
    CurrentRoot    common.Hash  // State root after this block
    
    // Changed entries: key -> new value (nil means deleted)
    Updates map[[32]byte]*[32]byte
    
    // Optional: Original values for changed entries (for rollback)
    Origins map[[32]byte]*[32]byte
}
```

**Diff Semantics:**
- If `Updates[key] = nil`: Key was deleted (account self-destructed, storage cleared)
- If `Updates[key] = value`: Key was created or updated
- If key not in `Updates`: Key unchanged

## Data Sourcing Methods

### Method 1: Incremental State Diffs (Recommended)

**Approach:** Generate state diffs during block processing and publish them.

**Implementation Strategy:**

1. **During Block Processing:**
   - Track all trie nodes accessed/modified
   - For each modified StemNode, extract changed values
   - Build diff map: `key -> new_value`

2. **Diff Generation:**
   ```go
   // Pseudo-code for diff generation
   func GenerateStateDiff(prevRoot, newRoot common.Hash, 
                         modifiedNodes []*StemNode) *StateDiff {
       diff := &StateDiff{
           ParentRoot: prevRoot,
           CurrentRoot: newRoot,
           Updates: make(map[[32]byte]*[32]byte),
       }
       
       for _, stemNode := range modifiedNodes {
           stem := stemNode.Stem
           for suffix := 0; suffix < 256; suffix++ {
               if stemNode.Values[suffix] != nil {
                   key := BuildKey(stem, byte(suffix))
                   value := stemNode.Values[suffix]
                   diff.Updates[key] = &value
               }
           }
       }
       return diff
   }
   ```

3. **Publishing:**
   - Serialize diff to compact binary format (see Serialization Format)
   - Publish via:
     - **Pub/Sub**: Redis, Kafka, NATS, etc.
     - **RPC API**: HTTP/gRPC endpoint
     - **File-based**: Write to shared storage (S3, NFS)

**Advantages:**
- Minimal data transfer (only changes)
- Fast application (direct key-value updates)
- Supports incremental sync

**Disadvantages:**
- Requires tracking changes during execution
- More complex implementation

### Method 2: Full StemNode Snapshots

**Approach:** Publish complete StemNodes that were modified.

**Implementation Strategy:**

1. **During Block Processing:**
   - Identify all StemNodes that changed
   - Serialize entire StemNode (stem + all 256 values)

2. **Publishing:**
   ```go
   type StemNodeUpdate struct {
       Stem   [31]byte      // Stem key
       Values [256]*[32]byte // All values (nil = empty slot)
       BlockNumber uint64
   }
   ```

3. **Indexer Application:**
   - For each StemNodeUpdate:
     - For suffix 0-255:
       - If `Values[suffix] != nil`: Update `DB[BuildKey(stem, suffix)] = Values[suffix]`
       - If `Values[suffix] == nil` AND key exists in DB: Delete `DB[BuildKey(stem, suffix)]`

**Advantages:**
- Simpler to implement (just serialize StemNodes)
- Indexer can reconstruct full state for a stem
- Natural alignment with UBT structure

**Disadvantages:**
- Larger data transfer (includes unchanged values in stem)
- Indexer must handle partial updates

### Method 3: Witness-Based Extraction

**Approach:** Extract flattened state from witness data.

**Implementation Strategy:**

1. **Generate Witness:**
   - Use existing witness generation (already implemented)
   - Witness contains all accessed trie nodes

2. **Extract Flattened State:**
   ```go
   func ExtractFlattenedState(witness *stateless.Witness) map[[32]byte][32]byte {
       state := make(map[[32]byte][32]byte)
       
       for blob := range witness.State {
           node, _ := bintrie.DeserializeNode([]byte(blob), 0)
           if stemNode, ok := node.(*bintrie.StemNode); ok {
               stem := stemNode.Stem
               for suffix := 0; suffix < 256; suffix++ {
                   if stemNode.Values[suffix] != nil {
                       key := BuildKey(stem, byte(suffix))
                       state[key] = stemNode.Values[suffix]
                   }
               }
           }
       }
       return state
   }
   ```

3. **Compute Diff:**
   - Compare current witness state with previous block's state
   - Generate diff: `new_keys - old_keys` and `changed_values`

**Advantages:**
- Reuses existing witness infrastructure
- Complete state available for verification

**Disadvantages:**
- Witness contains only accessed nodes (not all modified nodes)
- Need to track state across blocks for diff computation

**Example of Modified but Not Accessed Nodes:**

Consider this scenario:

1. **Initial State**: The trie root is an `InternalNode` already loaded in memory (not a `HashedNode`):
   ```
   Root (InternalNode, depth=0)
   ├── left: StemNode_A (contains account 0xAAA...)
   └── right: StemNode_B (contains account 0xBBB...)
   ```

2. **Block Execution**: A transaction creates a new account `0xCCC...`:
   - The new account's key routes through the `right` branch of Root
   - `StemNode_B` is read from disk (via `nodeResolver`) → **added to witness**
   - `StemNode_B` is modified in memory to include the new account → **modified**
   - Root `InternalNode` is modified in memory (its `right` child changed) → **modified**
   - But Root was already in memory, so it was **never read via `nodeResolver`** → **NOT in witness**

3. **Result**:
   - **In Witness**: `StemNode_B` (was read from disk)
   - **Modified but NOT in Witness**: Root `InternalNode` (was already in memory)

This happens because:
- **Witness collection** occurs when nodes are *read from disk* via `nodeResolver()` (line 169 in `trie.go`)
- **Node modifications** happen in memory and don't trigger witness collection
- Nodes already loaded in memory (like the root) can be modified without being re-read

For indexers, this means:
- Witness-based extraction might miss some modified internal nodes
- You'd need to track all in-memory modifications separately, not just witness nodes
- Method 1 (tracking modifications during execution) is more complete

## Serialization Format

### State Diff Serialization

**Binary Format:**
```
┌──────────────┬──────────────┬──────────────┬──────────────┬──────────────┐
│ BlockNumber  │ BlockHash    │ ParentRoot   │ CurrentRoot  │ UpdateCount  │
│ (8 bytes)    │ (32 bytes)   │ (32 bytes)   │ (32 bytes)   │ (4 bytes)    │
└──────────────┴──────────────┴──────────────┴──────────────┴──────────────┘
┌──────────────────────────────────────────────────────────────────────────┐
│ Updates (repeated UpdateCount times)                                      │
│ ┌──────────────┬──────────┬──────────────┐                               │
│ │ Key (32B)    │ HasValue │ Value (32B)  │                               │
│ │              │ (1 byte) │ (if present) │                               │
│ └──────────────┴──────────┴──────────────┘                               │
└──────────────────────────────────────────────────────────────────────────┘
```

**JSON Format (alternative):**
```json
{
  "blockNumber": 12345,
  "blockHash": "0xabc...",
  "parentRoot": "0xdef...",
  "currentRoot": "0x123...",
  "updates": [
    {
      "key": "0xabc...def",
      "value": "0x123...456"  // null if deleted
    }
  ]
}
```

### StemNode Update Serialization

**Binary Format:**
```
┌──────────────┬──────────────┬──────────────┬──────────────┐
│ BlockNumber  │ Stem (31B)   │ Bitmap (32B) │ Values       │
│ (8 bytes)    │              │              │ (variable)   │
└──────────────┴──────────────┴──────────────┴──────────────┘

Bitmap: Bit i set → Values[i] is present
Values: Concatenated 32-byte values for non-nil slots
```

## Indexer Integration

### Database Schema

Indexers should maintain:

```sql
-- Flattened state table
CREATE TABLE state (
    key BINARY(32) PRIMARY KEY,
    value BINARY(32) NOT NULL,
    updated_at_block BIGINT NOT NULL,
    INDEX idx_block (updated_at_block)
);

-- Block metadata table
CREATE TABLE blocks (
    number BIGINT PRIMARY KEY,
    hash BINARY(32) NOT NULL,
    state_root BINARY(32) NOT NULL,
    parent_root BINARY(32) NOT NULL,
    processed_at TIMESTAMP NOT NULL
);
```

### Applying State Diffs

**Algorithm:**
```go
func ApplyStateDiff(db Database, diff *StateDiff) error {
    tx := db.Begin()
    defer tx.Rollback()
    
    for key, newValue := range diff.Updates {
        if newValue == nil {
            // Delete
            tx.Delete(key)
        } else {
            // Insert or update
            tx.Put(key, *newValue, diff.BlockNumber)
        }
    }
    
    // Update block metadata
    tx.PutBlock(diff.BlockNumber, diff.BlockHash, 
                diff.CurrentRoot, diff.ParentRoot)
    
    return tx.Commit()
}
```

### Handling Reorgs

**Rollback Strategy:**
1. Use `Origins` map in diff to restore previous values
2. Or query historical diffs and reverse-apply them
3. Or maintain snapshot at regular intervals (e.g., every 1000 blocks)

**Example:**
```go
func Rollback(db Database, fromBlock, toBlock uint64) error {
    // Fetch diffs in reverse order
    diffs := FetchDiffs(fromBlock, toBlock, reverse=true)
    
    for _, diff := range diffs {
        // Reverse-apply: restore origins
        for key, originValue := range diff.Origins {
            if originValue == nil {
                tx.Delete(key)
            } else {
                tx.Put(key, *originValue, diff.BlockNumber-1)
            }
        }
    }
    return nil
}
```

## Transport Mechanisms

### 1. Pub/Sub (Real-time)

**Redis Pub/Sub:**
```go
// Publisher
redis.Publish("ubt:state-diffs", serializedDiff)

// Subscriber
pubsub := redis.Subscribe("ubt:state-diffs")
for msg := range pubsub.Channel() {
    diff := DeserializeDiff(msg.Data)
    ApplyStateDiff(db, diff)
}
```

**Kafka:**
- Topic: `ubt-state-diffs`
- Partition by block number
- Consumer groups for parallel processing

### 2. RPC API (Pull-based)

**HTTP Endpoint:**
```
GET /api/v1/state-diff/{blockNumber}
Response: JSON StateDiff

GET /api/v1/state-diff/range?from={from}&to={to}
Response: Array of StateDiff
```

**gRPC:**
```protobuf
service UBTIndexer {
    rpc GetStateDiff(BlockNumber) returns (StateDiff);
    rpc StreamStateDiffs(BlockRange) returns (stream StateDiff);
}
```

### 3. File-based (Batch)

**Format:** One file per block or range
```
ubt-diffs/
  ├── 00000000-00000999.bin
  ├── 00001000-00001999.bin
  └── ...
```

**Indexer:**
- Poll for new files
- Download and process in batches
- Mark processed ranges

## Performance Considerations

### Diff Size Estimation

**Typical block:**
- ~100-1000 account updates
- ~1000-10000 storage updates
- Diff size: ~100KB - 1MB per block

**Peak block:**
- Large contract deployments
- Many storage updates
- Diff size: up to ~10MB per block

### Optimization Strategies

1. **Compression:**
   - Use gzip/snappy for serialized diffs
   - Typical compression ratio: 3-5x

2. **Batching:**
   - Combine multiple block diffs
   - Reduce network overhead
   - Trade-off: larger batches = more latency

3. **Delta Encoding:**
   - For StemNode updates, only send changed value slots
   - Use bitmap to indicate which slots changed

4. **Caching:**
   - Indexer caches frequently accessed keys
   - Reduces database lookups

## Verification

Indexers should verify state diffs:

1. **Root Verification:**
   ```go
   func VerifyDiff(diff *StateDiff, prevState map[[32]byte][32]byte) bool {
       // Apply diff to prevState
       newState := applyDiffToState(prevState, diff)
       
       // Rebuild trie from newState
       trie := BuildTrieFromFlattenedState(newState)
       
       // Verify root matches
       return trie.Hash() == diff.CurrentRoot
   }
   ```

2. **Witness Verification:**
   - Use witness to verify state transitions
   - Ensures diffs are consistent with block execution

**How Witness Verification Works:**

Witness verification allows you to independently verify that state diffs are correct by re-executing the block using only the witness data:

```
┌─────────────────────────────────────────────────────────┐
│ Step 1: Rebuild Pre-State from Witness                 │
│                                                         │
│ witness.MakeHashDB() creates an in-memory database     │
│ containing:                                             │
│   - All trie nodes from witness.State                  │
│   - All contract codes from witness.Codes               │
│   - All headers from witness.Headers                   │
│                                                         │
│ Then rebuild trie starting from witness.Root()         │
│ (parent block's state root)                            │
└─────────────────────────────────────────────────────────┘
                        ↓
┌─────────────────────────────────────────────────────────┐
│ Step 2: Re-execute Block Transactions                   │
│                                                         │
│ Execute all transactions from the block against the     │
│ rebuilt pre-state                                       │
│                                                         │
│ This produces:                                          │
│   - New state root                                      │
│   - Receipt root                                        │
└─────────────────────────────────────────────────────────┘
                        ↓
┌─────────────────────────────────────────────────────────┐
│ Step 3: Verify Results Match                            │
│                                                         │
│ Compare computed state root == block.StateRoot          │
│ Compare computed receipt root == block.ReceiptHash     │
│                                                         │
│ If they match: state diff is correct ✓                  │
│ If they differ: state diff is incorrect ✗              │
└─────────────────────────────────────────────────────────┘
```

**Why It's Needed:**

1. **Trustless Verification**: Indexers can verify state diffs without trusting the source. They independently re-execute the block and verify the results match.

2. **Detect Corruption**: If a state diff is corrupted or malicious, witness verification will fail because:
   - Missing nodes → trie rebuild fails
   - Incorrect node data → hash verification fails
   - Wrong state changes → computed root doesn't match

3. **Stateless Execution**: Enables stateless clients to verify blocks without maintaining full state. They only need the witness data.

4. **Cross-Validation**: Provides a way to cross-check state diffs against the actual block execution, ensuring consistency.

**Example Implementation:**

```go
// Pseudo-code for witness verification
func VerifyStateDiffWithWitness(diff *StateDiff, witness *stateless.Witness, block *types.Block) error {
    // Step 1: Rebuild pre-state from witness
    memdb := witness.MakeHashDB()
    db := state.NewDatabase(triedb.NewDatabase(memdb))
    statedb, _ := state.New(witness.Root(), db)
    
    // Step 2: Re-execute block
    processor := NewStateProcessor(chain)
    res, err := processor.Process(block, statedb, vmConfig)
    if err != nil {
        return fmt.Errorf("execution failed: %w", err)
    }
    
    // Step 3: Verify state root matches
    computedRoot := statedb.IntermediateRoot(config.IsEIP158(block.Number()))
    if computedRoot != block.Root() {
        return fmt.Errorf("state root mismatch: computed %x, block %x", 
                         computedRoot, block.Root())
    }
    
    // Step 4: Verify state diff matches execution
    // (Compare diff.Updates with actual state changes)
    
    return nil
}
```

**Limitations:**

- Witness only contains *accessed* nodes, not all modified nodes (as discussed earlier)
- Requires full block execution, which is computationally expensive
- Best used for verification rather than primary data sourcing

## Example: Complete Flow

```
┌─────────────┐
│ Block N     │
│ Processing  │
└──────┬──────┘
       │
       ├─→ Track modified StemNodes
       │
       ├─→ Generate StateDiff
       │
       └─→ Serialize & Publish
            │
            ▼
┌─────────────────┐
│ Pub/Sub / API   │
└──────┬──────────┘
       │
       ▼
┌─────────────────┐
│ Indexer         │
│ - Receive diff  │
│ - Apply to DB   │
│ - Verify root   │
└─────────────────┘
```

## On-Chain State Diffs

**Question: Is it possible to publish state diffs on-chain?**

**Short Answer:** Technically possible, but currently not implemented. It would require protocol changes and has significant cost trade-offs.

### Current State

1. **Infrastructure Exists:**
   - `ExecutionWitness` type already includes `StateDiff` (for Verkle trees)
   - Blocks have a `witness` field, but it's **not encoded in block body** (see `core/types/block.go:212-215`)
   - `ExecutableData` in beacon engine has optional `ExecutionWitness` field
   - Currently used for stateless execution, not stored on-chain

2. **Why Not Currently On-Chain:**
   - **Cost**: Storing state diffs on-chain would consume significant gas
   - **Size**: Typical state diff is 100KB-1MB per block (10MB+ for peak blocks)
   - **Not Required**: Consensus doesn't need state diffs, only state roots
   - **Optional Data**: Witness is optional metadata, not consensus-critical

### Approaches for On-Chain State Diffs

#### Approach 1: Include in Block Body (Protocol Change)

**How it would work:**
```go
// Hypothetical block structure
type Block struct {
    header       *Header
    transactions Transactions
    withdrawals  Withdrawals
    stateDiff    *StateDiff  // NEW: Encoded in block body
}
```

**Requirements:**
- EIP/protocol upgrade to add state diff to block encoding
- All nodes must accept/store state diffs
- Consensus rules: State diff must match block execution

**Trade-offs:**
- ✅ **Pros**: 
  - Trustless: State diffs cryptographically committed in blocks
  - Always available: No need for separate infrastructure
  - Verifiable: Can verify diffs match state root
- ❌ **Cons**:
  - **High cost**: ~100KB-1MB per block = significant gas costs
  - **Storage bloat**: All nodes must store diffs forever
  - **Protocol complexity**: Consensus-critical data increases attack surface
  - **Backward compatibility**: Requires hard fork

#### Approach 2: Store in Separate On-Chain Registry (Smart Contract)

**How it would work:**
- Deploy a smart contract that stores state diffs
- Block proposers submit diffs via transactions
- Indexers read from contract

**Example:**
```solidity
contract StateDiffRegistry {
    mapping(uint256 => bytes) public stateDiffs;  // blockNumber => diff
    
    function submitStateDiff(uint256 blockNumber, bytes calldata diff) external {
        require(msg.sender == block.coinbase, "Only miner");
        stateDiffs[blockNumber] = diff;
    }
}
```

**Trade-offs:**
- ✅ **Pros**:
  - No protocol change needed
  - Optional: Only interested parties pay gas
  - Flexible: Can add incentives/penalties
- ❌ **Cons**:
  - **Still expensive**: Gas costs for storing large diffs
  - **Not trustless**: Relies on miners to submit correctly
  - **Optional**: Not guaranteed to be present
  - **Centralization risk**: High gas costs favor large miners

#### Approach 3: Store Hash/Commitment On-Chain, Data Off-Chain

**How it would work:**
- Store only a hash/commitment of state diff in block
- Actual diff stored off-chain (IPFS, blob storage, etc.)
- Indexers verify hash matches before using diff

**Example:**
```go
type Header struct {
    // ... existing fields ...
    StateDiffHash *common.Hash `json:"stateDiffHash" rlp:"optional"`  // NEW
}
```

**Trade-offs:**
- ✅ **Pros**:
  - **Low cost**: Only 32 bytes per block
  - **Trustless commitment**: Hash proves diff exists and is correct
  - **Flexible storage**: Can use any off-chain storage
- ❌ **Cons**:
  - Still requires protocol change (smaller than Approach 1)
  - Off-chain data availability problem
  - Need fallback mechanisms if off-chain storage fails

#### Approach 4: Use EIP-4844 Blob Transactions

**How it would work:**
- Store state diffs in blob transactions (EIP-4844)
- Blobs are cheaper than calldata (~100x)
- Available for ~18 days, then pruned

**Trade-offs:**
- ✅ **Pros**:
  - **Lower cost**: Blobs are much cheaper than calldata
  - **Already available**: EIP-4844 is live
  - **Large capacity**: Up to ~128KB per blob
- ❌ **Cons**:
  - **Temporary**: Blobs pruned after ~18 days
  - **Not permanent**: Indexers must sync within window
  - **Multiple blobs**: Large diffs need multiple blob transactions

**How to Use Blob Transactions for State Diffs:**

See the detailed guide below: [Using Blob Transactions](#using-blob-transactions-for-state-diffs)

### Cost Analysis

**Typical Block:**
- State diff size: ~100KB - 1MB
- Gas cost (calldata): ~16 gas/byte = 1.6M - 16M gas
- ETH cost (at 20 gwei): ~0.032 - 0.32 ETH per block

**Peak Block:**
- State diff size: up to ~10MB
- Gas cost: ~160M gas
- ETH cost: ~3.2 ETH per block

**Blob Transaction (EIP-4844):**
- Blob cost: ~0.001 ETH per blob (~128KB)
- Multiple blobs needed for large diffs
- Much cheaper but temporary

**How to Use Blob Transactions:**

See the detailed guide below: [Using Blob Transactions for State Diffs](#using-blob-transactions-for-state-diffs)

### Recommendation

**For UBT/Indexers, off-chain approaches are recommended:**

1. **Primary**: Use off-chain pub/sub or API (as described in this doc)
   - No protocol changes needed
   - Flexible and efficient
   - Can optimize for indexer needs

2. **Optional Enhancement**: Store state diff hash in block header
   - Provides cryptographic commitment
   - Minimal cost (32 bytes)
   - Enables trustless verification
   - Requires protocol change but small impact

3. **Alternative**: Use blob transactions for temporary availability
   - Good for recent blocks
   - Indexers sync within 18-day window
   - Falls back to off-chain for older blocks

**On-chain storage of full state diffs is generally not recommended** due to:
- High cost
- Storage bloat
- Protocol complexity
- Better alternatives exist

## Future Enhancements

1. **Incremental Sync:**
   - Indexer requests diffs from last processed block
   - Supports catch-up after downtime

2. **Selective Sync:**
   - Indexer subscribes to specific address ranges
   - Reduces data transfer for specialized indexers

3. **Snapshot Sync:**
   - Periodic full state snapshots
   - Faster initial sync for new indexers

4. **Parallel Processing:**
   - Multiple indexers process different address ranges
   - Horizontal scaling

5. **On-Chain Commitments:**
   - Store state diff hash in block header
   - Enables trustless verification
   - Minimal protocol change required

---

## Using Blob Transactions for State Diffs

EIP-4844 blob transactions provide a cost-effective way to publish state diffs on-chain temporarily (~18 days). This section explains how to create and use blob transactions for state diff publishing.

### Blob Transaction Overview

**Key Properties:**
- **Blob size**: 131,072 bytes (128 KB) per blob
- **Max blobs per transaction**: Up to 6 blobs (768 KB total)
- **Max blobs per block**: Varies by network (typically 6-18)
- **Cost**: ~0.001 ETH per blob (much cheaper than calldata)
- **Retention**: ~18 days, then pruned
- **KZG commitments**: Cryptographic proofs ensure data integrity

### Step-by-Step Guide

#### Step 1: Prepare State Diff Data

```go
// Serialize your state diff
diff := &StateDiff{
    BlockNumber: 12345,
    BlockHash:   common.HexToHash("0xabc..."),
    ParentRoot:  common.HexToHash("0xdef..."),
    CurrentRoot: common.HexToHash("0x123..."),
    Updates:     map[[32]byte]*[32]byte{...},
}

// Serialize to binary format
diffBytes, err := SerializeStateDiff(diff)
if err != nil {
    return err
}

// Compress if desired (gzip/snappy)
compressed := compress(diffBytes)
```

#### Step 2: Split Data into Blobs

```go
import (
    "github.com/ethereum/go-ethereum/crypto/kzg4844"
)

const BlobSize = 131072 // 128 KB

func splitIntoBlobs(data []byte) []kzg4844.Blob {
    var blobs []kzg4844.Blob
    
    for offset := 0; offset < len(data); offset += BlobSize {
        var blob kzg4844.Blob
        copySize := BlobSize
        if offset+copySize > len(data) {
            copySize = len(data) - offset
        }
        copy(blob[:copySize], data[offset:offset+copySize])
        blobs = append(blobs, blob)
    }
    
    return blobs
}

// Split state diff into blobs
blobs := splitIntoBlobs(compressed)
```

#### Step 3: Generate KZG Commitments and Proofs

```go
import (
    "github.com/ethereum/go-ethereum/core/types"
    "github.com/ethereum/go-ethereum/crypto/kzg4844"
)

func createBlobSidecar(blobs []kzg4844.Blob) (*types.BlobTxSidecar, error) {
    commitments := make([]kzg4844.Commitment, len(blobs))
    proofs := make([]kzg4844.Proof, 0, len(blobs)*kzg4844.CellProofsPerBlob)
    
    // Generate commitment and proofs for each blob
    for i, blob := range blobs {
        // Create commitment
        commitment, err := kzg4844.BlobToCommitment(&blob)
        if err != nil {
            return nil, fmt.Errorf("failed to create commitment: %w", err)
        }
        commitments[i] = commitment
        
        // Generate cell proofs (version 1)
        cellProofs, err := kzg4844.ComputeCellProofs(&blob)
        if err != nil {
            return nil, fmt.Errorf("failed to compute proofs: %w", err)
        }
        proofs = append(proofs, cellProofs...)
    }
    
    // Create sidecar with version 1 (cell proofs)
    sidecar := types.NewBlobTxSidecar(
        types.BlobSidecarVersion1,
        blobs,
        commitments,
        proofs,
    )
    
    return sidecar, nil
}

sidecar, err := createBlobSidecar(blobs)
if err != nil {
    return err
}
```

#### Step 4: Create Blob Transaction

```go
import (
    "github.com/ethereum/go-ethereum/common"
    "github.com/ethereum/go-ethereum/core/types"
    "github.com/holiman/uint256"
)

func createBlobTx(
    chainID *big.Int,
    from common.Address,
    to common.Address,
    nonce uint64,
    gasFeeCap, gasTipCap, blobFeeCap *big.Int,
    sidecar *types.BlobTxSidecar,
) *types.Transaction {
    // Compute blob hashes from commitments
    blobHashes := sidecar.BlobHashes()
    
    // Create blob transaction
    blobTx := &types.BlobTx{
        ChainID:    uint256.MustFromBig(chainID),
        Nonce:      nonce,
        GasTipCap:  uint256.MustFromBig(gasTipCap),
        GasFeeCap:  uint256.MustFromBig(gasFeeCap),
        Gas:        21000, // Base gas for blob tx
        To:         to,
        Value:      uint256.NewInt(0), // No ETH transfer
        Data:       []byte{}, // Optional: can include metadata
        BlobFeeCap: uint256.MustFromBig(blobFeeCap),
        BlobHashes: blobHashes,
        Sidecar:    sidecar, // Attach sidecar for signing
    }
    
    return types.NewTx(blobTx)
}
```

#### Step 5: Sign and Send Transaction

```go
import (
    "github.com/ethereum/go-ethereum/core/types"
    "github.com/ethereum/go-ethereum/crypto"
)

func signAndSendBlobTx(
    tx *types.Transaction,
    chainID *big.Int,
    privateKey *ecdsa.PrivateKey,
    client *ethclient.Client,
) (common.Hash, error) {
    // Sign the transaction
    signer := types.NewCancunSigner(chainID)
    signedTx, err := types.SignTx(tx, signer, privateKey)
    if err != nil {
        return common.Hash{}, fmt.Errorf("failed to sign: %w", err)
    }
    
    // Send transaction
    err = client.SendTransaction(context.Background(), signedTx)
    if err != nil {
        return common.Hash{}, fmt.Errorf("failed to send: %w", err)
    }
    
    return signedTx.Hash(), nil
}
```

#### Step 6: Retrieve Blob Data (Indexer Side)

```go
func retrieveBlobData(
    client *ethclient.Client,
    txHash common.Hash,
) ([]byte, error) {
    // Get transaction receipt
    receipt, err := client.TransactionReceipt(context.Background(), txHash)
    if err != nil {
        return nil, err
    }
    
    // Get block to access blob sidecars
    block, err := client.BlockByHash(context.Background(), receipt.BlockHash)
    if err != nil {
        return nil, err
    }
    
    // Find the transaction in the block
    var blobTx *types.Transaction
    for _, tx := range block.Transactions() {
        if tx.Hash() == txHash {
            blobTx = tx
            break
        }
    }
    
    if blobTx == nil {
        return nil, errors.New("transaction not found in block")
    }
    
    // Get blob sidecar from block
    sidecar := blobTx.BlobTxSidecar()
    if sidecar == nil {
        return nil, errors.New("no blob sidecar found")
    }
    
    // Reconstruct data from blobs
    var data []byte
    for _, blob := range sidecar.Blobs {
        // Find actual data length (blobs are zero-padded)
        dataLen := len(blob)
        for i := len(blob) - 1; i >= 0; i-- {
            if blob[i] != 0 {
                dataLen = i + 1
                break
            }
        }
        data = append(data, blob[:dataLen]...)
    }
    
    return data, nil
}
```

### Best Practices

1. **Compression**: Always compress state diffs before splitting into blobs
   - Typical compression ratio: 3-5x
   - Reduces number of blobs needed

2. **Metadata in Transaction Data**: Store metadata in `tx.Data`:
   ```go
   metadata := struct {
       BlockNumber uint64
       BlockHash   common.Hash
       DiffSize    uint32
   }{...}
   tx.Data = encodeMetadata(metadata)
   ```

3. **Multiple Transactions**: For very large diffs (>768 KB):
   - Split across multiple blob transactions
   - Include sequence numbers in metadata
   - Indexers reassemble in order

4. **Error Handling**: 
   - Verify blob commitments match hashes
   - Check blob proofs are valid
   - Handle missing blobs gracefully

5. **Cost Optimization**:
   - Batch multiple small diffs into single blob
   - Use compression to reduce blob count
   - Monitor blob gas prices

### Limitations

- **Temporary Storage**: Blobs pruned after ~18 days
- **Size Limits**: Max 6 blobs per transaction (~768 KB)
- **Availability**: Indexers must sync within retention window
- **Gas Costs**: Still costs gas (though much less than calldata)

### Alternative: Hybrid Approach

Combine blob transactions with off-chain storage:

1. **Publish hash on-chain** (in blob transaction data)
2. **Store full diff off-chain** (IPFS, S3, etc.)
3. **Indexers**: 
   - Read hash from blob transaction
   - Fetch full diff from off-chain storage
   - Verify hash matches

This provides:
- ✅ On-chain commitment (trustless)
- ✅ Lower costs (small blob for hash only)
- ✅ Permanent storage (off-chain)
- ✅ Best of both worlds

## References

- [EIP-7864: Binary Tree State Representation](https://eips.ethereum.org/EIPS/eip-7864)
- [WITNESS.md](./WITNESS.md) - Witness format specification
- [trie/bintrie/key_encoding.go](./key_encoding.go) - Key encoding implementation
- [trie/bintrie/binary_node.go](./binary_node.go) - Node structure

