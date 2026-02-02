import { Trie } from '@ethereumjs/trie';
import { decode as rlpDecode } from '@ethereumjs/rlp';
import { keccak256 as keccakBytes } from './hash';
import { bytesToHex, hexToBytes, pad32, strip0x, toBigInt } from './format';

export type EthStorageProof = {
  key: string;
  value: string;
  proof: string[];
};

export type EthGetProof = {
  address: string;
  balance: string;
  nonce: string;
  codeHash: string;
  storageHash: string;
  accountProof: string[];
  storageProof: EthStorageProof[];
};

function toProofNodes(proof: string[]): Uint8Array[] {
  return proof.map((p) => hexToBytes(p));
}

async function verifyProofWithFallback(
  root: Uint8Array,
  rawKey: Uint8Array,
  hashedKey: Uint8Array,
  proof: Uint8Array[]
): Promise<Uint8Array> {
  const trieHashed = new Trie();
  try {
    return (await trieHashed.verifyProof(root, hashedKey, proof)) ?? new Uint8Array();
  } catch {
    const trieRaw = new Trie({
      useKeyHashing: true,
      useKeyHashingFunction: keccakBytes,
    });
    return (await trieRaw.verifyProof(root, rawKey, proof)) ?? new Uint8Array();
  }
}

function rlpToHex(value: Uint8Array | Uint8Array[]): string {
  if (value instanceof Uint8Array) {
    return bytesToHex(value);
  }
  return bytesToHex(rlpEncodeBytes(value));
}

function rlpEncodeBytes(values: Uint8Array[]): Uint8Array {
  const total = values.reduce((sum, v) => sum + v.length, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const v of values) {
    out.set(v, offset);
    offset += v.length;
  }
  return out;
}

export async function verifyEthGetProof(
  proof: EthGetProof,
  stateRoot: string
): Promise<{ ok: boolean; accountOk: boolean; storage: { key: string; ok: boolean }[]; errors: string[] }> {
  const errors: string[] = [];
  let accountOk = false;
  const storageResults: { key: string; ok: boolean }[] = [];

  try {
    const addressBytes = hexToBytes(proof.address);
    const accountKey = keccakBytes(addressBytes);
    const accountProof = toProofNodes(proof.accountProof);

    const accountRlp = await verifyProofWithFallback(
      hexToBytes(stateRoot),
      addressBytes,
      accountKey,
      accountProof
    );
    if (!accountRlp || accountRlp.length === 0) {
      errors.push('account proof returned empty value');
    } else {
      const decoded = rlpDecode(accountRlp) as Uint8Array[];
      if (!Array.isArray(decoded) || decoded.length < 4) {
        errors.push('account RLP decode failed');
      } else {
        const nonceHex = rlpToHex(decoded[0]);
        const balanceHex = rlpToHex(decoded[1]);
        const storageRootHex = bytesToHex(decoded[2]);
        const codeHashHex = bytesToHex(decoded[3]);
        if (toBigInt(nonceHex) !== toBigInt(proof.nonce)) {
          errors.push('nonce mismatch');
        }
        if (toBigInt(balanceHex) !== toBigInt(proof.balance)) {
          errors.push('balance mismatch');
        }
        if (strip0x(storageRootHex).toLowerCase() !== strip0x(proof.storageHash).toLowerCase()) {
          errors.push('storageRoot mismatch');
        }
        if (strip0x(codeHashHex).toLowerCase() !== strip0x(proof.codeHash).toLowerCase()) {
          errors.push('codeHash mismatch');
        }
        accountOk = true;
      }
    }

    for (const storage of proof.storageProof) {
      try {
        const rawKey = hexToBytes(storage.key);
        const paddedKey = pad32(rawKey);
        const storageKey = keccakBytes(paddedKey);
        const storageProof = toProofNodes(storage.proof);
        const storageValue = await verifyProofWithFallback(
          hexToBytes(proof.storageHash),
          paddedKey,
          storageKey,
          storageProof
        );
        let decoded = new Uint8Array();
        if (storageValue && storageValue.length > 0) {
          const raw = rlpDecode(storageValue);
          if (raw instanceof Uint8Array) {
            decoded = raw;
          }
        }
        const storageHex = bytesToHex(pad32(decoded));
        const expected = bytesToHex(pad32(hexToBytes(storage.value)));
        const ok = strip0x(storageHex).toLowerCase() === strip0x(expected).toLowerCase();
        storageResults.push({ key: storage.key, ok });
        if (!ok) {
          errors.push(`storage mismatch for ${storage.key}`);
        }
      } catch (err) {
        storageResults.push({ key: storage.key, ok: false });
        errors.push(`storage proof failed for ${storage.key}: ${(err as Error).message}`);
      }
    }
  } catch (err) {
    errors.push((err as Error).message);
  }

  return { ok: errors.length === 0, accountOk, storage: storageResults, errors };
}
