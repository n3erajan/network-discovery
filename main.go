package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultConcurrency = 100
	defaultTimeout     = 800 * time.Millisecond
	maxTargets         = 65536
)

var version = "dev"

type options struct {
	target      string
	ports       []int
	concurrency int
	timeout     time.Duration
	showHelp    bool
	showVersion bool
}

type hostResult struct {
	target string
	alive  bool
	ports  []int
}

type portJob struct {
	hostIndex int
	target    string
	port      int
}

type portResult struct {
	hostIndex int
	port      int
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n\n", err)
		printUsage(stderr)
		return 2
	}
	if opts.showHelp {
		printUsage(stdout)
		return 0
	}
	if opts.showVersion {
		fmt.Fprintf(stdout, "discover %s\n", version)
		return 0
	}

	targets, err := expandTarget(opts.target)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	results := make([]hostResult, len(targets))
	for i, target := range targets {
		results[i].target = target
	}

	discoverHosts(ctx, results, opts)
	if len(opts.ports) > 0 && ctx.Err() == nil {
		scanPorts(ctx, results, opts)
	}

	if ctx.Err() != nil {
		fmt.Fprintln(stderr, "Scan interrupted.")
		return 130
	}

	printResults(stdout, results, len(opts.ports) > 0)
	return 0
}

func parseArgs(args []string) (options, error) {
	opts := options{concurrency: defaultConcurrency, timeout: defaultTimeout}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			opts.showHelp = true
		case arg == "--version":
			opts.showVersion = true
		case arg == "-p-":
			opts.ports, _ = parsePorts("1-65535")
		case arg == "-p" || arg == "--ports":
			value, next, err := optionValue(args, i, arg)
			if err != nil {
				return opts, err
			}
			i = next
			opts.ports, err = parsePorts(value)
			if err != nil {
				return opts, err
			}
		case strings.HasPrefix(arg, "--ports=") || strings.HasPrefix(arg, "-p="):
			value := strings.SplitN(arg, "=", 2)[1]
			var err error
			opts.ports, err = parsePorts(value)
			if err != nil {
				return opts, err
			}
		case arg == "-c" || arg == "--concurrency":
			value, next, err := optionValue(args, i, arg)
			if err != nil {
				return opts, err
			}
			i = next
			opts.concurrency, err = strconv.Atoi(value)
			if err != nil || opts.concurrency < 1 || opts.concurrency > 10000 {
				return opts, fmt.Errorf("concurrency must be between 1 and 10000")
			}
		case strings.HasPrefix(arg, "--concurrency=") || strings.HasPrefix(arg, "-c="):
			value := strings.SplitN(arg, "=", 2)[1]
			var err error
			opts.concurrency, err = strconv.Atoi(value)
			if err != nil || opts.concurrency < 1 || opts.concurrency > 10000 {
				return opts, fmt.Errorf("concurrency must be between 1 and 10000")
			}
		case arg == "-t" || arg == "--timeout":
			value, next, err := optionValue(args, i, arg)
			if err != nil {
				return opts, err
			}
			i = next
			opts.timeout, err = time.ParseDuration(value)
			if err != nil || opts.timeout <= 0 {
				return opts, fmt.Errorf("timeout must be a positive duration such as 500ms or 2s")
			}
		case strings.HasPrefix(arg, "--timeout=") || strings.HasPrefix(arg, "-t="):
			value := strings.SplitN(arg, "=", 2)[1]
			var err error
			opts.timeout, err = time.ParseDuration(value)
			if err != nil || opts.timeout <= 0 {
				return opts, fmt.Errorf("timeout must be a positive duration such as 500ms or 2s")
			}
		case strings.HasPrefix(arg, "-"):
			return opts, fmt.Errorf("unknown option %q", arg)
		default:
			if opts.target != "" {
				return opts, fmt.Errorf("only one target may be specified")
			}
			opts.target = arg
		}
	}

	if opts.showHelp || opts.showVersion {
		return opts, nil
	}
	if opts.target == "" {
		return opts, fmt.Errorf("a target IP, hostname, or CIDR network is required")
	}
	return opts, nil
}

func optionValue(args []string, index int, option string) (string, int, error) {
	if index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
		return "", index, fmt.Errorf("%s requires a value", option)
	}
	return args[index+1], index + 1, nil
}

func parsePorts(value string) ([]int, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("port list cannot be empty")
	}

	seen := make(map[int]struct{})
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, fmt.Errorf("invalid empty port in %q", value)
		}

		parts := strings.Split(item, "-")
		switch len(parts) {
		case 1:
			port, err := validPort(parts[0])
			if err != nil {
				return nil, err
			}
			seen[port] = struct{}{}
		case 2:
			start, err := validPort(parts[0])
			if err != nil {
				return nil, err
			}
			end, err := validPort(parts[1])
			if err != nil {
				return nil, err
			}
			if start > end {
				return nil, fmt.Errorf("invalid port range %q: start is greater than end", item)
			}
			for port := start; port <= end; port++ {
				seen[port] = struct{}{}
			}
		default:
			return nil, fmt.Errorf("invalid port or range %q", item)
		}
	}

	ports := make([]int, 0, len(seen))
	for port := range seen {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports, nil
}

func validPort(value string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q: ports must be between 1 and 65535", value)
	}
	return port, nil
}

func expandTarget(target string) ([]string, error) {
	if !strings.Contains(target, "/") {
		if strings.TrimSpace(target) == "" {
			return nil, fmt.Errorf("target cannot be empty")
		}
		if isLegacySubnetPrefix(target) {
			return nil, fmt.Errorf("incomplete subnet %q: use CIDR notation such as %s0/24", target, target)
		}
		return []string{target}, nil
	}

	ip, network, err := net.ParseCIDR(target)
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR target %q (example: 192.168.1.0/24)", target)
	}
	if ip.To4() == nil {
		return nil, fmt.Errorf("IPv6 CIDR ranges are not supported; use a single IPv6 address")
	}

	ones, bits := network.Mask.Size()
	hostCount := uint64(1) << uint(bits-ones)
	if hostCount > maxTargets {
		return nil, fmt.Errorf("CIDR contains %d addresses; the maximum is %d", hostCount, maxTargets)
	}

	first := network.IP.To4()
	start := ipv4ToUint32(first)
	end := start + uint32(hostCount) - 1
	if ones <= 30 {
		start++ // Skip the network address.
		end--   // Skip the broadcast address.
	}

	targets := make([]string, 0, hostCount)
	for current := start; current <= end; current++ {
		targets = append(targets, uint32ToIPv4(current).String())
		if current == ^uint32(0) {
			break
		}
	}
	return targets, nil
}

func isLegacySubnetPrefix(target string) bool {
	if !strings.HasSuffix(target, ".") {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(target, "."), ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 || value > 255 {
			return false
		}
	}
	return true
}

func ipv4ToUint32(ip net.IP) uint32 {
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

func uint32ToIPv4(value uint32) net.IP {
	return net.IPv4(byte(value>>24), byte(value>>16), byte(value>>8), byte(value))
}

func discoverHosts(ctx context.Context, results []hostResult, opts options) {
	jobs := make(chan int)
	var workers sync.WaitGroup

	for i := 0; i < min(opts.concurrency, len(results)); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				results[index].alive = hostIsAlive(ctx, results[index].target, opts.timeout, opts.ports)
			}
		}()
	}

sendLoop:
	for index := range results {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break sendLoop
		}
	}
	close(jobs)
	workers.Wait()
}

func hostIsAlive(ctx context.Context, target string, timeout time.Duration, requestedPorts []int) bool {
	if systemPing(ctx, target, timeout) {
		return true
	}

	// TCP is a fallback for minimal systems without ping and hosts that block ICMP.
	seen := make(map[int]struct{})
	probe := func(port int) bool {
		if _, exists := seen[port]; exists {
			return false
		}
		seen[port] = struct{}{}
		return tcpHostProbe(ctx, target, port, timeout)
	}

	for _, port := range requestedPorts {
		if probe(port) {
			return true
		}
		// Keep discovery bounded when a very large port range was requested.
		if len(seen) >= 3 {
			break
		}
	}
	for _, port := range []int{80, 443, 22, 445, 3389} {
		if probe(port) {
			return true
		}
	}
	return false
}

func systemPing(parent context.Context, target string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	milliseconds := max(1, int(timeout.Milliseconds()))
	seconds := max(1, int((timeout+time.Second-1)/time.Second))
	var args []string
	switch runtime.GOOS {
	case "windows":
		args = []string{"-n", "1", "-w", strconv.Itoa(milliseconds), target}
	default:
		args = []string{"-c", "1", "-W", strconv.Itoa(seconds), target}
	}

	command := exec.CommandContext(ctx, "ping", args...)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Run() == nil
}

func tcpHostProbe(ctx context.Context, target string, port int, timeout time.Duration) bool {
	dialer := net.Dialer{Timeout: timeout}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(target, strconv.Itoa(port)))
	if err == nil {
		connection.Close()
		return true
	}
	// A refusal is a response from a live host with a closed port.
	return errors.Is(err, syscall.ECONNREFUSED)
}

func scanPorts(ctx context.Context, results []hostResult, opts options) {
	jobs := make(chan portJob)
	open := make(chan portResult)
	var workers sync.WaitGroup

	for i := 0; i < opts.concurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				if portIsOpen(ctx, job.target, job.port, opts.timeout) {
					select {
					case open <- portResult{hostIndex: job.hostIndex, port: job.port}:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for hostIndex, result := range results {
			for _, port := range opts.ports {
				select {
				case jobs <- portJob{hostIndex: hostIndex, target: result.target, port: port}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	go func() {
		workers.Wait()
		close(open)
	}()

	for result := range open {
		results[result.hostIndex].alive = true
		results[result.hostIndex].ports = append(results[result.hostIndex].ports, result.port)
	}
	for i := range results {
		sort.Ints(results[i].ports)
	}
}

func portIsOpen(ctx context.Context, target string, port int, timeout time.Duration) bool {
	dialer := net.Dialer{Timeout: timeout}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(target, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	connection.Close()
	return true
}

func printResults(output io.Writer, results []hostResult, portScan bool) {
	liveHosts := 0
	openPorts := 0
	for _, result := range results {
		if !result.alive {
			continue
		}
		liveHosts++
		fmt.Fprintf(output, "Host %s is up\n", result.target)
		for _, port := range result.ports {
			openPorts++
			fmt.Fprintf(output, "  %d/tcp open\n", port)
		}
	}

	if liveHosts == 0 {
		fmt.Fprintln(output, "No live hosts found.")
	}
	if portScan {
		fmt.Fprintf(output, "\nScanned %d host(s): %d up, %d open port(s)\n", len(results), liveHosts, openPorts)
	} else {
		fmt.Fprintf(output, "\nScanned %d host(s): %d up\n", len(results), liveHosts)
	}
}

func printUsage(output io.Writer) {
	fmt.Fprint(output, `discover - simple host and TCP port discovery

Usage:
  discover <target> [options]

Target:
  A single IP, hostname, or IPv4 CIDR network (for example 192.168.1.0/24)

Options:
  -p, --ports <ports>       TCP ports such as 22,80,443 or 1-1024
  -p-                       Scan all TCP ports (1-65535)
  -c, --concurrency <n>     Number of concurrent checks (default 100)
  -t, --timeout <duration>  Timeout per check (default 800ms)
  -h, --help                Show this help
      --version             Show the version

Examples:
  discover 192.168.1.0/24
  discover 192.168.1.10 -p 22,80,443
  discover 10.10.0.0/24 --ports 1-1024 --timeout 1s
`)
}
