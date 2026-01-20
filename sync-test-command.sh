./build/bin/geth --sepolia \
    --datadir ~/sepolia_node/geth_data \
    --state.ubt \
    --state.scheme=path \
    --state.skiproot \
    --syncmode "full" \
    --authrpc.jwtsecret ~/sepolia_node/jwt.hex


./lighthouse beacon \
    --network sepolia \
    --datadir ~/sepolia_node/lighthouse_data \
    --execution-endpoint http://localhost:8551 \
    --jwt-secrets ~/sepolia_node/jwt.hex

./lighthouse beacon \
    --network sepolia \
    --datadir ~/sepolia_node/lighthouse_data \
    --execution-endpoint http://localhost:8551 \
    --jwt-secrets ~/sepolia_node/jwt.hex --allow-insecure-genesis-sync \
    --boot-nodes="enr:-Iq4QMCTfIMXnow27baRUb35Q8iiFHSIDBJh6hQM5Axohhf4b6Kr_cOCu0htQ5WvVqKvFgY28893ZgTTsrcodB2Qa0hg2hVJGc0L9Cgo__4v2v63RTKORi4THT29OU2i12e93nn-SkL-bSt1I2eC3I4a821DS_pBCi2t24Qx1U-AU3x4A042-32bFdSeDAGvYdeAisw-shO25e1s2tGgNOII8Q25b7MokYMrT8V35v0z2401tI02Zqf62fRB8hM,enr:-Iq4QP2448p2gS3yBqeywe2f7gcI9sRkQreN515zZ7dGg9dp3K3L1f2nS6s2MR3Yd5P1f24fGhgL5WsDso3GGAv65RBg2hVJGc0L9Cgo__4v2v63RTKORi4THT29OU2i1 93nn-SkL-bSt1I2eC3I4a821DS_pBCi2t24Qx1U-AU3x4A042-32bFdSeDAGv5vYdeAisw-shO25e1s2tGgNOII8Q25b7MokYMrT8V35v0z2401tI02Zqf62fRB8hY"


./build/bin/geth --sepolia \
   --datadir ~/sepolia_node/geth_data \
    --state.ubt \
    --state.scheme=path \
    --state.skiproot \
    --syncmode "full" \
    --authrpc.jwtsecret ~/sepolia_node/jwt.hex



    # 1. 古いプロセスを終了
kill 11771 11772

# 2. Gethを新しいディスクで起動
nohup ./build/bin/geth \
    --sepolia \
    --datadir /data2/sepolia_node/geth \
    --syncmode snap \
    --http \
    --http.port 8545 \
    --http.api eth,net,web3,admin,debug \
    --authrpc.addr 0.0.0.0 \
    --authrpc.port 8551 \
    --authrpc.jwtsecret /data2/sepolia_node/jwt.hex \
    --state.scheme path \
    --state.ubt \
    --cache.preimages \
    --verbosity 4 \
    > /data2/sepolia_node/geth.log 2>&1 &

# 3. Lighthouseを起動
nohup lighthouse bn \
    --network sepolia \
    --datadir /data2/sepolia_node/lighthouse \
    --execution-endpoint http://localhost:8551 \
    --execution-jwt /data2/sepolia_node/jwt.hex \
    --checkpoint-sync-url https://sepolia.beaconstate.info \
    --http \
    > /data2/sepolia_node/lighthouse.log 2>&1 &

# 4. 起動確認
sleep 3
pgrep -a geth
pgrep -a lighthouse
