import React, { useMemo } from 'react';

type Props = {
  data: unknown;
};

export default function JsonViewer({ data }: Props) {
  const formatted = useMemo(() => JSON.stringify(data, null, 2), [data]);

  return (
    <div className="card">
      <div className="button-row">
        <button
          type="button"
          className="secondary"
          onClick={() => navigator.clipboard.writeText(formatted)}
        >
          Copy JSON
        </button>
      </div>
      <pre className="json-viewer">{formatted}</pre>
    </div>
  );
}
