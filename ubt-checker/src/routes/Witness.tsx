import React, { useState } from 'react';
import EndpointForm, { EndpointValues } from '../components/EndpointForm';
import BlockSelector, { BlockSelection } from '../components/BlockSelector';
import ResultPanel from '../components/ResultPanel';
import JsonViewer from '../components/JsonViewer';
import { useLocalStorage } from '../hooks/useLocalStorage';
import { callBatch, createRpcClient, RpcEndpoint } from '../lib/rpc';

const defaultEndpoints: EndpointValues = {
  mptUrl: 'http://localhost:8545',
  ubtUrl: 'http://localhost:9545',
};

const defaultBlock: BlockSelection = {
  mode: 'latest',
  value: '',
};

const MAX_SCAN_DEPTH = 20;
const MAX_UBT_FALLBACK = 20;

type RpcBlock = {
  number?: string;
  hash?: string;
  transactions?: unknown[];
};

type ResolvedBlock = {
  number?: string;
  hash?: string;
};

type WitnessCallPlan = {
  mptMethod: 'debug_executionWitness' | 'debug_executionWitnessByHash';
  mptParam: unknown;
  ubtParam: unknown;
  resolved?: ResolvedBlock;
};

function hasTransactions(block: RpcBlock | null | undefined): boolean {
  return !!block && Array.isArray(block.transactions) && block.transactions.length > 0;
}

function isNoStatePathsError(err: unknown): boolean {
  const message = err instanceof Error ? err.message : String(err);
  return message.toLowerCase().includes('no state paths');
}

function parseBlockNumber(value?: string): bigint | null {
  if (!value) {
    return null;
  }
  try {
    return BigInt(value);
  } catch {
    return null;
  }
}

function formatWitnessError(err: unknown): string {
  const message = err instanceof Error ? err.message : String(err);
  if (message.toLowerCase().includes('no state paths')) {
    return `${message} (block has no state access; try a block with transactions)`;
  }
  return message;
}

function normalizeBlockNumberInput(value: string): string {
  if (value.startsWith('0x') || value.startsWith('0X')) {
    return value;
  }
  const parsed = BigInt(value);
  return `0x${parsed.toString(16)}`;
}

function normalizeHexHash(value: string): string {
  if (value.startsWith('0x') || value.startsWith('0X')) {
    return value;
  }
  return `0x${value}`;
}

async function findUbtWitnessFallback(
  client: ReturnType<typeof createRpcClient>,
  startNumberHex: string
): Promise<{ witness: unknown; blockNumber: string }> {
  const start = parseBlockNumber(startNumberHex);
  if (start === null) {
    throw new Error('Could not parse resolved block number for UBT fallback');
  }
  for (let i = 1; i <= MAX_UBT_FALLBACK; i += 1) {
    const n = start - BigInt(i);
    if (n < 0n) {
      break;
    }
    const numberHex = `0x${n.toString(16)}`;
    try {
      const witness = await client.call<unknown>('debug_executionWitnessUBT', [numberHex]);
      if (witness != null) {
        return { witness, blockNumber: numberHex };
      }
    } catch (err) {
      if (!isNoStatePathsError(err)) {
        throw err;
      }
    }
  }
  throw new Error(`UBT witness has no state paths within ${MAX_UBT_FALLBACK} blocks of ${startNumberHex}`);
}

async function resolveWitnessPlan(endpoint: RpcEndpoint, selection: BlockSelection): Promise<WitnessCallPlan> {
  if (selection.mode === 'number') {
    if (!selection.value) {
      throw new Error('Block number is required');
    }
    const numberHex = normalizeBlockNumberInput(selection.value);
    return {
      mptMethod: 'debug_executionWitness',
      mptParam: numberHex,
      ubtParam: numberHex,
      resolved: { number: numberHex },
    };
  }
  if (selection.mode === 'hash') {
    if (!selection.value) {
      throw new Error('Block hash is required');
    }
    const hash = normalizeHexHash(selection.value);
    let numberHex: string | undefined;
    try {
      const client = createRpcClient(endpoint);
      const block = await client.call<RpcBlock>('eth_getBlockByHash', [hash, false]);
      numberHex = block?.number;
    } catch {
      numberHex = undefined;
    }
    return {
      mptMethod: 'debug_executionWitnessByHash',
      mptParam: hash,
      ubtParam: { blockHash: hash, requireCanonical: false },
      resolved: { hash, number: numberHex },
    };
  }

  const client = createRpcClient(endpoint);
  const tagBlock = await client.call<RpcBlock>('eth_getBlockByNumber', [selection.mode, false]);
  if (!tagBlock || !tagBlock.hash) {
    throw new Error(`Block not found for ${selection.mode}`);
  }
  if (hasTransactions(tagBlock)) {
    if (!tagBlock.number) {
      throw new Error(`Block number missing for ${selection.mode}`);
    }
    return {
      mptMethod: 'debug_executionWitness',
      mptParam: tagBlock.number,
      ubtParam: tagBlock.number,
      resolved: { number: tagBlock.number, hash: tagBlock.hash },
    };
  }

  const start = parseBlockNumber(tagBlock.number);
  if (start === null) {
    throw new Error(`Could not parse block number for ${selection.mode}`);
  }

  const calls: { method: string; params: unknown[] }[] = [];
  for (let i = 1; i <= MAX_SCAN_DEPTH; i += 1) {
    const n = start - BigInt(i);
    if (n < 0n) {
      break;
    }
    calls.push({ method: 'eth_getBlockByNumber', params: [`0x${n.toString(16)}`, false] });
  }
  if (calls.length === 0) {
    throw new Error(`No recent blocks to scan from ${selection.mode}`);
  }

  const results = await callBatch<RpcBlock>(endpoint, calls);
  for (const block of results) {
    if (block?.hash && hasTransactions(block)) {
      if (!block.number) {
        throw new Error('Block number missing for resolved transaction block');
      }
      return {
        mptMethod: 'debug_executionWitness',
        mptParam: block.number,
        ubtParam: block.number,
        resolved: { number: block.number, hash: block.hash },
      };
    }
  }

  throw new Error(`No recent blocks with transactions (looked back ${calls.length} blocks from ${selection.mode}).`);
}

export default function Witness() {
  const [endpoints, setEndpoints] = useLocalStorage<EndpointValues>('ubt-checker:endpoints', defaultEndpoints);
  const [blockSelection, setBlockSelection] = useLocalStorage<BlockSelection>('ubt-checker:block', defaultBlock);
  const [status, setStatus] = useState<'idle' | 'loading' | 'success' | 'error'>('idle');
  const [error, setError] = useState<string | undefined>(undefined);
  const [mptError, setMptError] = useState<string | undefined>(undefined);
  const [ubtError, setUbtError] = useState<string | undefined>(undefined);
  const [standardWitness, setStandardWitness] = useState<unknown>(null);
  const [ubtWitness, setUbtWitness] = useState<unknown>(null);
  const [resolvedBlock, setResolvedBlock] = useState<ResolvedBlock | null>(null);
  const [ubtResolvedBlock, setUbtResolvedBlock] = useState<ResolvedBlock | null>(null);

  const handleFetch = async () => {
    try {
      setStatus('loading');
      setError(undefined);
      setMptError(undefined);
      setUbtError(undefined);
      setStandardWitness(null);
      setUbtWitness(null);
      setResolvedBlock(null);
      setUbtResolvedBlock(null);

      const mptClient = createRpcClient({ name: 'MPT', url: endpoints.mptUrl });
      const ubtClient = createRpcClient({ name: 'UBT', url: endpoints.ubtUrl });
      const resolveEndpoint =
        endpoints.ubtUrl && endpoints.ubtUrl !== endpoints.mptUrl
          ? { name: 'UBT', url: endpoints.ubtUrl }
          : { name: 'MPT', url: endpoints.mptUrl };

      const plan = await resolveWitnessPlan(resolveEndpoint, blockSelection);
      setResolvedBlock(plan.resolved ?? null);

      const [stdResult, ubtResult] = await Promise.allSettled([
        mptClient.call<unknown>(plan.mptMethod, [plan.mptParam]),
        ubtClient.call<unknown>('debug_executionWitnessUBT', [plan.ubtParam]),
      ]);

      let mptOk = false;
      let ubtOk = false;

      if (stdResult.status === 'fulfilled' && stdResult.value != null) {
        setStandardWitness(stdResult.value);
        mptOk = true;
      } else {
        const reason =
          stdResult.status === 'fulfilled'
            ? new Error('RPC debug_executionWitness returned empty result')
            : stdResult.reason;
        setMptError(formatWitnessError(reason));
      }

      if (ubtResult.status === 'fulfilled' && ubtResult.value != null) {
        setUbtWitness(ubtResult.value);
        ubtOk = true;
      } else {
        const reason =
          ubtResult.status === 'fulfilled'
            ? new Error('RPC debug_executionWitnessUBT returned empty result')
            : ubtResult.reason;
        if (isNoStatePathsError(reason) && plan.resolved?.number) {
          try {
            const fallback = await findUbtWitnessFallback(ubtClient, plan.resolved.number);
            setUbtWitness(fallback.witness);
            setUbtResolvedBlock({ number: fallback.blockNumber });
            ubtOk = true;
          } catch (fallbackErr) {
            setUbtError(formatWitnessError(fallbackErr));
          }
        } else {
          setUbtError(formatWitnessError(reason));
        }
      }

      if (mptOk || ubtOk) {
        setStatus('success');
        setError(undefined);
      } else {
        setStatus('error');
        setError('Both witness RPCs failed.');
      }
    } catch (err) {
      setStatus('error');
      setError(formatWitnessError(err));
    }
  };

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h1>Execution Witness</h1>
          <p>Fetch execution witnesses. Verification is not implemented yet.</p>
        </div>
        <span className="badge">RPC only</span>
      </div>

      <EndpointForm values={endpoints} onChange={setEndpoints} />
      <BlockSelector value={blockSelection} onChange={setBlockSelection} />

      <div className="card">
        <div className="button-row">
          <button type="button" onClick={handleFetch}>Fetch Witness</button>
          <button type="button" className="secondary">Verification TODO</button>
        </div>
      </div>

      <ResultPanel title="Witness Results" status={status} error={error}>
        <div className="diff">
          <div className="badge rose">Verification not implemented yet.</div>
          {resolvedBlock && (
            <div className="mono">
              Resolved block: {resolvedBlock.number ?? 'unknown'} {resolvedBlock.hash ?? ''}
            </div>
          )}
          {ubtResolvedBlock && (
            <div className="mono">UBT witness block: {ubtResolvedBlock.number ?? 'unknown'}</div>
          )}
          <div className={`badge ${mptError ? 'rose' : standardWitness ? 'teal' : ''}`}>
            MPT witness:{' '}
            {mptError ? 'error' : standardWitness ? 'ok' : status === 'loading' ? 'loading' : 'idle'}
          </div>
          {mptError && <div className="mono">{mptError}</div>}
          <div className={`badge ${ubtError ? 'rose' : ubtWitness ? 'teal' : ''}`}>
            UBT witness:{' '}
            {ubtError ? 'error' : ubtWitness ? 'ok' : status === 'loading' ? 'loading' : 'idle'}
          </div>
          {ubtError && <div className="mono">{ubtError}</div>}
        </div>
      </ResultPanel>

      {standardWitness && (
        <div className="page">
          <h2>debug_executionWitness</h2>
          <JsonViewer data={standardWitness} />
        </div>
      )}
      {ubtWitness && (
        <div className="page">
          <h2>debug_executionWitnessUBT</h2>
          <JsonViewer data={ubtWitness} />
        </div>
      )}
    </div>
  );
}
