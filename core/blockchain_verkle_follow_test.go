package core

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

func newFollowTestChain(headRoot, execRoot common.Hash) *BlockChain {
	bc := &BlockChain{
		chainConfig: &params.ChainConfig{
			EnableVerkleAtGenesis: true,
		},
	}
	head := &types.Header{
		Number: big.NewInt(1),
		Root:   headRoot,
	}
	bc.currentBlock.Store(head)
	bc.setCurrentExecRoot(execRoot)
	return bc
}

func TestResolveStateRootHead(t *testing.T) {
	headRoot := common.HexToHash("0x1111")
	execRoot := common.HexToHash("0x2222")
	bc := newFollowTestChain(headRoot, execRoot)

	got, err := bc.resolveStateRoot(headRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != headRoot {
		t.Fatalf("resolved root mismatch: got %x want %x", got, headRoot)
	}
}

func TestResolveStateRootAny(t *testing.T) {
	headRoot := common.HexToHash("0x1111")
	execRoot := common.HexToHash("0x2222")
	other := common.HexToHash("0x3333")
	bc := newFollowTestChain(headRoot, execRoot)

	got, err := bc.resolveStateRoot(other)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != other {
		t.Fatalf("resolved root mismatch: got %x want %x", got, other)
	}
}

func TestResolveParentExecRootHeadOnly(t *testing.T) {
	headRoot := common.HexToHash("0x1111")
	execRoot := common.HexToHash("0x2222")
	bc := newFollowTestChain(headRoot, execRoot)

	head := bc.CurrentBlock()
	parent := types.CopyHeader(head)

	// In binary follow mode, resolveParentExecRoot returns the tracked
	// binary exec root, not the MPT root from the parent header.
	got, err := bc.resolveParentExecRoot(parent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != execRoot {
		t.Fatalf("resolved root mismatch: got %x want %x", got, execRoot)
	}

	// Even with a different parent header, the binary exec root is returned.
	parent.Number = big.NewInt(2)
	got2, err := bc.resolveParentExecRoot(parent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got2 != execRoot {
		t.Fatalf("resolved root mismatch: got %x want %x", got2, execRoot)
	}
}
