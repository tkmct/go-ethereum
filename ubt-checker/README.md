# UBT Checker

React UI for comparing MPT and UBT state, plus proof inspection/verification.

## Quick start

```bash
cd ubt-checker
npm install
npm run dev
```

Open the app and configure:
- **MPT RPC URL**: a reference node (standard geth) with debug enabled
- **UBT RPC URL**: a UBT sidecar-enabled node

## Features

- **Compare**: balance/code/storage via `eth_getBalance`, `eth_getCode`, `eth_getStorageAt` vs `debug_getUBTState`.
- **MPT Proof**: `eth_getProof` verified against the block state root.
- **UBT Proof**: `debug_getUBTProof` verified against the UBT root.
- **Witness**: `debug_executionWitness`, `debug_executionWitnessUBT`, plus `debug_verifyExecutionWitness` / `debug_verifyExecutionWitnessUBT` for stateless verification.

## Notes

- `eth_getProof` verification uses `@ethereumjs/trie`.
- `debug_getUBTProof` verification implements the binary trie proof format used by UBT.
- Witness verification uses stateless execution on the RPC node; the debug RPCs must be enabled.

## Test

```bash
npm run test
```

To regenerate UBT proof vectors:

```bash
go run ./scripts/gen-ubt-vectors.go > ./testdata/ubt_vectors.json
```
