import { keccak_256 } from '@noble/hashes/sha3';
import { sha256 as sha256Hash } from '@noble/hashes/sha256';

export function keccak256(bytes: Uint8Array): Uint8Array {
  return keccak_256(bytes);
}

export function sha256(bytes: Uint8Array): Uint8Array {
  return sha256Hash(bytes);
}
