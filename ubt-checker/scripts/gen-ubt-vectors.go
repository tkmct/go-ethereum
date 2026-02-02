package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/trie/bintrie"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/holiman/uint256"
)

type storageProof struct {
	Key   string   `json:"key"`
	Value string   `json:"value"`
	Proof []string `json:"proof"`
}

type ubtVector struct {
	Address      string         `json:"address"`
	Balance      string         `json:"balance"`
	Nonce        string         `json:"nonce"`
	CodeHash     string         `json:"codeHash"`
	AccountProof []string       `json:"accountProof"`
	StorageProof []storageProof `json:"storageProof"`
	UbtRoot      string         `json:"ubtRoot"`
}

func proofHex(proof []hexutil.Bytes) []string {
	out := make([]string, len(proof))
	for i, p := range proof {
		out[i] = hexutil.Encode(p)
	}
	return out
}

func leafProof(bt *bintrie.BinaryTrie, targetKey []byte) ([]hexutil.Bytes, error) {
	it, err := bt.NodeIterator(nil)
	if err != nil {
		return nil, err
	}
	found := false
	for it.Next(true) {
		if it.Leaf() && string(it.LeafKey()) == string(targetKey) {
			found = true
			break
		}
	}
	if it.Error() != nil {
		return nil, it.Error()
	}
	if !found {
		return nil, fmt.Errorf("leaf not found")
	}
	proofBytes := it.LeafProof()
	out := make([]hexutil.Bytes, len(proofBytes))
	for i, p := range proofBytes {
		out[i] = p
	}
	return out, nil
}

func main() {
	db := rawdb.NewMemoryDatabase()
	triedb := triedb.NewDatabase(db, triedb.VerkleDefaults)
	bt, err := bintrie.NewBinaryTrie(types.EmptyBinaryHash, triedb)
	if err != nil {
		panic(err)
	}

	addr := common.HexToAddress("0x000000000000000000000000000000000000beef")
	balance := uint256.NewInt(123456)
	nonce := uint64(7)
	code := []byte{0x60, 0x00, 0x60, 0x01}
	codeHash := crypto.Keccak256Hash(code)

	acc := &types.StateAccount{
		Nonce:    nonce,
		Balance:  balance,
		CodeHash: codeHash.Bytes(),
	}
	if err := bt.UpdateAccount(addr, acc, len(code)); err != nil {
		panic(err)
	}
	// Add a second account to ensure the trie has internal nodes for proof generation.
	addr2 := common.HexToAddress("0x0000000000000000000000000000000000000001")
	acc2 := &types.StateAccount{
		Nonce:    1,
		Balance:  uint256.NewInt(1),
		CodeHash: common.Hash{}.Bytes(),
	}
	if err := bt.UpdateAccount(addr2, acc2, 0); err != nil {
		panic(err)
	}

	storageKey := common.HexToHash("0x01")
	storageValue := common.HexToHash("0x42")
	if err := bt.UpdateStorage(addr, storageKey.Bytes(), storageValue.Bytes()); err != nil {
		panic(err)
	}

	accountKey := bintrie.GetBinaryTreeKeyBasicData(addr)
	storageLeafKey := bintrie.GetBinaryTreeKeyStorageSlot(addr, storageKey.Bytes())

	accountProof, err := leafProof(bt, accountKey)
	if err != nil {
		panic(err)
	}
	storageProofBytes, err := leafProof(bt, storageLeafKey)
	if err != nil {
		panic(err)
	}

	vector := ubtVector{
		Address:      addr.Hex(),
		Balance:      hexutil.EncodeBig(balance.ToBig()),
		Nonce:        hexutil.EncodeUint64(nonce),
		CodeHash:     codeHash.Hex(),
		AccountProof: proofHex(accountProof),
		StorageProof: []storageProof{{
			Key:   storageKey.Hex(),
			Value: storageValue.Hex(),
			Proof: proofHex(storageProofBytes),
		}},
		UbtRoot: bt.Hash().Hex(),
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode([]ubtVector{vector}); err != nil {
		panic(err)
	}
}
