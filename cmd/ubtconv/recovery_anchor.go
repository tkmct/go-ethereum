// Copyright 2024 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
)

const (
	recoveryAnchorFormatVersion = uint16(1)
	recoveryAnchorManifestFile  = "manifest.rlp"
)

func (c *Consumer) recoveryRootDir() string {
	return filepath.Join(c.cfg.DataDir, "recovery")
}

func (c *Consumer) recoveryAnchorsDir() string {
	return filepath.Join(c.recoveryRootDir(), "anchors")
}

func (c *Consumer) recoveryStagingDir(anchorID uint64) string {
	return filepath.Join(c.recoveryRootDir(), "staging", fmt.Sprintf("%020d.tmp", anchorID))
}

func (c *Consumer) recoveryAnchorDir(anchorID uint64) string {
	return filepath.Join(c.recoveryAnchorsDir(), fmt.Sprintf("%020d", anchorID))
}

func (c *Consumer) recoveryAnchorTrieDir(anchorID uint64) string {
	return filepath.Join(c.recoveryAnchorDir(anchorID), "triedb")
}

func clampGauge(v uint64) int64 {
	const maxInt64 = ^uint64(0) >> 1
	if v > maxInt64 {
		return int64(maxInt64)
	}
	return int64(v)
}

func truncateFailureReason(reason string) string {
	const maxLen = 512
	if len(reason) <= maxLen {
		return reason
	}
	return reason[:maxLen]
}

func writeRecoveryAnchorManifestFile(dir string, manifest *rawdb.UBTRecoveryAnchorManifest) error {
	data, err := rlp.EncodeToBytes(manifest)
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	path := filepath.Join(dir, recoveryAnchorManifestFile)
	tmp := path + ".tmp"

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create temp manifest: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write temp manifest: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync temp manifest: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp manifest: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename manifest: %w", err)
	}
	return nil
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyDirRecursive(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("source is not a directory: %s", src)
	}
	if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
		return err
	}

	return filepath.Walk(src, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// LevelDB lock files are process-local and must not be copied.
		if !fi.IsDir() && filepath.Base(path) == "LOCK" {
			return nil
		}
		target := filepath.Join(dst, rel)
		switch {
		case fi.IsDir():
			return os.MkdirAll(target, fi.Mode().Perm())
		case fi.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case fi.Mode().IsRegular():
			return copyFile(path, target, fi.Mode().Perm())
		default:
			return nil
		}
	})
}

func (c *Consumer) verifyRecoveryAnchorData(anchorDataDir string, expectedRoot common.Hash) error {
	verifyCfg := *c.cfg
	verifyCfg.DataDir = anchorDataDir
	applier, err := NewApplier(&verifyCfg, expectedRoot)
	if err != nil {
		return err
	}
	applier.Close()
	return nil
}

func (c *Consumer) markRecoveryAnchorBroken(anchorID uint64, reason error) {
	manifest := rawdb.ReadUBTRecoveryAnchorManifest(c.db, anchorID)
	if manifest == nil {
		manifest = &rawdb.UBTRecoveryAnchorManifest{
			AnchorID:      anchorID,
			FormatVersion: recoveryAnchorFormatVersion,
		}
	}
	manifest.State = rawdb.UBTRecoveryAnchorBroken
	if reason != nil {
		manifest.FailureReason = truncateFailureReason(reason.Error())
	}
	rawdb.WriteUBTRecoveryAnchorManifest(c.db, anchorID, manifest)
}

func (c *Consumer) refreshLatestRecoveryAnchorMetadata() {
	c.hasRecoveryAnchor = false
	c.latestRecoveryAnchorSeq = 0
	c.latestRecoveryAnchorBlock = 0

	if idx, ok := rawdb.ReadUBTRecoveryAnchorLatestReady(c.db); ok {
		if manifest := rawdb.ReadUBTRecoveryAnchorManifest(c.db, idx); manifest != nil && manifest.State == rawdb.UBTRecoveryAnchorReady {
			c.hasRecoveryAnchor = true
			c.latestRecoveryAnchorSeq = manifest.Seq
			c.latestRecoveryAnchorBlock = manifest.BlockNumber
			recoveryAnchorLatestSeqGauge.Update(clampGauge(manifest.Seq))
			recoveryAnchorLatestBlockGauge.Update(clampGauge(manifest.BlockNumber))
			return
		}
	}
	count := rawdb.ReadUBTRecoveryAnchorCount(c.db)
	for i := int64(count) - 1; i >= 0; i-- {
		manifest := rawdb.ReadUBTRecoveryAnchorManifest(c.db, uint64(i))
		if manifest == nil || manifest.State != rawdb.UBTRecoveryAnchorReady {
			continue
		}
		rawdb.WriteUBTRecoveryAnchorLatestReady(c.db, uint64(i))
		c.hasRecoveryAnchor = true
		c.latestRecoveryAnchorSeq = manifest.Seq
		c.latestRecoveryAnchorBlock = manifest.BlockNumber
		recoveryAnchorLatestSeqGauge.Update(clampGauge(manifest.Seq))
		recoveryAnchorLatestBlockGauge.Update(clampGauge(manifest.BlockNumber))
		return
	}
	rawdb.DeleteUBTRecoveryAnchorLatestReady(c.db)
	recoveryAnchorLatestSeqGauge.Update(0)
	recoveryAnchorLatestBlockGauge.Update(0)
}

func (c *Consumer) createMaterializedRecoveryAnchor() {
	if c.cfg == nil || c.cfg.RecoveryAnchorInterval == 0 {
		return
	}
	// Avoid generating unusable anchors at genesis/empty state.
	if c.state.AppliedSeq == 0 && c.state.AppliedBlock == 0 && c.state.AppliedRoot == (common.Hash{}) {
		return
	}

	recoveryAnchorCreateAttempts.Inc(1)

	anchorID := rawdb.ReadUBTRecoveryAnchorCount(c.db)
	manifest := &rawdb.UBTRecoveryAnchorManifest{
		AnchorID:      anchorID,
		Seq:           c.state.AppliedSeq,
		BlockNumber:   c.state.AppliedBlock,
		BlockRoot:     c.state.AppliedRoot,
		CreatedAt:     uint64(time.Now().Unix()),
		FormatVersion: recoveryAnchorFormatVersion,
		State:         rawdb.UBTRecoveryAnchorCreating,
	}

	batch := c.db.NewBatch()
	rawdb.WriteUBTRecoveryAnchorManifest(batch, anchorID, manifest)
	rawdb.WriteUBTRecoveryAnchorCount(batch, anchorID+1)
	if err := batch.Write(); err != nil {
		recoveryAnchorCreateFailures.Inc(1)
		log.Warn("Recovery anchor creation failed: metadata write", "id", anchorID, "err", err)
		return
	}

	stagingDir := c.recoveryStagingDir(anchorID)
	stagingTrieDir := filepath.Join(stagingDir, "triedb")
	finalDir := c.recoveryAnchorDir(anchorID)
	liveTrieDir := filepath.Join(c.cfg.DataDir, "triedb")

	fail := func(err error) {
		recoveryAnchorCreateFailures.Inc(1)
		c.markRecoveryAnchorBroken(anchorID, err)
		_ = os.RemoveAll(stagingDir)
		log.Warn("Recovery anchor creation failed", "id", anchorID, "seq", manifest.Seq, "block", manifest.BlockNumber, "root", manifest.BlockRoot, "err", err)
	}

	if err := os.MkdirAll(filepath.Dir(stagingDir), 0o755); err != nil {
		fail(fmt.Errorf("create staging parent: %w", err))
		return
	}
	_ = os.RemoveAll(stagingDir)
	if err := copyDirRecursive(liveTrieDir, stagingTrieDir); err != nil {
		fail(fmt.Errorf("copy triedb to staging: %w", err))
		return
	}
	if err := c.verifyRecoveryAnchorData(stagingDir, manifest.BlockRoot); err != nil {
		fail(fmt.Errorf("verify staged recovery anchor: %w", err))
		return
	}

	manifest.State = rawdb.UBTRecoveryAnchorReady
	manifest.FailureReason = ""
	if err := writeRecoveryAnchorManifestFile(stagingDir, manifest); err != nil {
		fail(fmt.Errorf("write staging manifest file: %w", err))
		return
	}

	if err := os.MkdirAll(filepath.Dir(finalDir), 0o755); err != nil {
		fail(fmt.Errorf("create anchor parent: %w", err))
		return
	}
	if _, err := os.Stat(finalDir); err == nil {
		fail(fmt.Errorf("anchor directory already exists: %s", finalDir))
		return
	}
	if err := os.Rename(stagingDir, finalDir); err != nil {
		fail(fmt.Errorf("publish anchor: %w", err))
		return
	}

	batch = c.db.NewBatch()
	rawdb.WriteUBTRecoveryAnchorManifest(batch, anchorID, manifest)
	rawdb.WriteUBTRecoveryAnchorLatestReady(batch, anchorID)
	if err := batch.Write(); err != nil {
		recoveryAnchorCreateFailures.Inc(1)
		log.Warn("Recovery anchor creation failed: finalize metadata write", "id", anchorID, "err", err)
		return
	}

	recoveryAnchorCreateSuccesses.Inc(1)
	c.refreshLatestRecoveryAnchorMetadata()
	c.pruneOldRecoveryAnchors(anchorID)

	log.Info("UBT materialized recovery anchor created",
		"id", anchorID, "seq", manifest.Seq, "block", manifest.BlockNumber, "root", manifest.BlockRoot)
}

func (c *Consumer) pruneOldRecoveryAnchors(latestID uint64) {
	retention := c.cfg.RecoveryAnchorRetention
	if retention == 0 || latestID+1 <= retention {
		return
	}
	pruneBefore := latestID + 1 - retention
	for i := uint64(0); i < pruneBefore; i++ {
		rawdb.DeleteUBTRecoveryAnchorManifest(c.db, i)
		_ = os.RemoveAll(c.recoveryAnchorDir(i))
	}
}

func (c *Consumer) recoveryAnchorCoverageOK(anchorSeq uint64) (bool, string) {
	nextSeq := anchorSeq + 1
	if nextSeq == 0 {
		return false, "anchor sequence overflow"
	}
	lowest, lowErr := c.reader.LowestSeq()
	if lowErr == nil && lowest > 0 && nextSeq < lowest {
		return false, fmt.Sprintf("outbox floor is above replay start (next=%d lowest=%d)", nextSeq, lowest)
	}
	latest, latestErr := c.reader.LatestSeq()
	if latestErr == nil && nextSeq > latest+1 {
		return false, fmt.Sprintf("anchor replay start is ahead of latest outbox seq (next=%d latest=%d)", nextSeq, latest)
	}
	return true, ""
}

func (c *Consumer) replaceLiveTrieDBFrom(sourceTrieDir string) error {
	info, err := os.Stat(sourceTrieDir)
	if err != nil {
		return fmt.Errorf("stat source triedb: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source triedb is not a directory: %s", sourceTrieDir)
	}
	liveTrieDir := filepath.Join(c.cfg.DataDir, "triedb")
	if err := os.RemoveAll(liveTrieDir); err != nil {
		return fmt.Errorf("remove live triedb: %w", err)
	}
	if err := copyDirRecursive(sourceTrieDir, liveTrieDir); err != nil {
		return fmt.Errorf("copy recovery anchor triedb to live: %w", err)
	}
	return nil
}

func (c *Consumer) ensureFallbackApplier() error {
	if c.applier != nil {
		return nil
	}
	applier, err := newApplierWithRetry(c.cfg, common.Hash{}, 6, 250*time.Millisecond)
	if err != nil {
		return err
	}
	c.applier = applier
	return nil
}

func (c *Consumer) restoreFromMaterializedAnchor(targetBlock uint64) error {
	recoveryAnchorRestoreAttempts.Inc(1)

	readyIdx, ok := rawdb.ReadUBTRecoveryAnchorLatestReady(c.db)
	if !ok {
		recoveryAnchorRestoreFailures.Inc(1)
		return fmt.Errorf("no ready materialized recovery anchors")
	}
	count := rawdb.ReadUBTRecoveryAnchorCount(c.db)
	if count == 0 {
		recoveryAnchorRestoreFailures.Inc(1)
		return fmt.Errorf("no materialized recovery anchors available")
	}
	if readyIdx >= count {
		readyIdx = count - 1
	}

	for i := int64(readyIdx); i >= 0; i-- {
		anchorID := uint64(i)
		manifest := rawdb.ReadUBTRecoveryAnchorManifest(c.db, anchorID)
		if manifest == nil || manifest.State != rawdb.UBTRecoveryAnchorReady {
			continue
		}
		if manifest.BlockNumber > targetBlock {
			continue
		}
		if ok, reason := c.recoveryAnchorCoverageOK(manifest.Seq); !ok {
			log.Warn("Recovery anchor candidate rejected", "id", anchorID, "seq", manifest.Seq, "block", manifest.BlockNumber, "reason", reason)
			continue
		}

		anchorDir := c.recoveryAnchorDir(anchorID)
		if err := c.verifyRecoveryAnchorData(anchorDir, manifest.BlockRoot); err != nil {
			c.markRecoveryAnchorBroken(anchorID, fmt.Errorf("verification failed: %w", err))
			log.Warn("Recovery anchor candidate verification failed", "id", anchorID, "seq", manifest.Seq, "block", manifest.BlockNumber, "err", err)
			continue
		}

		if c.applier != nil {
			c.applier.Close()
			c.applier = nil
		}
		if err := c.replaceLiveTrieDBFrom(c.recoveryAnchorTrieDir(anchorID)); err != nil {
			c.markRecoveryAnchorBroken(anchorID, fmt.Errorf("copy to live failed: %w", err))
			log.Warn("Recovery anchor candidate rejected", "id", anchorID, "err", err)
			_ = c.ensureFallbackApplier()
			continue
		}

		applier, err := newApplierWithRetry(c.cfg, manifest.BlockRoot, 8, 300*time.Millisecond)
		if err != nil {
			c.markRecoveryAnchorBroken(anchorID, fmt.Errorf("open restored triedb failed: %w", err))
			log.Warn("Recovery anchor candidate rejected", "id", anchorID, "err", err)
			_ = c.ensureFallbackApplier()
			continue
		}
		c.applier = applier

		c.state.AppliedSeq = manifest.Seq
		c.state.AppliedRoot = manifest.BlockRoot
		c.state.AppliedBlock = manifest.BlockNumber
		c.processedSeq = manifest.Seq
		c.pendingRoot = manifest.BlockRoot
		c.pendingRootKnown = true
		c.pendingBlock = manifest.BlockNumber
		c.pendingBlockHash = rawdb.ReadUBTCanonicalBlockHash(c.db, manifest.BlockNumber)
		c.pendingParentHash = rawdb.ReadUBTCanonicalParentHash(c.db, manifest.BlockNumber)
		c.uncommittedBlocks = 0
		c.pendingBlockRoots = c.pendingBlockRoots[:0]
		if c.pendingStrictValidations != nil {
			for block := range c.pendingStrictValidations {
				delete(c.pendingStrictValidations, block)
			}
		}
		c.clearPendingMetadata()
		c.hasState = true
		c.recoveryMode = "anchor-restore"
		c.persistState()
		c.refreshLatestRecoveryAnchorMetadata()

		recoveryAnchorRestoreSuccesses.Inc(1)
		daemonSnapshotRestoreTotal.Inc(1)
		log.Info("Restored from materialized recovery anchor",
			"id", anchorID,
			"anchorSeq", manifest.Seq,
			"anchorBlock", manifest.BlockNumber,
			"anchorRoot", manifest.BlockRoot,
			"targetBlock", targetBlock)
		return nil
	}

	c.refreshLatestRecoveryAnchorMetadata()
	if err := c.ensureFallbackApplier(); err != nil {
		recoveryAnchorRestoreFailures.Inc(1)
		return fmt.Errorf("no usable materialized recovery anchor; fallback applier open failed: %w", err)
	}
	recoveryAnchorRestoreFailures.Inc(1)
	return fmt.Errorf("no usable materialized recovery anchor found at or below block %d", targetBlock)
}
