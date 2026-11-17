package main

import (
	"reflect"
	"testing"
	"time"
)

func TestParsePorts(t *testing.T) {
	ports, err := parsePorts("443, 80,100-102,80")
	if err != nil {
		t.Fatalf("parsePorts returned an error: %v", err)
	}
	want := []int{80, 100, 101, 102, 443}
	if !reflect.DeepEqual(ports, want) {
		t.Fatalf("parsePorts() = %v, want %v", ports, want)
	}
}

func TestParsePortsRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "0", "65536", "100-20", "1-2-3", "abc"} {
		if _, err := parsePorts(value); err == nil {
			t.Errorf("parsePorts(%q) unexpectedly succeeded", value)
		}
	}
}

func TestParseArgsAllowsOptionsAfterTarget(t *testing.T) {
	opts, err := parseArgs([]string{"192.168.1.0/24", "-p", "22,80-81", "--timeout", "2s", "-c", "50"})
	if err != nil {
		t.Fatalf("parseArgs returned an error: %v", err)
	}
	if opts.target != "192.168.1.0/24" {
		t.Errorf("target = %q", opts.target)
	}
	if !reflect.DeepEqual(opts.ports, []int{22, 80, 81}) {
		t.Errorf("ports = %v", opts.ports)
	}
	if opts.timeout != 2*time.Second {
		t.Errorf("timeout = %v", opts.timeout)
	}
	if opts.concurrency != 50 {
		t.Errorf("concurrency = %d", opts.concurrency)
	}
}

func TestParseArgsAllPortsShorthand(t *testing.T) {
	opts, err := parseArgs([]string{"192.168.1.10", "-p-"})
	if err != nil {
		t.Fatalf("parseArgs returned an error: %v", err)
	}
	if len(opts.ports) != 65535 || opts.ports[0] != 1 || opts.ports[65534] != 65535 {
		t.Fatalf("-p- produced unexpected ports: count=%d", len(opts.ports))
	}
}

func TestExpandTarget24(t *testing.T) {
	targets, err := expandTarget("192.168.1.0/24")
	if err != nil {
		t.Fatalf("expandTarget returned an error: %v", err)
	}
	if len(targets) != 254 {
		t.Fatalf("got %d targets, want 254", len(targets))
	}
	if targets[0] != "192.168.1.1" || targets[len(targets)-1] != "192.168.1.254" {
		t.Fatalf("unexpected target bounds: %s to %s", targets[0], targets[len(targets)-1])
	}
}

func TestExpandTarget32(t *testing.T) {
	targets, err := expandTarget("192.168.1.7/32")
	if err != nil {
		t.Fatalf("expandTarget returned an error: %v", err)
	}
	want := []string{"192.168.1.7"}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("expandTarget() = %v, want %v", targets, want)
	}
}

func TestExpandTargetRejectsLargeNetworks(t *testing.T) {
	if _, err := expandTarget("10.0.0.0/8"); err == nil {
		t.Fatal("expandTarget unexpectedly accepted an oversized network")
	}
}

func TestExpandTargetRejectsLegacyPrefix(t *testing.T) {
	if _, err := expandTarget("192.168.1."); err == nil {
		t.Fatal("expandTarget unexpectedly accepted an incomplete subnet prefix")
	}
}
