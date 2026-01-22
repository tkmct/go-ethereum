// Copyright 2026 The go-ethereum Authors
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
	"errors"

	"github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/sidecar"
	"github.com/urfave/cli/v2"
)

var (
	ubtConvertCommand = &cli.Command{
		Name:      "convert",
		Usage:     "Convert MPT state to UBT sidecar (requires --state.scheme=path)",
		ArgsUsage: " ",
		Action:    ubtConvert,
	}

	ubtCommand = &cli.Command{
		Name:        "ubt",
		Usage:       "UBT sidecar utilities",
		Subcommands: []*cli.Command{ubtConvertCommand},
	}
)

func ubtConvert(ctx *cli.Context) error {
	stack, _ := makeConfigNode(ctx)
	defer stack.Close()

	chaindb := utils.MakeChainDatabase(ctx, stack, false)
	defer chaindb.Close()

	scheme, err := rawdb.ParseStateScheme(ctx.String(utils.StateSchemeFlag.Name), chaindb)
	if err != nil {
		return err
	}
	if scheme != rawdb.PathScheme {
		return errors.New("ubt conversion requires --state.scheme=path")
	}

	head := rawdb.ReadHeadBlock(chaindb)
	if head == nil {
		return errors.New("no head block")
	}

	mptdb := utils.MakeTrieDatabase(ctx, stack, chaindb, false, false, false)
	defer mptdb.Close()

	sidecarDB := utils.MakeTrieDatabase(ctx, stack, chaindb, false, false, true)
	sc, err := sidecar.NewUBTSidecarWithTrieDB(chaindb, sidecarDB)
	if err != nil {
		sidecarDB.Close()
		return err
	}
	defer sc.Close()

	log.Info("Starting UBT sidecar conversion", "block", head.NumberU64(), "hash", head.Hash(), "root", head.Root())
	if err := sc.ConvertFromMPT(head.Root(), head.NumberU64(), head.Hash(), mptdb); err != nil {
		return err
	}
	log.Info("UBT sidecar conversion complete", "block", head.NumberU64(), "hash", head.Hash(), "ubtRoot", sc.CurrentRoot())
	return nil
}
