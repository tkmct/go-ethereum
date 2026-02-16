package main

import (
	"errors"
	"testing"
)

func TestIsHistoricalStateUnavailableRPC(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "historical state unavailable message",
			err:  errors.New("eth_getBalance: historical state 0xabc is not available"),
			want: true,
		},
		{
			name: "unrelated RPC error",
			err:  errors.New("eth_getBalance: context deadline exceeded"),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := isHistoricalStateUnavailableRPC(tt.err)
			if got != tt.want {
				t.Fatalf("isHistoricalStateUnavailableRPC()=%v, want %v", got, tt.want)
			}
		})
	}
}
