import { defineConfig, Plugin } from 'vite';
import react from '@vitejs/plugin-react';

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
        const headers: Record<string, string> = {
          'Content-Type': req.headers['content-type'] ?? 'application/json',
        };
        const apiKey = req.headers['x-api-key'] as string | undefined;
        if (apiKey) headers['X-API-Key'] = apiKey;
        try {
          const resp = await fetch(target, { method: 'POST', headers, body });
          res.statusCode = resp.status;
          res.setHeader('Content-Type', 'application/json');
          res.end(await resp.text());
        } catch (err) {
          res.statusCode = 502;
          res.end(JSON.stringify({ error: (err as Error).message }));
        }
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
