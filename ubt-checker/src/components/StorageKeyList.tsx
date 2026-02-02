import React from 'react';
import { FiTrash2, FiPlus } from 'react-icons/fi';

type Props = {
  keys: string[];
  onChange: (next: string[]) => void;
};

export default function StorageKeyList({ keys, onChange }: Props) {
  return (
    <div className="card">
      <div className="field">
        <label>Storage Slots</label>
      </div>
      <div className="diff">
        {keys.map((key, index) => (
          <div key={`${key}-${index}`} className="field">
            <div className="input-row">
              <input
                type="text"
                placeholder="0x..."
                value={key}
                onChange={(e) => {
                  const next = [...keys];
                  next[index] = e.target.value;
                  onChange(next);
                }}
              />
              <button
                type="button"
                className="secondary icon-button"
                onClick={() => {
                  const next = keys.filter((_, i) => i !== index);
                  onChange(next.length ? next : ['']);
                }}
              >
                <FiTrash2 aria-hidden="true" />
              </button>
            </div>
          </div>
        ))}
      </div>
      <div className="button-row">
        <button
          type="button"
          className="icon-button"
          onClick={() => onChange([...keys, ''])}
        >
          <FiPlus aria-hidden="true" />
          Add slot
        </button>
      </div>
    </div>
  );
}
