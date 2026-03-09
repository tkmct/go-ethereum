package sidecar

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkBuildOfflineFromMPT(b *testing.B) {
	fixture := newConvertFromMPTBenchmarkFixture(b)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		sidecar, err := NewUBTSidecar(fixture.chainDB, fixture.chainDB, fixture.mptTrieDB)
		if err != nil {
			b.Fatal(err)
		}
		if !sidecar.BeginConversion() {
			b.Fatal("failed to begin conversion")
		}
		if err := sidecar.BuildOfflineFromMPT(context.Background(), fixture.chain, nil); err != nil {
			b.Fatal(err)
		}
		sidecar.Shutdown()
	}
}

func BenchmarkBuildOfflineFromMPTShardBits(b *testing.B) {
	for _, bits := range []uint8{1, 2, 3, 4} {
		b.Run(fmt.Sprintf("bits-%d", bits), func(b *testing.B) {
			fixture := newConvertFromMPTBenchmarkFixture(b)
			cfg := &UBTBuilderConfig{ShardBits: bits, QueueSize: 256}

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				sidecar, err := NewUBTSidecar(fixture.chainDB, fixture.chainDB, fixture.mptTrieDB)
				if err != nil {
					b.Fatal(err)
				}
				if !sidecar.BeginConversion() {
					b.Fatal("failed to begin conversion")
				}
				if err := sidecar.BuildOfflineFromMPT(context.Background(), fixture.chain, cfg); err != nil {
					b.Fatal(err)
				}
				sidecar.Shutdown()
			}
		})
	}
}

func BenchmarkBuildOfflineFromMPTWorkers(b *testing.B) {
	for _, workers := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("workers-%d", workers), func(b *testing.B) {
			fixture := newConvertFromMPTBenchmarkFixture(b)
			cfg := &UBTBuilderConfig{ShardBits: 4, QueueSize: 256, PrepareWorkers: workers}

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				sidecar, err := NewUBTSidecar(fixture.chainDB, fixture.chainDB, fixture.mptTrieDB)
				if err != nil {
					b.Fatal(err)
				}
				if !sidecar.BeginConversion() {
					b.Fatal("failed to begin conversion")
				}
				if err := sidecar.BuildOfflineFromMPT(context.Background(), fixture.chain, cfg); err != nil {
					b.Fatal(err)
				}
				sidecar.Shutdown()
			}
		})
	}
}

func BenchmarkBuildOfflineFromMPTScale(b *testing.B) {
	for _, totalAccounts := range []int{2368, 10000, 50000} {
		b.Run(fmt.Sprintf("size-%d", totalAccounts), func(b *testing.B) {
			fixture := newSizedConvertFromMPTBenchmarkFixture(b, totalAccounts)

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				sidecar, err := NewUBTSidecar(fixture.chainDB, fixture.chainDB, fixture.mptTrieDB)
				if err != nil {
					b.Fatal(err)
				}
				if !sidecar.BeginConversion() {
					b.Fatal("failed to begin conversion")
				}
				if err := sidecar.BuildOfflineFromMPT(context.Background(), fixture.chain, nil); err != nil {
					b.Fatal(err)
				}
				sidecar.Shutdown()
			}
		})
	}
}
