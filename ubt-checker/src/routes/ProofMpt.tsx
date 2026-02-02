import React, { useState } from 'react';
import EndpointForm, { EndpointValues } from '../components/EndpointForm';
import BlockSelector, { BlockSelection } from '../components/BlockSelector';
import StorageKeyList from '../components/StorageKeyList';
import ResultPanel from '../components/ResultPanel';
import JsonViewer from '../components/JsonViewer';
import { useLocalStorage } from '../hooks/useLocalStorage';
import { createRpcClient } from '../lib/rpc';
import { verifyEthGetProof, EthGetProof } from '../lib/ethProof';

const defaultEndpoints: EndpointValues = {
  mptUrl: 'http://localhost:8545',
  ubtUrl: 'http://localhost:9545',
};

const defaultBlock: BlockSelection = {
  mode: 'latest',
  value: '',
};

function normalizeHex(input: string): string {
  if (!input) {
    return '0x';
  }
  return input.startsWith('0x') ? input : `0x${input}`;
}

function normalizeBlockNumber(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) {
    return 'latest';
  }
  if (trimmed.startsWith('0x')) {
    return trimmed;
  }
  try {
    return `0x${BigInt(trimmed).toString(16)}`;
  } catch {
    return trimmed;
  }
}

async function fetchHeader(
  client: ReturnType<typeof createRpcClient>,
  block: BlockSelection
): Promise<{ hash: string; stateRoot: string; number?: string } | null> {
  if (block.mode === 'hash') {
    const blockData = await client.call<{ stateRoot?: string; hash?: string; number?: string }>('eth_getBlockByHash', [
      block.value,
      false,
    ]);
    if (!blockData?.hash || !blockData?.stateRoot) {
      return null;
    }
    return { hash: blockData.hash, stateRoot: blockData.stateRoot, number: blockData.number };
  }
  const tag = block.mode === 'number' ? normalizeBlockNumber(block.value) : block.mode;
  const blockData = await client.call<{ stateRoot?: string; hash?: string; number?: string }>('eth_getBlockByNumber', [
    tag,
    false,
  ]);
  if (!blockData?.hash || !blockData?.stateRoot) {
    return null;
  }
  return { hash: blockData.hash, stateRoot: blockData.stateRoot, number: blockData.number };
}

export default function ProofMpt() {
  const [endpoints, setEndpoints] = useLocalStorage<EndpointValues>('ubt-checker:endpoints', defaultEndpoints);
  const [blockSelection, setBlockSelection] = useLocalStorage<BlockSelection>('ubt-checker:block', defaultBlock);
  const [address, setAddress] = useState('0x0000000000000000000000000000000000000000');
  const [storageKeys, setStorageKeys] = useState<string[]>(['']);
  const [status, setStatus] = useState<'idle' | 'loading' | 'success' | 'error'>('idle');
  const [error, setError] = useState<string | undefined>(undefined);
  const [proof, setProof] = useState<EthGetProof | null>(null);
  const [verification, setVerification] = useState<Awaited<ReturnType<typeof verifyEthGetProof>> | null>(null);

  const handleFetch = async () => {
    try {
      setStatus('loading');
      setError(undefined);
      setProof(null);
      setVerification(null);

      const client = createRpcClient({ name: 'MPT', url: endpoints.mptUrl });
      const keys = storageKeys.map(normalizeHex).filter((k) => k !== '0x');
      const header = await fetchHeader(client, blockSelection);
      if (!header) {
        throw new Error('Failed to fetch block header for proof verification');
      }
      const blockParam =
        blockSelection.mode === 'hash'
          ? { blockHash: header.hash, requireCanonical: true }
          : header.number ?? normalizeBlockNumber(blockSelection.value);

      const result = await client.call<EthGetProof>('eth_getProof', [address, keys, blockParam]);
      const verified = await verifyEthGetProof(result, header.stateRoot);

      setProof(result);
      setVerification(verified);
      setStatus('success');
    } catch (err) {
      setStatus('error');
      setError((err as Error).message);
    }
  };

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h1>MPT Proof</h1>
          <p>Fetch and verify eth_getProof against the state root.</p>
        </div>
        <span className="badge">eth_getProof</span>
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
        {verification && (
          <div className="diff">
            <div className={`badge ${verification.ok ? 'teal' : 'rose'}`}>
              {verification.ok ? 'Proof valid' : 'Proof invalid'}
            </div>
            {verification.errors.length > 0 && (
              <div className="mono">
                {verification.errors.map((err) => (
                  <div key={err}>{err}</div>
                ))}
              </div>
            )}
            <div className="diff">
              {verification.storage.map((item) => (
                <div key={item.key} className="diff-row">
                  <span>{item.key}</span>
                  <div className="mono">{item.ok ? 'ok' : 'mismatch'}</div>
                </div>
              ))}
            </div>
          </div>
        )}
      </ResultPanel>

      {proof && <JsonViewer data={proof} />}
    </div>
  );
}
