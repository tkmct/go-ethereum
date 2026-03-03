import { defineConfig, Plugin } from 'vite';
import react from '@vitejs/plugin-react';
import https from 'node:https';
import http from 'node:http';

function rpcProxy(): Plugin {
  return {
    name: 'rpc-proxy',
    configureServer(server) {
      server.middlewares.use('/__proxy', async (req, res) => {
        const target = req.headers['x-proxy-target'] as string | undefined;
        if (!target) {
          res.statusCode = 400;
          res.end('Missing X-Proxy-Target header');
          return;
        }
        const chunks: Buffer[] = [];
        for await (const chunk of req) chunks.push(chunk as Buffer);
        const body = Buffer.concat(chunks);
        const targetUrl = new URL(target);
        const headers: Record<string, string> = {
          'Content-Type': req.headers['content-type'] ?? 'application/json',
          'Host': targetUrl.host,
        };
        const apiKey = req.headers['x-api-key'] as string | undefined;
        if (apiKey) headers['X-API-Key'] = apiKey;
        const transport = targetUrl.protocol === 'https:' ? https : http;
        const proxyReq = transport.request(
          target,
          { method: 'POST', headers },
          (proxyRes) => {
            res.statusCode = proxyRes.statusCode ?? 502;
            res.setHeader('Content-Type', 'application/json');
            const resChunks: Buffer[] = [];
            proxyRes.on('data', (c) => resChunks.push(c));
            proxyRes.on('end', () => res.end(Buffer.concat(resChunks)));
          }
        );
        proxyReq.on('error', (err) => {
          res.statusCode = 502;
          res.end(JSON.stringify({ error: err.message }));
        });
        proxyReq.end(body);
      });
    },
  };
}

export default defineConfig({
  plugins: [react(), rpcProxy()],
  define: {
    global: 'globalThis',
  },
  resolve: {
    alias: {
      buffer: 'buffer/',
      process: 'process/browser',
    },
  },
});
