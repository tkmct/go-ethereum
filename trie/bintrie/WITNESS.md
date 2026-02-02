# BinaryTrie Witness Specification

This document describes the witness format for the Unified Binary Trie (UBT) implementation per [EIP-7864](https://eips.ethereum.org/EIPS/eip-7864).

## Overview

A **witness** contains all trie nodes accessed during block execution, enabling stateless verification without full state.

```go
type Witness struct {
    Headers []*types.Header     // Ancestor headers for BLOCKHASH opcode
    Codes   map[string]struct{} // Contract bytecodes (key = raw bytecode)
    State   map[string]struct{} // Serialized trie nodes (key = node blob)
}
```

## Node Types

### 1. InternalNode

Routes traversal based on key bit at current depth.

**Structure:**
```go
type InternalNode struct {
    left, right BinaryNode  // Child nodes
    depth       int         // Bit position (0-247)
}
```

**Serialized Format (65 bytes):**
```
┌──────────────┬─────────────────────┬─────────────────────┐
│ Type: 0x02   │ Left Hash (32)      │ Right Hash (32)     │
│ (1 byte)     │ sha256 of left      │ sha256 of right     │
└──────────────┴─────────────────────┴─────────────────────┘
```

**Hash Computation:**
```
Hash = sha256(left.Hash() || right.Hash())
```

---

### 2. StemNode

Holds up to 256 values sharing the same 31-byte stem.

**Structure:**
```go
type StemNode struct {
    Stem   []byte    // 31 bytes (key[:31])
    Values [][]byte  // 256 slots × 32 bytes each
    depth  int
}
```

**Serialized Format (64 to 8256 bytes):**
```
┌──────────────┬─────────────┬────────────────┬────────────────────┐
│ Type: 0x01   │ Stem (31)   │ Bitmap (32)    │ Values (variable)  │
│ (1 byte)     │ key[:31]    │ presence bits  │ 32 bytes per value │
└──────────────┴─────────────┴────────────────┴────────────────────┘

Bitmap: Bit i set → Values[i] is present
Values: Concatenated in index order, only non-nil slots
```

**Value Slot Assignments:**
| Slot | Content |
|------|---------|
| 0 | BasicData: nonce (8B) + balance (16B) + codeSize (4B) |
| 1 | CodeHash (32 bytes) |
| 2-63 | Reserved |
| 64-127 | Header storage slots (for storage keys 0-63) |
| 128-255 | Code chunks (31 bytes code + 1 byte metadata each) |

**Hash Computation:**
```
1. hash_i = sha256(Values[i]) for each non-nil value
2. merkle_root = MerkleTree(hash_0, hash_1, ..., hash_255)  // 8 levels
3. Hash = sha256(Stem || 0x00 || merkle_root)
```

---

### 3. HashedNode

Placeholder for unresolved nodes (hash only).

```go
type HashedNode common.Hash  // 32 bytes
```

**Not serialized to witness.** When encountered during traversal, resolved via database and the actual node is recorded.

---

### 4. Empty

Represents absence of data.

```go
type Empty struct{}
```

**Not serialized to witness.** Hash is `0x0000...0000`.

---

## Key Encoding (EIP-7864)

```go
func GetBinaryTreeKey(addr Address, key []byte) []byte {
    h := sha256(
        [12]byte{0}  ||  // 12 zero bytes
        addr[:]      ||  // 20 bytes address  
        key[:31]         // 31 bytes of key
    )
    h[31] = key[31]      // Last byte is suffix
    return h             // 32 bytes total
}
```

**Stem:** First 31 bytes of the tree key.
**Suffix:** Last byte (key[31]) indexes into StemNode.Values.

---

## Witness Collection

Nodes are recorded when loaded from database during trie operations:

```go
// trie/bintrie/trie.go
func (t *BinaryTrie) nodeResolver(path []byte, hash common.Hash) ([]byte, error) {
    blob, err := t.reader.Node(path, hash)
    t.tracer.Put(path, blob)  // ← Records to witness
    return blob, nil
}
```

The `PrevalueTracer` accumulates all loaded nodes:
```go
type PrevalueTracer struct {
    data map[string][]byte  // path → serialized node
}

func (t *BinaryTrie) Witness() map[string][]byte {
    return t.tracer.Values()
}
```

---

## Root Hash Verification

Given a witness, verify the pre-state root:

```
1. DESERIALIZE all nodes from witness.State

2. COMPUTE leaf hashes:
   For each StemNode:
     hash = sha256(stem || 0x00 || merkle(values))

3. COMPUTE internal hashes bottom-up:
   For each InternalNode:
     hash = sha256(left_hash || right_hash)
   
   Where child hash is either:
   - Computed from child node in witness, OR
   - Taken from serialized InternalNode (sibling branch)

4. VERIFY root:
   computed_root == witness.Headers[0].Root
```

**Key property:** Witness contains full nodes on accessed path, but only hashes for sibling branches. This is sufficient for Merkle verification.

```
              Root
             /    \
      [accessed]  [hash only]
          ↓           ↓
    InternalNode   0xabc...
       /    \
  StemNode  [hash only]
  (full)       ↓
            0xdef...
```

---

## Comparison: MPT vs BinaryTrie Witness

| Aspect | MPT | BinaryTrie |
|--------|-----|------------|
| Node encoding | RLP | Custom binary |
| Node types | fullNode (17), shortNode | InternalNode (2), StemNode (256) |
| Trie structure | Separate account + storage tries | Unified trie |
| Key encoding | keccak256(addr) | sha256(zeros \|\| addr \|\| key) |
| Witness key | Serialized node blob | Serialized node blob |

---

## Stateless Execution

```
┌─────────────────────────────────────────────────────────┐
│ 1. Receive block + witness                              │
│                                                         │
│ 2. Verify pre-state:                                    │
│    witness.Headers[0].Root == parent.StateRoot          │
│                                                         │
│ 3. Build in-memory trie from witness nodes              │
│                                                         │
│ 4. Execute transactions                                 │
│    - Read: lookup in witness trie                       │
│    - Write: modify in-memory trie                       │
│                                                         │
│ 5. Compute post-state root                              │
│                                                         │
│ 6. Verify: computed_root == block.StateRoot             │
└─────────────────────────────────────────────────────────┘
```

---

## References

- [EIP-7864: Binary Tree State Representation](https://eips.ethereum.org/EIPS/eip-7864)
- [trie/bintrie/binary_node.go](binary_node.go) - Serialization
- [trie/bintrie/trie.go](trie.go) - Witness collection
- [core/stateless/witness.go](../../core/stateless/witness.go) - Witness struct
