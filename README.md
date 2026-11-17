# discover

`discover` is a small, portable host and TCP port discovery tool for situations
where a larger scanner is not available. It intentionally does only two things:

- find live hosts in an IPv4 network;
- find open TCP ports on an IP, hostname, or IPv4 network.

It is written in Go and builds as a standalone executable. Python and Nmap are
not required on the target system.

## Download

Download the appropriate Linux or Windows executable from the
[latest GitHub Release](https://github.com/n3erajan/network-discovery/releases/latest).

## Usage

Use standard CIDR notation for a network:

```bash
discover 192.168.1.0/24
```

Scan one host using a comma-separated port list, ranges, or both:

```bash
discover 192.168.1.10 -p 22,80,443
discover 192.168.1.10 -p 1-1024
discover 192.168.1.10 -p-
discover server.internal --ports 22,80,443,8000-8100
```

Discover hosts and open ports across a network:

```bash
discover 10.10.10.0/24 -p 22,80,443,445,3389
```

The complete interface is:

```text
Usage:
  discover <target> [options]

Options:
  -p, --ports <ports>       TCP ports such as 22,80,443 or 1-1024
  -p-                       Scan all TCP ports (1-65535)
  -c, --concurrency <n>     Number of concurrent checks (default 100)
  -t, --timeout <duration>  Timeout per check (default 800ms)
  -h, --help                Show help
      --version             Show the version
```

Options may appear before or after the target.

## Build from source

Build a standalone executable for the current system:

```bash
make build
./bin/discover --help
```

Run the tests:

```bash
make test
```

Create standalone binaries for common Linux and Windows targets:

```bash
make release VERSION=1.0.0
ls dist/
```

Copy the binary matching the destination operating system and CPU architecture.
A single executable cannot run across different operating systems or CPU
architectures, but each generated executable has no Python or Go runtime
dependency.

You can also cross-compile one specific target directly:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o discover .
```

## Discovery behavior

Host discovery first uses the operating system's `ping` command and then falls
back to TCP probes on common ports. When `--ports` is supplied, every requested
port is scanned even if ping is blocked; an open port therefore also marks the
host as live.

Firewalls can drop both ICMP and TCP probes, so no network discovery method can
guarantee that a silent host is offline. IPv4 CIDR scans are limited to 65,536
addresses to prevent accidental extremely large scans. Single IPv6 addresses
and hostnames can be port-scanned, but IPv6 network ranges are intentionally not
expanded.

Only scan systems and networks you are authorized to test.
