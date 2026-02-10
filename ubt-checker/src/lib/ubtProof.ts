import { bytesToHex, hexToBytes, pad32, strip0x, toBigInt } from './format';
import { sha256 } from './hash';
import { getBinaryTreeKeyBasicData, getBinaryTreeKeyStorageSlot } from './ubtKeys';

export type UbtStorageProof = {
  key: string;
  value: string;
  proof: string[];
  proofPath?: UbtProofSibling[];
};

export type UbtProof = {
  address: string;
  balance: string;
  nonce: string;
  codeHash: string;
  accountProof: string[];
  accountProofPath?: UbtProofSibling[];
  storageProof: UbtStorageProof[];
  ubtRoot: string;
  blockHash?: string;
  blockNumber?: string;
};

export type UbtProofSibling = {
  depth: number;
  hash: string;
};

const STEM_WIDTH = 256;
const ZERO32 = new Uint8Array(32);
const EMPTY_CODE_HASH = '0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470';

function isZero32(bytes: Uint8Array): boolean {
  return bytes.length === 32 && bytes.every((b) => b === 0);
}

function hashPair(left: Uint8Array, right: Uint8Array): Uint8Array {
  const data = new Uint8Array(64);
  data.set(left, 0);
  data.set(right, 32);
  return sha256(data);
}

function toBytes(hex: string): Uint8Array {
  return hexToBytes(hex);
}

type ParsedProof = {
  siblings: Uint8Array[];
  stem?: Uint8Array;
  values?: Uint8Array[];
  hasStem: boolean;
};

function parseProof(proof: string[]): ParsedProof {
  if (proof.length === 0) {
    return { siblings: [], hasStem: false };
  }
  if (proof.length < STEM_WIDTH + 1) {
    return { siblings: proof.map(toBytes), hasStem: false };
  }
  const stemIndex = proof.length - (STEM_WIDTH + 1);
  const siblings = proof.slice(0, stemIndex).map(toBytes);
  const stem = toBytes(proof[stemIndex]);
  if (stem.length !== 31) {
    throw new Error(`stem must be 31 bytes, got ${stem.length}`);
  }
  const values = proof.slice(stemIndex + 1).map(toBytes);
  if (values.length !== STEM_WIDTH) {
    throw new Error(`expected ${STEM_WIDTH} values, got ${values.length}`);
  }
  return { siblings, stem, values, hasStem: true };
}

function stemHash(stem: Uint8Array, values: Uint8Array[]): Uint8Array {
  let hashes = values.map((v) => {
    if (v.length === 0) {
      return ZERO32;
    }
    return sha256(v);
  });

  for (let level = 0; level < 8; level += 1) {
    const next: Uint8Array[] = [];
    for (let i = 0; i < hashes.length; i += 2) {
      const left = hashes[i];
      const right = hashes[i + 1];
      if (isZero32(left) && isZero32(right)) {
        next.push(ZERO32);
      } else {
        next.push(hashPair(left, right));
      }
    }
    hashes = next;
  }

  const finalData = new Uint8Array(31 + 1 + 32);
  finalData.set(stem, 0);
  finalData[31] = 0;
  finalData.set(hashes[0], 32);
  return sha256(finalData);
}

function getBit(key: Uint8Array, index: number): number {
  const byteIndex = Math.floor(index / 8);
  const bitIndex = 7 - (index % 8);
  return (key[byteIndex] >> bitIndex) & 1;
}

function computeRoot(key: Uint8Array, siblings: Uint8Array[], leafHash: Uint8Array): Uint8Array {
  let current = leafHash;
  for (let level = siblings.length - 1; level >= 0; level -= 1) {
    const sibling = siblings[level] ?? ZERO32;
    const bit = getBit(key, level);
    if (bit === 0) {
      current = hashPair(current, sibling);
    } else {
      current = hashPair(sibling, current);
    }
  }
  return current;
}

function computeRootWithPath(key: Uint8Array, siblings: UbtProofSibling[], leafHash: Uint8Array): Uint8Array {
  if (siblings.length === 0) {
    return leafHash;
  }
  const ordered = [...siblings].sort((a, b) => a.depth - b.depth);
  let current = leafHash;
  for (let i = ordered.length - 1; i >= 0; i -= 1) {
    const depth = Number(ordered[i].depth);
    const sibling = toBytes(ordered[i].hash);
    const bit = getBit(key, depth);
    if (bit === 0) {
      current = hashPair(current, sibling);
    } else {
      current = hashPair(sibling, current);
    }
  }
  return current;
}

function bytesEq(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) {
    return false;
  }
  for (let i = 0; i < a.length; i += 1) {
    if (a[i] !== b[i]) {
      return false;
    }
  }
  return true;
}

function isEmptyCodeHash(hash: string): boolean {
  const norm = strip0x(hash).toLowerCase();
  return norm === '' || norm === strip0x(EMPTY_CODE_HASH);
}

function isExpectedEmptyAccount(proof: UbtProof): boolean {
  return toBigInt(proof.balance) === 0n && toBigInt(proof.nonce) === 0n && isEmptyCodeHash(proof.codeHash);
}

function parseUint64(bytes: Uint8Array): bigint {
  if (bytes.length !== 8) {
    return 0n;
  }
  let value = 0n;
  for (const b of bytes) {
    value = (value << 8n) + BigInt(b);
  }
  return value;
}

function parseBalance(bytes: Uint8Array): bigint {
  if (bytes.length !== 16) {
    return 0n;
  }
  let value = 0n;
  for (const b of bytes) {
    value = (value << 8n) + BigInt(b);
  }
  return value;
}

export function verifyUbtAccountProof(proof: UbtProof): { ok: boolean; errors: string[] } {
  const errors: string[] = [];
  try {
    const parsed = parseProof(proof.accountProof);
    const address = hexToBytes(proof.address);
    const key = getBinaryTreeKeyBasicData(address);
    const usePath = proof.accountProofPath && proof.accountProofPath.length > 0;
    if (!parsed.hasStem) {
      const root = usePath
        ? computeRootWithPath(key, proof.accountProofPath ?? [], ZERO32)
        : computeRoot(key, parsed.siblings, ZERO32);
      const rootHex = bytesToHex(root);
      if (strip0x(rootHex).toLowerCase() !== strip0x(proof.ubtRoot).toLowerCase()) {
        errors.push(`ubt root mismatch: ${rootHex} vs ${proof.ubtRoot}`);
      }
      if (!isExpectedEmptyAccount(proof)) {
        errors.push('account missing but expected non-empty');
      }
      return { ok: errors.length === 0, errors };
    }

    const { siblings, stem, values } = parsed;
    const leaf = stemHash(stem, values);
    const root = usePath ? computeRootWithPath(key, proof.accountProofPath ?? [], leaf) : computeRoot(key, siblings, leaf);
    const rootHex = bytesToHex(root);
    if (strip0x(rootHex).toLowerCase() !== strip0x(proof.ubtRoot).toLowerCase()) {
      errors.push(`ubt root mismatch: ${rootHex} vs ${proof.ubtRoot}`);
    }

    const stemMatches = bytesEq(stem, key.slice(0, 31));
    const basic = values[0] ?? new Uint8Array();
    const codeHash = values[1] ?? new Uint8Array();
    const deletedMarker = values[10] ?? new Uint8Array();
    const isDeleted =
      basic.length === 32 && isZero32(basic) && codeHash.length === 32 && isZero32(codeHash) && deletedMarker.length > 0;
    const hasAccount = stemMatches && !isDeleted && (basic.length > 0 || codeHash.length > 0);
    if (!hasAccount) {
      if (!isExpectedEmptyAccount(proof)) {
        errors.push('account missing but expected non-empty');
      }
      return { ok: errors.length === 0, errors };
    }

    if (basic.length === 0) {
      errors.push('basic data leaf missing');
    } else {
      const nonce = parseUint64(basic.slice(8, 16));
      const balance = parseBalance(basic.slice(16, 32));
      if (nonce !== toBigInt(proof.nonce)) {
        errors.push('nonce mismatch');
      }
      if (balance !== toBigInt(proof.balance)) {
        errors.push('balance mismatch');
      }
    }

    if (codeHash.length === 0) {
      errors.push('codeHash leaf missing');
    } else if (strip0x(bytesToHex(codeHash)).toLowerCase() !== strip0x(proof.codeHash).toLowerCase()) {
      errors.push('codeHash mismatch');
    }
  } catch (err) {
    errors.push((err as Error).message);
  }
  return { ok: errors.length === 0, errors };
}

export function verifyUbtStorageProofs(proof: UbtProof): { ok: boolean; errors: string[] } {
  const errors: string[] = [];
  for (const storage of proof.storageProof) {
    try {
      const parsed = parseProof(storage.proof);
      const address = hexToBytes(proof.address);
      const keyBytes = pad32(hexToBytes(storage.key));
      const key = getBinaryTreeKeyStorageSlot(address, keyBytes);
      const usePath = storage.proofPath && storage.proofPath.length > 0;
      if (!parsed.hasStem) {
        const root = usePath
          ? computeRootWithPath(key, storage.proofPath ?? [], ZERO32)
          : computeRoot(key, parsed.siblings, ZERO32);
        const rootHex = bytesToHex(root);
        if (strip0x(rootHex).toLowerCase() !== strip0x(proof.ubtRoot).toLowerCase()) {
          errors.push(`storage root mismatch for ${storage.key}`);
        }
        const expected = pad32(hexToBytes(storage.value));
        if (!isZero32(expected)) {
          errors.push(`storage value missing for ${storage.key}`);
        }
        continue;
      }

      const { siblings, stem, values } = parsed;
      const leaf = stemHash(stem, values);
      const root = usePath
        ? computeRootWithPath(key, storage.proofPath ?? [], leaf)
        : computeRoot(key, siblings, leaf);
      const rootHex = bytesToHex(root);
      if (strip0x(rootHex).toLowerCase() !== strip0x(proof.ubtRoot).toLowerCase()) {
        errors.push(`storage root mismatch for ${storage.key}`);
      }
      const stemMatches = bytesEq(stem, key.slice(0, 31));
      const expected = pad32(hexToBytes(storage.value));
      if (!stemMatches) {
        if (!isZero32(expected)) {
          errors.push(`storage value missing for ${storage.key}`);
        }
        continue;
      }
      const index = key[31];
      const actual = values[index] ?? new Uint8Array();
      if (actual.length === 0) {
        if (!isZero32(expected)) {
          errors.push(`storage value missing for ${storage.key}`);
        }
      } else if (!bytesEq(pad32(actual), expected)) {
        errors.push(`storage value mismatch for ${storage.key}`);
      }
    } catch (err) {
      errors.push(`storage proof error for ${storage.key}: ${(err as Error).message}`);
    }
  }
  return { ok: errors.length === 0, errors };
}
