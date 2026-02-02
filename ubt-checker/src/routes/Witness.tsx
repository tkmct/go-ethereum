import React, { useState } from 'react';
import EndpointForm, { EndpointValues } from '../components/EndpointForm';
import BlockSelector, { BlockSelection, selectionToBlockRef } from '../components/BlockSelector';
import ResultPanel from '../components/ResultPanel';
import JsonViewer from '../components/JsonViewer';
import { useLocalStorage } from '../hooks/useLocalStorage';
import { blockRefToParam, createRpcClient } from '../lib/rpc';

const defaultEndpoints: EndpointValues = {
  mptUrl: 'http://localhost:8545',
  ubtUrl: 'http://localhost:9545',
};

const defaultBlock: BlockSelection = {
  mode: 'latest',
  value: '',
};

export default function Witness() {
  const [endpoints, setEndpoints] = useLocalStorage<EndpointValues>('ubt-checker:endpoints', defaultEndpoints);
  const [blockSelection, setBlockSelection] = useLocalStorage<BlockSelection>('ubt-checker:block', defaultBlock);
  const [status, setStatus] = useState<'idle' | 'loading' | 'success' | 'error'>('idle');
  const [error, setError] = useState<string | undefined>(undefined);
  const [standardWitness, setStandardWitness] = useState<unknown>(null);
  const [ubtWitness, setUbtWitness] = useState<unknown>(null);

  const handleFetch = async () => {
    try {
      setStatus('loading');
      setError(undefined);
      setStandardWitness(null);
      setUbtWitness(null);

      const mptClient = createRpcClient({ name: 'MPT', url: endpoints.mptUrl });
      const ubtClient = createRpcClient({ name: 'UBT', url: endpoints.ubtUrl });
      const blockRef = selectionToBlockRef(blockSelection);
      const blockParam = blockRefToParam(blockRef);

      const [std, ubt] = await Promise.all([
        mptClient.call<unknown>('debug_executionWitness', [blockParam]),
        ubtClient.call<unknown>('debug_executionWitnessUBT', [blockParam]),
      ]);

      setStandardWitness(std);
      setUbtWitness(ubt);
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
