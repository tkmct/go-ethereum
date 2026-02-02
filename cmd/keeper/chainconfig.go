// Copyright 2025 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/params"
)

// Helper function to create uint64 pointer
func u64(v uint64) *uint64 {
	return &v
}

// getChainConfig returns the appropriate chain configuration based on the chainID.
// Returns an error for unsupported chain IDs.
func getChainConfig(chainID uint64) (*params.ChainConfig, error) {
	switch chainID {
	case 0, params.MainnetChainConfig.ChainID.Uint64():
		return params.MainnetChainConfig, nil
	case params.SepoliaChainConfig.ChainID.Uint64():
		return params.SepoliaChainConfig, nil
	case params.HoodiChainConfig.ChainID.Uint64():
		return params.HoodiChainConfig, nil
	case 1338: // UBT test chain ID
		return getUBTTestChainConfig(), nil
	default:
		return nil, fmt.Errorf("unsupported chain ID: %d", chainID)
	}
}

// getUBTTestChainConfig returns a chain config with UBT enabled at genesis for testing.
func getUBTTestChainConfig() *params.ChainConfig {
	return &params.ChainConfig{
		ChainID:                 big.NewInt(1338),
		HomesteadBlock:          big.NewInt(0),
		EIP150Block:             big.NewInt(0),
		EIP155Block:             big.NewInt(0),
		EIP158Block:             big.NewInt(0),
		ByzantiumBlock:          big.NewInt(0),
		ConstantinopleBlock:     big.NewInt(0),
		PetersburgBlock:         big.NewInt(0),
		IstanbulBlock:           big.NewInt(0),
		MuirGlacierBlock:        big.NewInt(0),
		BerlinBlock:             big.NewInt(0),
		LondonBlock:             big.NewInt(0),
		Ethash:                  new(params.EthashConfig),
		ShanghaiTime:            u64(0),
		VerkleTime:              u64(0),
		TerminalTotalDifficulty: common.Big0,
		EnableVerkleAtGenesis:   true,
		BlobScheduleConfig: &params.BlobScheduleConfig{
			Verkle: params.DefaultPragueBlobConfig,
		},
	}
}
