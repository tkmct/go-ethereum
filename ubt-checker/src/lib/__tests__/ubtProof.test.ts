import { describe, expect, it } from 'vitest';
import vectors from '../../../testdata/ubt_vectors.json';
import { verifyUbtAccountProof, verifyUbtStorageProofs } from '../ubtProof';

const data = vectors as {
  address: string;
  balance: string;
  nonce: string;
  codeHash: string;
  accountProof: string[];
  storageProof: { key: string; value: string; proof: string[] }[];
  ubtRoot: string;
}[];

describe('verifyUbtProof', () => {
  it('verifies account and storage proofs', () => {
    for (const vector of data) {
      const account = verifyUbtAccountProof(vector);
      const storage = verifyUbtStorageProofs(vector);
      expect(account.ok).toBe(true);
      expect(storage.ok).toBe(true);
    }
  });
});
