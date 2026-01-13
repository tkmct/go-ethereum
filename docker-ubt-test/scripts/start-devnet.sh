#!/bin/bash
# Start a local devnet for lightweight UBT testing
# Creates a private PoA network with controlled state

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOCKER_DIR="$(dirname "$SCRIPT_DIR")"

cd "$DOCKER_DIR"

echo "=========================================="
echo "UBT Local Devnet - Starting"
echo "=========================================="

# Configuration
CHAIN_ID=1337
NUM_ACCOUNTS=${1:-1000}  # Default 1000 accounts
BALANCE="1000000000000000000000"  # 1000 ETH each

echo "Chain ID: $CHAIN_ID"
echo "Pre-funded accounts: $NUM_ACCOUNTS"

# Create devnet data directory
mkdir -p data/devnet

# Generate genesis with pre-funded accounts
echo "Generating genesis with $NUM_ACCOUNTS accounts..."

cat > data/devnet/genesis.json << EOF
{
  "config": {
    "chainId": $CHAIN_ID,
    "homesteadBlock": 0,
    "eip150Block": 0,
    "eip155Block": 0,
    "eip158Block": 0,
    "byzantiumBlock": 0,
    "constantinopleBlock": 0,
    "petersburgBlock": 0,
    "istanbulBlock": 0,
    "berlinBlock": 0,
    "londonBlock": 0,
    "arrowGlacierBlock": 0,
    "grayGlacierBlock": 0,
    "mergeNetsplitBlock": 0,
    "shanghaiTime": 0,
    "cancunTime": 0,
    "terminalTotalDifficulty": 0,
    "terminalTotalDifficultyPassed": true,
    "clique": {
      "period": 1,
      "epoch": 30000
    }
  },
  "difficulty": "1",
  "gasLimit": "30000000",
  "extradata": "0x0000000000000000000000000000000000000000000000000000000000000000f39Fd6e51aad88F6F4ce6aB8827279cffFb922660000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
  "alloc": {
EOF

# Add signer account (Hardhat default #0)
echo '    "f39Fd6e51aad88F6F4ce6aB8827279cffFb92266": {"balance": "10000000000000000000000000"},' >> data/devnet/genesis.json

# Generate deterministic accounts
for i in $(seq 1 $NUM_ACCOUNTS); do
    # Generate address from index (deterministic)
    ADDR=$(printf "0x%040x" $i)
    if [ $i -lt $NUM_ACCOUNTS ]; then
        echo "    \"$ADDR\": {\"balance\": \"$BALANCE\"}," >> data/devnet/genesis.json
    else
        echo "    \"$ADDR\": {\"balance\": \"$BALANCE\"}" >> data/devnet/genesis.json
    fi
done

cat >> data/devnet/genesis.json << EOF
  }
}
EOF

echo "Genesis created with $NUM_ACCOUNTS accounts"

# Create keystore for signer (Hardhat default account #0)
mkdir -p data/devnet/keystore
cat > data/devnet/keystore/signer.json << 'EOF'
{"address":"f39fd6e51aad88f6f4ce6ab8827279cfffb92266","crypto":{"cipher":"aes-128-ctr","ciphertext":"8c9c1f0537b6cf2bf2e7b1d7b1c0d6b7c5d4e3f2a1b0c9d8e7f6a5b4c3d2e1f0","cipherparams":{"iv":"a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6"},"kdf":"scrypt","kdfparams":{"dklen":32,"n":262144,"p":1,"r":8,"salt":"d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5"},"mac":"e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2"},"id":"12345678-1234-1234-1234-123456789012","version":3}
EOF

# Create password file
echo "password" > data/devnet/password.txt

echo ""
echo "To start the devnet:"
echo ""
echo "  # Initialize genesis"
echo "  docker run --rm -v \$(pwd)/data/devnet:/data docker-ubt-test-geth \\"
echo "    --datadir /data init /data/genesis.json"
echo ""
echo "  # Run geth with UBT"
echo "  docker run --rm -it -p 8545:8545 -v \$(pwd)/data/devnet:/data docker-ubt-test-geth \\"
echo "    --datadir /data \\"
echo "    --networkid $CHAIN_ID \\"
echo "    --state.scheme path \\"
echo "    --state.ubt \\"
echo "    --http --http.addr 0.0.0.0 \\"
echo "    --nodiscover"
echo ""
echo "This creates $NUM_ACCOUNTS accounts without needing snap sync!"
