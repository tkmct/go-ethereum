export type RpcEndpoint = {
  name: string;
  url: string;
  apiKey?: string;
};

export type BlockRef =
  | 'latest'
  | 'safe'
  | 'finalized'
  | { blockNumber: string }
  | { blockHash: string; requireCanonical?: boolean };

type JsonRpcResponse<T> = {
  jsonrpc: '2.0';
  id: number;
  result?: T;
  error?: { code: number; message: string; data?: unknown };
};

function resolveProxy(url: string): { url: string; extraHeaders: Record<string, string> } {
  if (import.meta.env.DEV) {
    try {
      const parsed = new URL(url);
      if (parsed.origin !== window.location.origin) {
        return { url: '/__proxy', extraHeaders: { 'X-Proxy-Target': url } };
      }
    } catch {}
  }
  return { url, extraHeaders: {} };
}

let nextId = 1;

export function createRpcClient(endpoint: RpcEndpoint) {
  return {
    async call<T>(method: string, params: unknown[]): Promise<T> {
      const body = {
        jsonrpc: '2.0',
        id: nextId++,
        method,
        params,
      };
      const headers: Record<string, string> = { 'Content-Type': 'application/json' };
      if (endpoint.apiKey) {
        headers['X-API-Key'] = endpoint.apiKey;
      }
      const proxy = resolveProxy(endpoint.url);
      Object.assign(headers, proxy.extraHeaders);
      const res = await fetch(proxy.url, {
        method: 'POST',
        headers,
        body: JSON.stringify(body),
      });
      if (!res.ok) {
        throw new Error(`RPC ${method} failed: ${res.status} ${res.statusText}`);
      }
      const json = (await res.json()) as JsonRpcResponse<T>;
      if (json.error) {
        throw new Error(`RPC ${method} error: ${json.error.message}`);
      }
      if (json.result === undefined) {
        throw new Error(`RPC ${method} returned no result`);
      }
      return json.result;
    },
  };
}

export async function callBatch<T>(
  endpoint: RpcEndpoint,
  calls: { method: string; params: unknown[] }[]
): Promise<T[]> {
  const payload = calls.map((call) => ({
    jsonrpc: '2.0',
    id: nextId++,
    method: call.method,
    params: call.params,
  }));
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (endpoint.apiKey) {
    headers['X-API-Key'] = endpoint.apiKey;
  }
  const proxy = resolveProxy(endpoint.url);
  Object.assign(headers, proxy.extraHeaders);
  const res = await fetch(proxy.url, {
    method: 'POST',
    headers,
    body: JSON.stringify(payload),
  });
  if (!res.ok) {
    throw new Error(`RPC batch failed: ${res.status} ${res.statusText}`);
  }
  const json = (await res.json()) as JsonRpcResponse<T>[];
  const byId = new Map(json.map((item) => [item.id, item]));
  return payload.map((req) => {
    const item = byId.get(req.id);
    if (!item) {
      throw new Error('RPC batch missing response');
    }
    if (item.error) {
      throw new Error(`RPC ${req.method} error: ${item.error.message}`);
    }
    if (item.result === undefined) {
      throw new Error(`RPC ${req.method} returned no result`);
    }
    return item.result;
  });
}

export function blockRefToParam(block: BlockRef): unknown {
  if (typeof block === 'string') {
    return block;
  }
  return block;
}
