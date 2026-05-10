//go:build linux

package main

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"time"
)

// collectNeighbors prefers `ip neigh` (modern Linux) and falls back to
// `arp -an` for systems without iproute2 utilities.
func collectNeighbors() []AgentNeighborEntry {
	if hasCommand("ip") {
		if out := runNeighbors("ip", "neigh"); out != nil {
			return parseIPNeigh(out)
		}
	}
	if hasCommand("arp") {
		if out := runNeighbors("arp", "-an"); out != nil {
			return parseArpAn(out)
		}
	}
	return nil
}

func runNeighbors(name string, args ...string) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return nil
	}
	return out
}

// parseIPNeigh: `192.168.1.1 dev wlan0 lladdr aa:bb:cc:dd:ee:ff REACHABLE`
func parseIPNeigh(out []byte) []AgentNeighborEntry {
	res := []AgentNeighborEntry{}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		entry := AgentNeighborEntry{IP: fields[0]}
		for i := 1; i < len(fields); i++ {
			switch fields[i] {
			case "dev":
				if i+1 < len(fields) {
					entry.Iface = fields[i+1]
				}
			case "lladdr":
				if i+1 < len(fields) {
					entry.MAC = fields[i+1]
				}
			}
		}
		// Skip entries with no MAC (FAILED / INCOMPLETE state).
		if entry.MAC == "" {
			continue
		}
		res = append(res, entry)
	}
	return res
}

// parseArpAn: `? (192.168.1.1) at aa:bb:cc:dd:ee:ff [ether] on wlan0`
func parseArpAn(out []byte) []AgentNeighborEntry {
	res := []AgentNeighborEntry{}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		ipStart := strings.Index(line, "(")
		ipEnd := strings.Index(line, ")")
		if ipStart < 0 || ipEnd <= ipStart {
			continue
		}
		ip := line[ipStart+1 : ipEnd]
		entry := AgentNeighborEntry{IP: ip}
		if at := strings.Index(line, "at "); at >= 0 {
			rest := line[at+3:]
			fields := strings.Fields(rest)
			if len(fields) >= 1 && fields[0] != "<incomplete>" {
				entry.MAC = fields[0]
			}
			for i, f := range fields {
				if f == "on" && i+1 < len(fields) {
					entry.Iface = fields[i+1]
				}
			}
		}
		if entry.MAC == "" {
			continue
		}
		res = append(res, entry)
	}
	return res
}
