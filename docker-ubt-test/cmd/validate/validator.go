package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
)

// Validator coordinates UBT validation against a reference node.
type Validator struct {
	ubtRPC string
	refRPC string

	ubtClient *rpc.Client
	refClient *rpc.Client

	ubt *ethclient.Client
	ref *ethclient.Client

	chainConfig *params.ChainConfig
}

// NewValidator initializes the validator and RPC clients.
func NewValidator(ubtRPC, refRPC string) (*Validator, error) {
	ubtClient, err := rpc.Dial(ubtRPC)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to UBT RPC: %w", err)
	}
	refClient, err := rpc.Dial(refRPC)
	if err != nil {
		ubtClient.Close()
		return nil, fmt.Errorf("failed to connect to reference RPC: %w", err)
	}

	return &Validator{
		ubtRPC:    ubtRPC,
		refRPC:    refRPC,
		ubtClient: ubtClient,
		refClient: refClient,
		ubt:       ethclient.NewClient(ubtClient),
		ref:       ethclient.NewClient(refClient),
	}, nil
}

// Close releases RPC connections.
func (v *Validator) Close() {
	if v.ubtClient != nil {
		v.ubtClient.Close()
	}
	if v.refClient != nil {
		v.refClient.Close()
	}
}

// LoadChainConfig attempts to load chain config from the reference node.
func (v *Validator) LoadChainConfig(ctx context.Context) error {
	var info struct {
		Protocols map[string]json.RawMessage `json:"protocols"`
	}
	if err := v.refClient.CallContext(ctx, &info, "admin_nodeInfo"); err == nil {
		if raw, ok := info.Protocols["eth"]; ok {
			var ethInfo struct {
				Config *params.ChainConfig `json:"config"`
			}
			if err := json.Unmarshal(raw, &ethInfo); err == nil && ethInfo.Config != nil {
				v.chainConfig = ethInfo.Config
				return nil
			}
		}
	}

	chainID, err := v.ref.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("failed to read chain ID: %w", err)
	}
	cfg := chainConfigByID(chainID)
	if cfg == nil {
		return fmt.Errorf("unknown chain ID %s; enable admin_nodeInfo", chainID.String())
	}
	v.chainConfig = cfg
	return nil
}

func chainConfigByID(chainID *big.Int) *params.ChainConfig {
	switch chainID.String() {
	case params.MainnetChainConfig.ChainID.String():
		return params.MainnetChainConfig
	case params.SepoliaChainConfig.ChainID.String():
		return params.SepoliaChainConfig
	case params.HoleskyChainConfig.ChainID.String():
		return params.HoleskyChainConfig
	case params.HoodiChainConfig.ChainID.String():
		return params.HoodiChainConfig
	case params.AllEthashProtocolChanges.ChainID.String():
		return params.AllEthashProtocolChanges
	case params.AllCliqueProtocolChanges.ChainID.String():
		return params.AllCliqueProtocolChanges
	default:
		return nil
	}
}

func (v *Validator) requireChainConfig() (*params.ChainConfig, error) {
	if v.chainConfig == nil {
		return nil, fmt.Errorf("chain config unavailable; enable admin_nodeInfo or use known chain ID")
	}
	return v.chainConfig, nil
}

func (v *Validator) logAnchor(anchor *BlockAnchor) {
	log.Info("Using anchor block", "number", anchor.Number, "hash", anchor.Hash)
}
