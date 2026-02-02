package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultGethRPC   = "http://localhost:8545"
	defaultContainer = "geth-ubt-test"
	defaultInterval  = 10 * time.Second
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "status":
		runStatus(os.Args[2:])
	case "monitor":
		runMonitor(os.Args[2:])
	case "logs":
		runLogs(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Println("ubtctl - UBT node monitoring tool")
	fmt.Println("")
	fmt.Println("Usage:")
	fmt.Println("  ubtctl status  [--geth-rpc URL] [--container NAME] [--log-lines N] [--log-tail N]")
	fmt.Println("  ubtctl monitor [--geth-rpc URL] [--interval 10s]")
	fmt.Println("  ubtctl logs    --mode tail|all|errors|progress [--container NAME]")
	fmt.Println("")
}

func runStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	gethRPC := fs.String("geth-rpc", defaultGethRPC, "Geth JSON-RPC endpoint")
	container := fs.String("container", defaultContainer, "Docker container name")
	logLines := fs.Int("log-lines", 10, "Number of recent UBT log lines to show")
	logTail := fs.Int("log-tail", 0, "Tail N lines when scanning logs (0 = full logs)")
	fs.Parse(args)

	ctx := context.Background()

	geth := fetchGethStatus(ctx, *gethRPC)

	fmt.Println("==========================================")
	fmt.Println("UBT Node Status")
	fmt.Println("==========================================")
	fmt.Println("")

	printGethStatus(geth)
	fmt.Println("")

	if *logLines > 0 {
		if err := requireContainer(*container); err != nil {
			fmt.Fprintf(os.Stderr, "Logs unavailable: %v\n", err)
			return
		}
		summary, err := summarizeLogs(*container, *logTail, *logLines)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Log summary error: %v\n", err)
			return
		}
		printLogSummary(summary)
	}
}

func runMonitor(args []string) {
	fs := flag.NewFlagSet("monitor", flag.ExitOnError)
	gethRPC := fs.String("geth-rpc", defaultGethRPC, "Geth JSON-RPC endpoint")
	interval := fs.Duration("interval", defaultInterval, "Polling interval")
	fs.Parse(args)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nExiting...")
			return
		default:
		}

		geth := fetchGethStatus(ctx, *gethRPC)

		now := time.Now()

		fmt.Println("==========================================")
		fmt.Printf("%s\n", now.Format("2006-01-02 15:04:05"))
		fmt.Println("==========================================")
		fmt.Println("")
		printGethStatus(geth)
		fmt.Println("")

		select {
		case <-ctx.Done():
			fmt.Println("\nExiting...")
			return
		case <-time.After(*interval):
		}
	}
}

func runLogs(args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	container := fs.String("container", defaultContainer, "Docker container name")
	mode := fs.String("mode", "tail", "Log mode: tail|all|errors|progress")
	fs.Parse(args)

	if err := requireContainer(*container); err != nil {
		fmt.Fprintf(os.Stderr, "Logs unavailable: %v\n", err)
		os.Exit(1)
	}

	filter, err := logFilter(*mode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	switch *mode {
	case "tail":
		if err := streamLogs(*container, filter); err != nil {
			fmt.Fprintf(os.Stderr, "Log streaming error: %v\n", err)
			os.Exit(1)
		}
	default:
		lines, err := fetchLogs(*container, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Log fetch error: %v\n", err)
			os.Exit(1)
		}
		for _, line := range lines {
			if filter.MatchString(line) {
				fmt.Println(line)
			}
		}
	}
}

// -------------------------
// Geth + UBT status helpers
// -------------------------

type gethStatus struct {
	RPCError    string
	Syncing     bool
	SyncKnown   bool
	Current     uint64
	Highest     uint64
	HeadBlock   uint64
	PeerCount   uint64
	SyncDetails bool
}

func fetchGethStatus(ctx context.Context, url string) gethStatus {
	status := gethStatus{}

	syncingRaw, err := rpcCall(ctx, url, "eth_syncing", []any{})
	if err != nil {
		status.RPCError = err.Error()
		return status
	}

	syncing, details, err := parseSyncing(syncingRaw)
	if err != nil {
		status.RPCError = err.Error()
		return status
	}
	status.Syncing = syncing
	status.SyncKnown = true
	if details != nil {
		status.Current = details.Current
		status.Highest = details.Highest
		status.SyncDetails = true
	}

	headRaw, err := rpcCall(ctx, url, "eth_blockNumber", []any{})
	if err == nil {
		if val, ok := parseHexUint(headRaw); ok {
			status.HeadBlock = val
		}
	}

	peerRaw, err := rpcCall(ctx, url, "net_peerCount", []any{})
	if err == nil {
		if val, ok := parseHexUint(peerRaw); ok {
			status.PeerCount = val
		}
	}

	return status
}

func printGethStatus(status gethStatus) {
	fmt.Println("Geth:")
	if status.RPCError != "" {
		fmt.Printf("  Status: RPC error (%s)\n", status.RPCError)
		return
	}

	if status.SyncKnown && !status.Syncing {
		fmt.Println("  Status: Synced")
	} else if status.SyncKnown && status.Syncing {
		if status.SyncDetails && status.Highest > 0 {
			gap := int64(status.Highest) - int64(status.Current)
			if gap < 0 {
				gap = 0
			}
			pct := float64(status.Current) * 100 / float64(status.Highest)
			fmt.Printf("  Status: Syncing (%.1f%%)\n", pct)
			fmt.Printf("  Blocks: current=%d highest=%d gap=%d\n", status.Current, status.Highest, gap)
		} else {
			fmt.Println("  Status: Syncing")
		}
	} else {
		fmt.Println("  Status: Unknown")
	}

	fmt.Printf("  Head Block: %d\n", status.HeadBlock)
	fmt.Printf("  Peers: %d\n", status.PeerCount)
}

// -------------------------
// Log summary + filtering
// -------------------------

type logSummary struct {
	StartedLine   string
	CompletedLine string
	FailedLine    string
	BatchCount    int
	LatestBatch   string
	RecentLines   []string
}

func summarizeLogs(container string, tail int, recent int) (logSummary, error) {
	lines, err := fetchLogs(container, tail)
	if err != nil {
		return logSummary{}, err
	}

	startedRe := regexp.MustCompile(`(?i)Starting UBT sidecar conversion`)
	completedRe := regexp.MustCompile(`(?i)UBT sidecar conversion complete`)
	failedRe := regexp.MustCompile(`(?i)UBT sidecar conversion failed`)
	batchRe := regexp.MustCompile(`(?i)UBT sidecar (conversion|replay|queue|update)`)
	ubtRe := regexp.MustCompile(`(?i)(ubt|binary.?trie|sidecar|conversion)`)

	summary := logSummary{}
	ring := make([]string, 0, recent)

	for _, line := range lines {
		if startedRe.MatchString(line) {
			summary.StartedLine = line
		}
		if completedRe.MatchString(line) {
			summary.CompletedLine = line
		}
		if failedRe.MatchString(line) {
			summary.FailedLine = line
		}
		if batchRe.MatchString(line) {
			summary.BatchCount++
			summary.LatestBatch = line
		}
		if ubtRe.MatchString(line) && recent > 0 {
			if len(ring) == cap(ring) {
				ring = ring[1:]
			}
			ring = append(ring, line)
		}
	}

	summary.RecentLines = ring
	return summary, nil
}

func printLogSummary(summary logSummary) {
	fmt.Println("Log Summary:")
	if summary.StartedLine != "" {
		fmt.Printf("  Started: %s\n", summary.StartedLine)
	} else {
		fmt.Println("  Started: not found")
	}

	if summary.CompletedLine != "" {
		fmt.Printf("  Completed: %s\n", summary.CompletedLine)
	} else {
		fmt.Println("  Completed: not found")
	}

	if summary.FailedLine != "" {
		fmt.Printf("  Failed: %s\n", summary.FailedLine)
	} else {
		fmt.Println("  Failed: not found")
	}

	if summary.BatchCount > 0 {
		fmt.Printf("  Progress lines: %d\n", summary.BatchCount)
		if summary.LatestBatch != "" {
			fmt.Printf("  Latest progress: %s\n", summary.LatestBatch)
		}
	} else {
		fmt.Println("  Progress lines: 0")
	}

	if len(summary.RecentLines) > 0 {
		fmt.Println("")
		fmt.Println("Recent UBT log entries:")
		for _, line := range summary.RecentLines {
			fmt.Printf("  %s\n", line)
		}
	}
}

func logFilter(mode string) (*regexp.Regexp, error) {
	switch mode {
	case "tail", "all":
		return regexp.MustCompile(`(?i)(ubt|binary.?trie|sidecar|conversion)`), nil
	case "errors":
		return regexp.MustCompile(`(?i)(ubt|binary.?trie|sidecar|conversion).*(error|fail|panic)`), nil
	case "progress":
		return regexp.MustCompile(`(?i)ubt.*(sidecar|conversion|queue|replay|progress|account|slot|commit)`), nil
	default:
		return nil, fmt.Errorf("Unknown mode: %s (use tail|all|errors|progress)", mode)
	}
}

// -------------------------
// Docker helpers
// -------------------------

func requireContainer(name string) error {
	running, err := containerRunning(name)
	if err != nil {
		return err
	}
	if !running {
		return fmt.Errorf("%s container is not running", name)
	}
	return nil
}

func containerRunning(name string) (bool, error) {
	cmd := exec.Command("docker", "ps", "--format", "{{.Names}}")
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("docker ps failed: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

func fetchLogs(container string, tail int) ([]string, error) {
	args := []string{"logs"}
	if tail > 0 {
		args = append(args, "--tail", strconv.Itoa(tail))
	}
	args = append(args, container)

	cmd := exec.Command("docker", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker logs failed: %w", err)
	}
	return splitLines(string(out)), nil
}

func streamLogs(container string, filter *regexp.Regexp) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "logs", "-f", container)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	go drain(stderr)

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if filter.MatchString(line) {
			fmt.Println(line)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return cmd.Wait()
}

func drain(r io.Reader) {
	io.Copy(io.Discard, r)
}

// -------------------------
// RPC helpers
// -------------------------

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
	ID      int    `json:"id"`
}

func rpcCall(ctx context.Context, url string, method string, params any) (json.RawMessage, error) {
	reqBody, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("RPC HTTP status %d", resp.StatusCode)
	}

	var out rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Error != nil {
		return nil, errors.New(out.Error.Message)
	}
	return out.Result, nil
}

// -------------------------
// Parsing + formatting
// -------------------------

type syncDetails struct {
	Current uint64
	Highest uint64
}

func parseSyncing(raw json.RawMessage) (bool, *syncDetails, error) {
	var flag bool
	if err := json.Unmarshal(raw, &flag); err == nil {
		return flag, nil, nil
	}

	var obj struct {
		CurrentBlock string `json:"currentBlock"`
		HighestBlock string `json:"highestBlock"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return false, nil, err
	}

	current, _ := parseHexString(obj.CurrentBlock)
	highest, _ := parseHexString(obj.HighestBlock)
	return true, &syncDetails{Current: current, Highest: highest}, nil
}

func parseHexUint(raw json.RawMessage) (uint64, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, false
	}
	return parseHexString(s)
}

func parseHexString(s string) (uint64, bool) {
	if strings.HasPrefix(s, "0x") {
		val, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64)
		if err != nil {
			return 0, false
		}
		return val, true
	}
	val, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return val, true
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
