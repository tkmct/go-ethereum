import { sha256 } from './hash';

export const BasicDataLeafKey = 0;
export const CodeHashLeafKey = 1;

export function getBinaryTreeKey(address: Uint8Array, key32: Uint8Array): Uint8Array {
  if (key32.length !== 32) {
    throw new Error('key must be 32 bytes');
  }
  const prefix = new Uint8Array(12);
  const data = new Uint8Array(12 + 20 + 31);
  data.set(prefix, 0);
  data.set(address, 12);
  data.set(key32.slice(0, 31), 12 + 20);
  const hash = sha256(data);
  hash[31] = key32[31];
  return hash;
}

export function getBinaryTreeKeyBasicData(address: Uint8Array): Uint8Array {
  const key = new Uint8Array(32);
  key[31] = BasicDataLeafKey;
  return getBinaryTreeKey(address, key);
}

export function getBinaryTreeKeyStorageSlot(address: Uint8Array, key32: Uint8Array): Uint8Array {
  if (key32.length !== 32) {
    throw new Error('storage key must be 32 bytes');
  }
  const zero = new Uint8Array(31);
  const headerMatch = key32.slice(0, 31).every((b, i) => b === zero[i]) && key32[31] < 64;
  if (headerMatch) {
    const headerKey = new Uint8Array(32);
    headerKey[31] = 64 + key32[31];
    return getBinaryTreeKey(address, headerKey);
  }
  const storageKey = new Uint8Array(32);
  storageKey[0] = 1;
  storageKey.set(key32.slice(0, 31), 1);
  storageKey[31] = key32[31];
  return getBinaryTreeKey(address, storageKey);
}
