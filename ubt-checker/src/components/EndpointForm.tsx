import React from 'react';

export type EndpointValues = {
  mptUrl: string;
  ubtUrl: string;
  apiKey: string;
};

type Props = {
  values: EndpointValues;
  onChange: (next: EndpointValues) => void;
};

export default function EndpointForm({ values, onChange }: Props) {
  return (
    <div className="card">
      <div className="grid-2">
        <div className="field">
          <label>MPT RPC URL</label>
          <input
            type="url"
            placeholder="http://localhost:8545"
            value={values.mptUrl}
            onChange={(e) => onChange({ ...values, mptUrl: e.target.value })}
          />
        </div>
        <div className="field">
          <label>UBT RPC URL</label>
          <input
            type="url"
            placeholder="http://localhost:9545"
            value={values.ubtUrl}
            onChange={(e) => onChange({ ...values, ubtUrl: e.target.value })}
          />
        </div>
      </div>
      <div className="field">
        <label>API Key</label>
        <input
          type="password"
          placeholder="Optional X-API-Key header"
          value={values.apiKey}
          onChange={(e) => onChange({ ...values, apiKey: e.target.value })}
        />
      </div>
    </div>
  );
}
