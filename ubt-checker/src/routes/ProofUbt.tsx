import React, { useState } from 'react';
import EndpointForm, { EndpointValues } from '../components/EndpointForm';
import BlockSelector, { BlockSelection, selectionToBlockRef } from '../components/BlockSelector';
import StorageKeyList from '../components/StorageKeyList';
import ResultPanel from '../components/ResultPanel';
import JsonViewer from '../components/JsonViewer';
import { useLocalStorage } from '../hooks/useLocalStorage';
import { bytesToHex, hexToBytes, strip0x } from '../lib/format';
import { sha256 } from '../lib/hash';
import { blockRefToParam, createRpcClient } from '../lib/rpc';
import { getBinaryTreeKeyBasicData } from '../lib/ubtKeys';
import { verifyUbtAccountProof, verifyUbtStorageProofs, UbtProof } from '../lib/ubtProof';

const defaultEndpoints: EndpointValues = {
  mptUrl: 'http://localhost:8545',
  ubtUrl: 'http://localhost:9545',
};

const defaultBlock: BlockSelection = {
  mode: 'latest',
  value: '',
};

const STEM_WIDTH = 256;
const ZERO32 = new Uint8Array(32);

type UbtRootLookup = {
  blockHash: string;
  blockNumber: string;
  ubtRoot: string;
  ok: boolean;
};

function normalizeHex(input: string): string {
  if (!input) {
    return '0x';
  }
  return input.startsWith('0x') ? input : `0x${input}`;
}

function isZero32(bytes: Uint8Array): boolean {
  return bytes.length === 32 && bytes.every((b) => b === 0);
}

function hashPair(left: Uint8Array, right: Uint8Array): Uint8Array {
  const data = new Uint8Array(64);
  data.set(left, 0);
  data.set(right, 32);
  return sha256(data);
}

function getBit(key: Uint8Array, index: number): number {
  const byteIndex = Math.floor(index / 8);
  const bitIndex = 7 - (index % 8);
  return (key[byteIndex] >> bitIndex) & 1;
}

function parseProof(proof: string[]) {
  if (proof.length === 0) {
    return { siblings: [] as Uint8Array[], hasStem: false as const };
  }
  if (proof.length < STEM_WIDTH + 1) {
    return { siblings: proof.map(hexToBytes), hasStem: false as const };
  }
  const stemIndex = proof.length - (STEM_WIDTH + 1);
  const siblings = proof.slice(0, stemIndex).map(hexToBytes);
  const stem = hexToBytes(proof[stemIndex]);
  if (stem.length !== 31) {
    throw new Error(`stem must be 31 bytes, got ${stem.length}`);
  }
  const values = proof.slice(stemIndex + 1).map(hexToBytes);
  if (values.length !== STEM_WIDTH) {
    throw new Error(`expected ${STEM_WIDTH} values, got ${values.length}`);
  }
  return { siblings, stem, values, hasStem: true as const };
}

function stemHash(stem: Uint8Array, values: Uint8Array[]): Uint8Array {
  let hashes = values.map((v) => {
    if (v.length === 0) {
      return ZERO32;
    }
    return sha256(v);
  });

  for (let level = 0; level < 8; level += 1) {
    const next: Uint8Array[] = [];
    for (let i = 0; i < hashes.length; i += 2) {
      const left = hashes[i];
      const right = hashes[i + 1];
      if (isZero32(left) && isZero32(right)) {
        next.push(ZERO32);
      } else {
        next.push(hashPair(left, right));
      }
    }
    hashes = next;
  }

  const finalData = new Uint8Array(31 + 1 + 32);
  finalData.set(stem, 0);
  finalData[31] = 0;
  finalData.set(hashes[0], 32);
  return sha256(finalData);
}

function computeRootWithPath(
  key: Uint8Array,
  siblings: { depth: number; hash: string }[],
  leafHash: Uint8Array,
  depthOffset = 0
): Uint8Array {
  if (siblings.length === 0) {
    return leafHash;
  }
  const ordered = [...siblings].sort((a, b) => a.depth - b.depth);
  let current = leafHash;
  for (let i = ordered.length - 1; i >= 0; i -= 1) {
    const depth = Number(ordered[i].depth) + depthOffset;
    if (depth < 0) {
      continue;
    }
    const sibling = hexToBytes(ordered[i].hash);
    const bit = getBit(key, depth);
    if (bit === 0) {
      current = hashPair(current, sibling);
    } else {
      current = hashPair(sibling, current);
    }
  }
  return current;
}

function debugAccountRoots(proof: UbtProof) {
  try {
    const parsed = parseProof(proof.accountProof);
    const key = getBinaryTreeKeyBasicData(hexToBytes(proof.address));
    const leaf = parsed.hasStem ? stemHash(parsed.stem, parsed.values) : ZERO32;
    const siblings = proof.accountProofPath ?? [];
    return {
      rootDepth0: bytesToHex(computeRootWithPath(key, siblings, leaf, 0)),
      rootDepthPlus1: bytesToHex(computeRootWithPath(key, siblings, leaf, 1)),
      rootDepthMinus1: bytesToHex(computeRootWithPath(key, siblings, leaf, -1)),
    };
  } catch (err) {
    return { error: (err as Error).message };
  }
}

export default function ProofUbt() {
  const [endpoints, setEndpoints] = useLocalStorage<EndpointValues>('ubt-checker:endpoints', defaultEndpoints);
  const [blockSelection, setBlockSelection] = useLocalStorage<BlockSelection>('ubt-checker:block', defaultBlock);
  const [address, setAddress] = useState('0x0000000000000000000000000000000000000000');
  const [storageKeys, setStorageKeys] = useState<string[]>(['']);
  const [status, setStatus] = useState<'idle' | 'loading' | 'success' | 'error'>('idle');
  const [error, setError] = useState<string | undefined>(undefined);
  const [proof, setProof] = useState<UbtProof | null>(null);
  const [ubtRootLookup, setUbtRootLookup] = useState<UbtRootLookup | null>(null);
  const [accountResult, setAccountResult] = useState<{ ok: boolean; errors: string[] } | null>(null);
  const [storageResult, setStorageResult] = useState<{ ok: boolean; errors: string[] } | null>(null);
  const [rootResult, setRootResult] = useState<{ ok: boolean; errors: string[] } | null>(null);
  const handleFetch = async () => {
    try {
      setStatus('loading');
      setError(undefined);
      setProof(null);
      setUbtRootLookup(null);
      setAccountResult(null);
      setStorageResult(null);
      setRootResult(null);

      const client = createRpcClient({ name: 'UBT', url: endpoints.ubtUrl });
      const keys = storageKeys.map(normalizeHex).filter((k) => k !== '0x');
      const blockRef = selectionToBlockRef(blockSelection);
      const blockParam = blockRefToParam(blockRef);

      const result = await client.call<UbtProof>('debug_getUBTProof', [address, keys, blockParam]);
      const rootLookup = await client.call<UbtRootLookup>('debug_getUBTRoot', [blockParam]);
      const stemIndex = result.accountProof.findIndex((hex) => {
        const clean = hex.startsWith('0x') ? hex.slice(2) : hex;
        return clean.length === 62; // 31 bytes
      });
      const valuesLength = stemIndex >= 0 ? result.accountProof.length - stemIndex - 1 : undefined;
      const depths = result.accountProofPath?.map((node) => node.depth) ?? [];
      const sortedDepths = [...depths].sort((a, b) => a - b);
      const minDepth = sortedDepths[0];
      const maxDepth = sortedDepths[sortedDepths.length - 1];
      const contiguous =
        sortedDepths.length > 0 && sortedDepths.every((d, i) => d === (minDepth ?? 0) + i);
      console.log('ubt proof debug', {
        accountProofLength: result.accountProof.length,
        stemIndex,
        valuesLength,
        accountProofPathLength: result.accountProofPath?.length,
        minDepth,
        maxDepth,
        contiguousDepths: contiguous,
      });
      console.log('ubt proof roots', debugAccountRoots(result));
      console.log('ubt root lookup', rootLookup);
      const account = verifyUbtAccountProof(result);
      const storage = verifyUbtStorageProofs(result);
      const rootErrors: string[] = [];
      if (!rootLookup.ok) {
        rootErrors.push('ubt root lookup missing for block');
      } else if (strip0x(rootLookup.ubtRoot).toLowerCase() !== strip0x(result.ubtRoot).toLowerCase()) {
        rootErrors.push(`ubt root mismatch: proof ${result.ubtRoot} vs lookup ${rootLookup.ubtRoot}`);
      }
      const rootCheck = { ok: rootErrors.length === 0, errors: rootErrors };

      setProof(result);
      setUbtRootLookup(rootLookup);
      setAccountResult(account);
      setStorageResult(storage);
      setRootResult(rootCheck);
      setStatus('success');
    } catch (err) {
      setStatus('error');
      const message = (err as Error).message;
      if (message.includes('key not found in trie')) {
        setError(
          `${message} (debug_getUBTProof only returns membership proofs; pick an address with non-zero balance/nonce/code)`
        );
      } else {
        setError(message);
      }
    }
  };

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h1>UBT Proof</h1>
          <p>Fetch and verify debug_getUBTProof against the UBT root.</p>
        </div>
        <span className="badge">debug_getUBTProof</span>
      </div>

      <EndpointForm values={endpoints} onChange={setEndpoints} />
      <BlockSelector value={blockSelection} onChange={setBlockSelection} />


      <div className="card">
        <div className="grid-2">
          <div className="field">
            <label>Address</label>
            <input value={address} onChange={(e) => setAddress(e.target.value)} />
          </div>
          <div className="field">
            <label>Actions</label>
            <div className="button-row">
              <button type="button" onClick={handleFetch}>Fetch + Verify</button>
            </div>
          </div>
        </div>
      </div>

      <StorageKeyList keys={storageKeys} onChange={setStorageKeys} />

      <ResultPanel title="Verification" status={status} error={error}>
        {accountResult && storageResult && (
          <div className="diff">
            <div className={`badge ${accountResult.ok ? 'teal' : 'rose'}`}>
              Account proof: {accountResult.ok ? 'valid' : 'invalid'}
            </div>
            {accountResult.errors.length > 0 && (
              <div className="mono">
                {accountResult.errors.map((err) => (
                  <div key={err}>{err}</div>
                ))}
              </div>
            )}
            <div className={`badge ${storageResult.ok ? 'teal' : 'rose'}`}>
              Storage proofs: {storageResult.ok ? 'valid' : 'invalid'}
            </div>
            {storageResult.errors.length > 0 && (
              <div className="mono">
                {storageResult.errors.map((err) => (
                  <div key={err}>{err}</div>
                ))}
              </div>
            )}
            {rootResult && (
              <>
                <div className={`badge ${rootResult.ok ? 'teal' : 'rose'}`}>
                  UBT root check: {rootResult.ok ? 'match' : 'mismatch'}
                </div>
                {rootResult.errors.length > 0 && (
                  <div className="mono">
                    {rootResult.errors.map((err) => (
                      <div key={err}>{err}</div>
                    ))}
                  </div>
                )}
              </>
            )}
            {ubtRootLookup && (
              <div className="mono mono-stack">
                <div>UBT root lookup: {ubtRootLookup.ok ? 'present' : 'missing'}</div>
                <div>block: {ubtRootLookup.blockNumber}</div>
                <div>hash: {ubtRootLookup.blockHash}</div>
                <div>ubtRoot: {ubtRootLookup.ubtRoot}</div>
              </div>
            )}
          </div>
        )}
      </ResultPanel>

      {proof && <JsonViewer data={proof} />}
    </div>
  );
}
