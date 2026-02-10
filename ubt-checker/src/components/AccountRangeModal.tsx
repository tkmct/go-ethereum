import React, { useState } from 'react';
import { FiCheck, FiCopy, FiSearch, FiX } from 'react-icons/fi';
import { BlockSelection, selectionToBlockRef } from './BlockSelector';
import { EndpointValues } from './EndpointForm';
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

function readLocalStorage<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key);
    if (!raw) {
      return fallback;
    }
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}

function readEndpoints(): EndpointValues {
  const stored = readLocalStorage<EndpointValues>('ubt-checker:endpoints', defaultEndpoints);
  return {
    mptUrl: stored.mptUrl ?? defaultEndpoints.mptUrl,
    ubtUrl: stored.ubtUrl ?? defaultEndpoints.ubtUrl,
    apiKey: stored.apiKey ?? defaultEndpoints.apiKey,
  };
}

function readBlockSelection(): BlockSelection {
  const stored = readLocalStorage<BlockSelection>('ubt-checker:block', defaultBlock);
  return {
    mode: stored.mode ?? defaultBlock.mode,
    value: stored.value ?? defaultBlock.value,
  };
}

type Props = {
  isOpen: boolean;
  onClose: () => void;
};

export default function AccountRangeModal({ isOpen, onClose }: Props) {
  const [rangeStatus, setRangeStatus] = useState<'idle' | 'loading' | 'success' | 'error'>('idle');
  const [rangeError, setRangeError] = useState<string | undefined>(undefined);
  const [rangeResult, setRangeResult] = useState<{
    root?: string;
    accounts: Record<string, { balance: string; nonce: number; codeHash?: string }>;
    next?: string;
  } | null>(null);
  const [copiedAddress, setCopiedAddress] = useState<string | null>(null);

  if (!isOpen) {
    return null;
  }

  const handleAccountRange = async () => {
    try {
      setRangeStatus('loading');
      setRangeError(undefined);
      setRangeResult(null);

      const endpoints = readEndpoints();
      const blockSelection = readBlockSelection();
      const client = createRpcClient({ name: 'UBT', url: endpoints.ubtUrl, apiKey: endpoints.apiKey });
      const blockRef = selectionToBlockRef(blockSelection);
      const blockParam = blockRefToParam(blockRef);

      const result = await client.call<{
        root?: string;
        accounts: Record<string, { balance: string; nonce: number; codeHash?: string }>;
        next?: string;
      }>('debug_accountRange', [blockParam, '0x', 20, false, true, false]);

      setRangeResult(result);
      setRangeStatus('success');
    } catch (err) {
      setRangeStatus('error');
      setRangeError((err as Error).message);
    }
  };

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="page-header">
          <h2>Find Addresses (debug_accountRange)</h2>
          <button type="button" className="ghost icon-button" onClick={onClose}>
            <FiX aria-hidden="true" />
            Close
          </button>
        </div>
        <div className="button-row">
          <button type="button" className="icon-button" onClick={handleAccountRange}>
            <FiSearch aria-hidden="true" />
            Fetch
          </button>
          <span className={`badge ${rangeStatus === 'success' ? 'teal' : rangeStatus === 'error' ? 'rose' : ''}`}>
            {rangeStatus}
          </span>
        </div>
        {rangeError && <p className="mono">{rangeError}</p>}
        {rangeResult && (
          <div className="diff">
            {Object.keys(rangeResult.accounts ?? {}).length === 0 && (
              <div className="mono">No addresses returned.</div>
            )}
            {Object.entries(rangeResult.accounts ?? {}).map(([addr, info]) => (
              <div key={addr} className="diff-row">
                <div className="address-row">
                  <div>
                    <span>Address</span>
                    <div className="mono">{addr}</div>
                  </div>
                  <button
                    type="button"
                    className="secondary icon-button"
                    onClick={() => {
                      navigator.clipboard.writeText(addr);
                      setCopiedAddress(addr);
                      window.setTimeout(() => setCopiedAddress(null), 1200);
                    }}
                  >
                    {copiedAddress === addr ? <FiCheck aria-hidden="true" /> : <FiCopy aria-hidden="true" />}
                    {copiedAddress === addr ? 'Copied' : 'Copy'}
                  </button>
                </div>
                {info.balance && <div className="mono">Balance: {info.balance}</div>}
                {Number.isFinite(info.nonce) && <div className="mono">Nonce: {info.nonce}</div>}
              </div>
            ))}
            {rangeResult.next && (
              <div className="mono">Next key: {rangeResult.next}</div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
