import React, { useState } from 'react';
import EndpointForm, { EndpointValues } from '../components/EndpointForm';
import BlockSelector, { BlockSelection, selectionToBlockRef } from '../components/BlockSelector';
import StorageKeyList from '../components/StorageKeyList';
import ResultPanel from '../components/ResultPanel';
import JsonViewer from '../components/JsonViewer';
import { useLocalStorage } from '../hooks/useLocalStorage';
import { blockRefToParam, createRpcClient } from '../lib/rpc';

const defaultEndpoints: EndpointValues = {
  mptUrl: 'http://localhost:8545',
  ubtUrl: 'http://localhost:9545',
  apiKey: '',
};

const defaultBlock: BlockSelection = {
  mode: 'latest',
  value: '',
};

type UbtProofResult = {
  address: string;
  balance?: string;
  nonce?: string;
  codeHash?: string;
  accountProof: string[] | Record<string, string>;
  accountProofPath?: { depth: number; hash: string }[];
  storageProof: { key: string; value?: string; proof: string[] | Record<string, string> }[];
  ubtRoot?: string;
  proofRoot?: string;
  root?: string;
  blockHash?: string;
  blockNumber?: string;
  stateRoot?: string;
};

function normalizeHex(input: string): string {
  if (!input) {
    return '0x';
  }
  return input.startsWith('0x') ? input : `0x${input}`;
}

function proofNodeCount(proof: string[] | Record<string, string>): number {
  return Array.isArray(proof) ? proof.length : Object.keys(proof).length;
}

export default function ProofUbt() {
  const [endpoints, setEndpoints] = useLocalStorage<EndpointValues>('ubt-checker:endpoints', defaultEndpoints);
  const [blockSelection, setBlockSelection] = useLocalStorage<BlockSelection>('ubt-checker:block', defaultBlock);
  const [address, setAddress] = useState('0x0000000000000000000000000000000000000000');
  const [storageKeys, setStorageKeys] = useState<string[]>(['']);
  const [status, setStatus] = useState<'idle' | 'loading' | 'success' | 'error'>('idle');
  const [error, setError] = useState<string | undefined>(undefined);
  const [proof, setProof] = useState<UbtProofResult | null>(null);

  const handleFetch = async () => {
    try {
      setStatus('loading');
      setError(undefined);
      setProof(null);

      const client = createRpcClient({ name: 'UBT', url: endpoints.ubtUrl, apiKey: endpoints.apiKey });
      const keys = storageKeys.map(normalizeHex).filter((k) => k !== '0x');
      const blockRef = selectionToBlockRef(blockSelection);
      const blockParam = blockRefToParam(blockRef);

      const result = await client.call<UbtProofResult>('debug_ubt_getProof', [address, keys, blockParam]);

      setProof(result);
      setStatus('success');
    } catch (err) {
      setStatus('error');
      const message = (err as Error).message;
      if (message.includes('key not found in trie')) {
        setError(
          `${message} (debug_ubt_getProof only returns membership proofs; pick an address with non-zero balance/nonce/code)`
        );
      } else {
        setError(message);
      }
    }
  };

  const accountNodeCount = proof ? proofNodeCount(proof.accountProof) : 0;
  const rootValue = proof?.ubtRoot ?? proof?.root ?? proof?.proofRoot;
  const storageProofs = proof?.storageProof ?? [];

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h1>UBT Proof</h1>
          <p>Fetch debug_ubt_getProof from the UBT daemon.</p>
        </div>
        <span className="badge">debug_ubt_getProof</span>
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
              <button type="button" onClick={handleFetch}>Fetch Proof</button>
            </div>
          </div>
        </div>
      </div>

      <StorageKeyList keys={storageKeys} onChange={setStorageKeys} />

      <ResultPanel title="Proof Result" status={status} error={error}>
        {proof && (
          <div className="diff">
            <div className="mono mono-stack">
              <div>Address: {proof.address}</div>
              {rootValue && <div>UBT Root: {rootValue}</div>}
              {proof.stateRoot && <div>State Root: {proof.stateRoot}</div>}
              {proof.blockNumber && <div>Block: {proof.blockNumber}</div>}
              {proof.blockHash && <div>Hash: {proof.blockHash}</div>}
              {proof.balance && <div>Balance: {proof.balance}</div>}
              {proof.nonce && <div>Nonce: {proof.nonce}</div>}
              {proof.codeHash && <div>Code Hash: {proof.codeHash}</div>}
              <div>Account proof nodes: {accountNodeCount}</div>
              {proof.accountProofPath && <div>Account proof path: {proof.accountProofPath.length} siblings</div>}
            </div>

            {storageProofs.length > 0 && (
              <>
                <h4>Storage Proofs</h4>
                {storageProofs.map((sp) => (
                  <div key={sp.key} className="mono mono-stack">
                    <div>Key: {sp.key}</div>
                    {sp.value && <div>Value: {sp.value}</div>}
                    <div>Proof nodes: {proofNodeCount(sp.proof)}</div>
                  </div>
                ))}
              </>
            )}
          </div>
        )}
      </ResultPanel>

      {proof && <JsonViewer data={proof} />}
    </div>
  );
}
