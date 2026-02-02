import React from 'react';

type Props = {
  title: string;
  status: 'idle' | 'loading' | 'success' | 'error';
  error?: string;
  children?: React.ReactNode;
};

export default function ResultPanel({ title, status, error, children }: Props) {
  const badgeClass = status === 'error' ? 'badge rose' : status === 'success' ? 'badge teal' : 'badge';
  const badgeText = status === 'loading' ? 'Loading' : status === 'success' ? 'Success' : status === 'error' ? 'Error' : 'Idle';

  return (
    <div className="card">
      <div className="page-header">
        <h2>{title}</h2>
        <span className={badgeClass}>{badgeText}</span>
      </div>
      {error && <p className="mono">{error}</p>}
      {children}
    </div>
  );
}
