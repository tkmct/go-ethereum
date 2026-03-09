package sidecar

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkBuildOfflineBucketedFromMPT(b *testing.B) {
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
		if err := sidecar.BuildOfflineBucketedFromMPT(context.Background(), fixture.chain, nil); err != nil {
			b.Fatal(err)
		}
		sidecar.Shutdown()
	}
}

func BenchmarkBuildOfflineBucketedFromMPTScale(b *testing.B) {
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
				if err := sidecar.BuildOfflineBucketedFromMPT(context.Background(), fixture.chain, nil); err != nil {
					b.Fatal(err)
				}
				sidecar.Shutdown()
			}
		})
	}
}
