import React from 'react';
import { createRoot } from 'react-dom/client';
import { Buffer } from 'buffer';
import process from 'process';
import App from './App';
import './styles.css';

if (!window.Buffer) {
  window.Buffer = Buffer;
}
if (!globalThis.process) {
  globalThis.process = process as unknown as NodeJS.Process;
}

const container = document.getElementById('root');
if (!container) {
  throw new Error('Root container not found');
}

createRoot(container).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);
