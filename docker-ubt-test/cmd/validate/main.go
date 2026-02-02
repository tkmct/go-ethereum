// Command validate checks UBT state consistency against a reference node.
package main

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/urfave/cli/v2"
)

func main() {
	log.SetDefault(log.NewLogger(log.NewTerminalHandler(os.Stdout, true)))

	app := &cli.App{
		Name:  "ubt-validator",
		Usage: "Validate UBT state against reference node",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "ubt-rpc",
				Usage:   "UBT node RPC endpoint",
				Value:   "http://localhost:8545",
				EnvVars: []string{"UBT_RPC"},
			},
			&cli.StringFlag{
				Name:     "reference-rpc",
				Usage:    "Reference node RPC endpoint (must support debug APIs)",
				Required: true,
				EnvVars:  []string{"REFERENCE_RPC"},
			},
			&cli.IntFlag{
				Name:  "account-samples",
				Usage: "Number of accounts to sample",
				Value: 30000,
			},
			&cli.IntFlag{
				Name:  "storage-samples",
				Usage: "Storage slots per contract",
				Value: 500,
			},
			&cli.Int64Flag{
				Name:  "seed",
				Usage: "Random seed for reproducibility",
				Value: time.Now().UnixNano(),
			},
			&cli.IntFlag{
				Name:  "transition-blocks",
				Usage: "Number of blocks to validate for state transition",
				Value: 5,
			},
			&cli.IntFlag{
				Name:  "witness-blocks",
				Usage: "Number of blocks to validate witnesses",
				Value: 5,
			},
			&cli.StringSliceFlag{
				Name:  "phases",
				Usage: "Phases to run (0,1,2,3,4,5 or all)",
				Value: cli.NewStringSlice("all"),
			},
		},
		Action: runValidator,
	}

	if err := app.Run(os.Args); err != nil {
		log.Crit("Validation failed", "error", err)
	}
}

func runValidator(c *cli.Context) error {
	ctx := context.Background()
	phases := parsePhases(c.StringSlice("phases"))

	v, err := NewValidator(c.String("ubt-rpc"), c.String("reference-rpc"))
	if err != nil {
		return err
	}
	defer v.Close()

	if err := v.LoadChainConfig(ctx); err != nil {
		log.Warn("Failed to load chain config; some phases may be unavailable", "err", err)
	}

	cfg := SamplingConfig{
		AccountCount:            c.Int("account-samples"),
		StorageSlotsPerContract: c.Int("storage-samples"),
		RandomSeed:              c.Int64("seed"),
		BatchSize:               100,
	}

	anchor, err := v.getAnchorBlock(ctx)
	if err != nil {
		return err
	}

	log.Info("Starting UBT validation", "phases", strings.Join(phasesOrder(phases), ","))

	if phases[0] {
		log.Info("Phase 0: Checking preconditions")
		if err := v.Phase0_PreconditionCheck(ctx, anchor); err != nil {
			return phaseError(0, err)
		}
	}
	if phases[1] {
		log.Info("Phase 1: Checking UBT status")
		if err := v.Phase1_UBTStatusCheck(ctx, anchor); err != nil {
			return phaseError(1, err)
		}
	}
	if phases[2] {
		log.Info("Phase 2: Validating sampled accounts")
		if err := v.Phase2_ValueValidation(ctx, anchor, cfg); err != nil {
			return phaseError(2, err)
		}
	}
	if phases[3] {
		log.Info("Phase 3: Validating state transitions")
		if err := v.Phase3_TransitionValidation(ctx, anchor, c.Int("transition-blocks"), cfg); err != nil {
			return phaseError(3, err)
		}
	}
	if phases[4] {
		log.Info("Phase 4: Validating witnesses")
		if err := v.Phase4_WitnessValidation(ctx, anchor, c.Int("witness-blocks")); err != nil {
			return phaseError(4, err)
		}
	}
	if phases[5] {
		log.Info("Phase 5: Validating RPC consistency")
		if err := v.Phase5_RPCConsistency(ctx, anchor); err != nil {
			return phaseError(5, err)
		}
	}

	log.Info("========================================")
	log.Info("VALIDATION PASSED")
	log.Info("========================================")
	return nil
}

func parsePhases(values []string) [6]bool {
	var selected [6]bool
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if v == "all" {
			for i := range selected {
				selected[i] = true
			}
			return selected
		}
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			switch part {
			case "0", "1", "2", "3", "4", "5":
				selected[part[0]-'0'] = true
			}
		}
	}
	return selected
}

func phasesOrder(phases [6]bool) []string {
	order := make([]string, 0, 6)
	for i := 0; i < 6; i++ {
		if phases[i] {
			order = append(order, string(rune('0'+i)))
		}
	}
	return order
}

func phaseError(phase int, err error) error {
	return &PhaseError{Phase: phase, Err: err}
}
