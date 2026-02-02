import { Buffer } from 'buffer';

export function strip0x(input: string): string {
  if (input.startsWith('0x') || input.startsWith('0X')) {
    return input.slice(2);
  }
  return input;
}

export function hexToBytes(hex: string): Uint8Array {
  const clean = strip0x(hex);
  if (clean.length === 0) {
    return new Uint8Array();
  }
  const padded = clean.length % 2 === 0 ? clean : `0${clean}`;
  return Uint8Array.from(Buffer.from(padded, 'hex'));
}

export function bytesToHex(bytes: Uint8Array): string {
  return `0x${Buffer.from(bytes).toString('hex')}`;
}

export function pad32(bytes: Uint8Array): Uint8Array {
  if (bytes.length > 32) {
    return bytes.slice(bytes.length - 32);
  }
  const out = new Uint8Array(32);
  out.set(bytes, 32 - bytes.length);
  return out;
}

export function toBigInt(hex: string): bigint {
  if (!hex || hex === '0x') {
    return 0n;
  }
  return BigInt(hex);
}

export function formatWei(hex: string): string {
  try {
    return toBigInt(hex).toString(10);
  } catch {
    return '0';
  }
}

export function formatBytes(hex: string, bytes = 32): string {
  const b = pad32(hexToBytes(hex));
  const slice = b.slice(32 - bytes);
  return bytesToHex(slice);
}
