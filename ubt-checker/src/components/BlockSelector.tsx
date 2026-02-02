import React from 'react';
import { BlockRef } from '../lib/rpc';

export type BlockSelection = {
  mode: 'latest' | 'safe' | 'finalized' | 'number' | 'hash';
  value: string;
};

export function selectionToBlockRef(selection: BlockSelection): BlockRef {
  if (selection.mode === 'latest' || selection.mode === 'safe' || selection.mode === 'finalized') {
    return selection.mode;
  }
  if (selection.mode === 'hash') {
    return { blockHash: selection.value, requireCanonical: false };
  }
  return { blockNumber: selection.value };
}

type Props = {
  value: BlockSelection;
  onChange: (next: BlockSelection) => void;
};

export default function BlockSelector({ value, onChange }: Props) {
  return (
    <div className="card">
      <div className="grid-2">
        <div className="field">
          <label>Block Selector</label>
          <select
            value={value.mode}
            onChange={(e) => onChange({ ...value, mode: e.target.value as BlockSelection['mode'] })}
          >
            <option value="latest">latest</option>
            <option value="safe">safe</option>
            <option value="finalized">finalized</option>
            <option value="number">block number</option>
            <option value="hash">block hash</option>
          </select>
        </div>
        <div className="field">
          <label>Value</label>
          <input
            type="text"
            placeholder={value.mode === 'hash' ? '0x...' : value.mode === 'number' ? '0x or decimal' : 'auto'}
            value={value.mode === 'latest' || value.mode === 'safe' || value.mode === 'finalized' ? '' : value.value}
            onChange={(e) => onChange({ ...value, value: e.target.value })}
            disabled={value.mode === 'latest' || value.mode === 'safe' || value.mode === 'finalized'}
          />
        </div>
      </div>
    </div>
  );
}
