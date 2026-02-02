import React, { useState } from 'react';
import EndpointForm, { EndpointValues } from '../components/EndpointForm';
import BlockSelector, { BlockSelection, selectionToBlockRef } from '../components/BlockSelector';
import StorageKeyList from '../components/StorageKeyList';
import ResultPanel from '../components/ResultPanel';
import { useLocalStorage } from '../hooks/useLocalStorage';
import { createRpcClient, blockRefToParam } from '../lib/rpc';
import { keccak256 } from '../lib/hash';
import { bytesToHex, hexToBytes, pad32, strip0x, toBigInt } from '../lib/format';

const defaultEndpoints: EndpointValues = {
  mptUrl: 'http://localhost:8545',
  ubtUrl: 'http://localhost:9545',
};

const defaultBlock: BlockSelection = {
  mode: 'latest',
  value: '',
};

type MptResult = {
  balance: string;
  code: string;
  storage: Record<string, string>;
  stateRoot?: string;
};

type UbtResult = {
  address: string;
  balance: string;
  nonce: string;
  codeHash: string;
  codeSize: string;
  storage: Record<string, string>;
  stateRoot: string;
  ubtRoot: string;
};

function normalizeHex(input: string): string {
  if (!input) {
    return '0x';
  }
  return input.startsWith('0x') ? input : `0x${input}`;
}

function isHexValue(value: string): boolean {
  return value.startsWith('0x') || value.startsWith('0X');
}

function renderHexWithDecimal(value: string, label?: string): React.ReactNode {
  const displayValue = label ? `${label} ${value}` : value;
  if (!isHexValue(value)) {
    return <div className="mono">{displayValue}</div>;
  }
  let decimal: string | null = null;
  try {
    decimal = toBigInt(value).toString(10);
  } catch {
    decimal = null;
  }
  return (
    <div className="mono mono-stack">
      <div>{displayValue}</div>
      {decimal !== null && <div className="mono-sub">dec: {decimal}</div>}
    </div>
  );
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

async function fetchStateRoot(client: ReturnType<typeof createRpcClient>, block: BlockSelection): Promise<string | undefined> {
  if (block.mode === 'hash') {
    const blockData = await client.call<{ stateRoot?: string }>('eth_getBlockByHash', [block.value, false]);
    return blockData?.stateRoot;
  }
  const tag = block.mode === 'number' ? normalizeBlockNumber(block.value) : block.mode;
  const blockData = await client.call<{ stateRoot?: string }>('eth_getBlockByNumber', [tag, false]);
  return blockData?.stateRoot;
}

export default function Compare() {
  const [endpoints, setEndpoints] = useLocalStorage<EndpointValues>('ubt-checker:endpoints', defaultEndpoints);
  const [blockSelection, setBlockSelection] = useLocalStorage<BlockSelection>('ubt-checker:block', defaultBlock);
  const [address, setAddress] = useState('0x0000000000000000000000000000000000000000');
  const [storageKeys, setStorageKeys] = useState<string[]>(['']);
  const [status, setStatus] = useState<'idle' | 'loading' | 'success' | 'error'>('idle');
  const [error, setError] = useState<string | undefined>(undefined);
  const [mpt, setMpt] = useState<MptResult | null>(null);
  const [ubt, setUbt] = useState<UbtResult | null>(null);

  const handleCompare = async () => {
    try {
      setStatus('loading');
      setError(undefined);
      setMpt(null);
      setUbt(null);

      const clientMpt = createRpcClient({ name: 'MPT', url: endpoints.mptUrl });
      const clientUbt = createRpcClient({ name: 'UBT', url: endpoints.ubtUrl });
      const keys = storageKeys.map(normalizeHex).filter((k) => k !== '0x');
      const paddedKeys = keys.map((key) => bytesToHex(pad32(hexToBytes(key))));
      const blockRef = selectionToBlockRef(blockSelection);
      const blockParam = blockRefToParam(blockRef);

      const [balance, code] = await Promise.all([
        clientMpt.call<string>('eth_getBalance', [address, blockParam]),
        clientMpt.call<string>('eth_getCode', [address, blockParam]),
      ]);

      const storage: Record<string, string> = {};
      if (keys.length > 0) {
        const storageValues = await Promise.all(
          keys.map((key) => clientMpt.call<string>('eth_getStorageAt', [address, key, blockParam]))
        );
        paddedKeys.forEach((key, index) => {
          storage[key] = storageValues[index];
        });
      }

      const ubtState = await clientUbt.call<UbtResult>('debug_getUBTState', [address, keys, blockParam]);
      const stateRoot = await fetchStateRoot(clientMpt, blockSelection);

      setMpt({ balance, code, storage, stateRoot });
      setUbt(ubtState);
      setStatus('success');
    } catch (err) {
      setStatus('error');
      setError((err as Error).message);
    }
  };

  const codeHash = mpt?.code ? bytesToHex(keccak256(hexToBytes(mpt.code))) : undefined;
  const codeSize = mpt?.code ? ((strip0x(mpt.code).length / 2).toString() ?? '0') : undefined;

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h1>State Compare</h1>
          <p>Compare MPT-backed state with UBT sidecar state.</p>
        </div>
        <span className="badge">Dual Endpoint</span>
      </div>

      <EndpointForm values={endpoints} onChange={setEndpoints} />
      <BlockSelector value={blockSelection} onChange={setBlockSelection} />

      <div className="card">
        <div className="grid-2">
          <div className="field">
            <label>Address</label>
            <input
              type="text"
              value={address}
              onChange={(e) => setAddress(e.target.value)}
            />
          </div>
          <div className="field">
            <label>Actions</label>
            <div className="button-row">
              <button type="button" onClick={handleCompare}>Compare</button>
            </div>
          </div>
        </div>
      </div>

      <StorageKeyList keys={storageKeys} onChange={setStorageKeys} />

      <ResultPanel title="Compare Results" status={status} error={error}>
        {mpt && ubt && (
          <div className="result-grid">
            <div className="card">
              <h3>MPT</h3>
              <div className="diff">
                <div className="diff-row">
                  <span>Balance</span>
                  {renderHexWithDecimal(mpt.balance)}
                </div>
                <div className="diff-row">
                  <span>Code Size</span>
                  <div className="mono">{codeSize ?? '0'}</div>
                </div>
                <div className="diff-row">
                  <span>Code Hash</span>
                  <div className="mono">{codeHash ?? '0x'}</div>
                </div>
                <div className="diff-row">
                  <span>State Root</span>
                  <div className="mono">{mpt.stateRoot ?? 'unknown'}</div>
                </div>
              </div>
            </div>
            <div className="card">
              <h3>UBT</h3>
              <div className="diff">
                <div className="diff-row">
                  <span>Balance</span>
                  {renderHexWithDecimal(ubt.balance)}
                </div>
                <div className="diff-row">
                  <span>Nonce</span>
                  {renderHexWithDecimal(ubt.nonce)}
                </div>
                <div className="diff-row">
                  <span>Code Size</span>
                  {renderHexWithDecimal(ubt.codeSize)}
                </div>
                <div className="diff-row">
                  <span>Code Hash</span>
                  <div className="mono">{ubt.codeHash}</div>
                </div>
                <div className="diff-row">
                  <span>State Root</span>
                  <div className="mono">{ubt.stateRoot}</div>
                </div>
                <div className="diff-row">
                  <span>UBT Root</span>
                  <div className="mono">{ubt.ubtRoot}</div>
                </div>
              </div>
            </div>
            <div className="card">
              <h3>Storage</h3>
              <div className="diff">
                {Object.keys(mpt.storage).length === 0 && <div className="mono">No slots requested.</div>}
                {Object.keys(mpt.storage).map((key) => (
                  <div key={key} className="diff-row">
                    <span>{key}</span>
                    {renderHexWithDecimal(mpt.storage[key], 'MPT:')}
                    {renderHexWithDecimal(ubt.storage[key] ?? '0x', 'UBT:')}
                  </div>
                ))}
              </div>
            </div>
          </div>
        )}
      </ResultPanel>
    </div>
  );
}
