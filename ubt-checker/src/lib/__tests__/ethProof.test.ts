import { describe, expect, it } from 'vitest';
import { Trie } from '@ethereumjs/trie';
import { encode as rlpEncode } from '@ethereumjs/rlp';
import { keccak256 } from '../hash';
import { bytesToHex, hexToBytes, pad32 } from '../format';
import { verifyEthGetProof } from '../ethProof';

function trimLeadingZeros(bytes: Uint8Array): Uint8Array {
  let i = 0;
  while (i < bytes.length && bytes[i] === 0) {
    i += 1;
  }
  return bytes.slice(i);
}

function encodeStorageValue(value: Uint8Array): Uint8Array {
  const trimmed = trimLeadingZeros(value);
  if (trimmed.length === 0) {
    return rlpEncode(new Uint8Array());
  }
  return rlpEncode(trimmed);
}

describe('verifyEthGetProof', () => {
  it('verifies account and storage proofs', async () => {
    const address = hexToBytes('0x0000000000000000000000000000000000001234');
    const accountKey = keccak256(address);

    const storageTrie = new Trie();
    const storageKey = pad32(hexToBytes('0x01'));
    const storageKeyHash = keccak256(storageKey);
    const storageValue = pad32(hexToBytes('0x05'));
    await storageTrie.put(storageKeyHash, encodeStorageValue(storageValue));

    const storageRoot = storageTrie.root();

    const nonce = pad32(hexToBytes('0x02'));
    const balance = pad32(hexToBytes('0x10'));
    const codeHash = pad32(hexToBytes('0xdeadbeef'));

    const accountRlp = rlpEncode([
      trimLeadingZeros(nonce),
      trimLeadingZeros(balance),
      storageRoot,
      codeHash,
    ]);

    const accountTrie = new Trie();
    await accountTrie.put(accountKey, accountRlp);

    const accountProof = await Trie.createProof(accountTrie, accountKey);
    const storageProof = await Trie.createProof(storageTrie, storageKeyHash);

    const proof = {
      address: bytesToHex(address),
      balance: bytesToHex(balance),
      nonce: bytesToHex(nonce),
      codeHash: bytesToHex(codeHash),
      storageHash: bytesToHex(storageRoot),
      accountProof: accountProof.map(bytesToHex),
      storageProof: [
        {
          key: bytesToHex(storageKey),
          value: bytesToHex(storageValue),
          proof: storageProof.map(bytesToHex),
        },
      ],
    };

    const result = await verifyEthGetProof(proof, bytesToHex(accountTrie.root()));
    expect(result.ok).toBe(true);
  });
});
