package sysfeatures

import "testing"

func TestClassifyOS(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Windows Server 2022 Datacenter", "windows-server"},
		{"Windows 11 Pro", "windows-workstation"},
		{"Windows 10 Enterprise", "windows-workstation"},
		{"Windows", "windows-other"},
		{"macOS 14.5", "mac"},
		{"Mac OS X 10.15", "mac"},
		{"Darwin 23.4", "mac"},
		{"Ubuntu Server 22.04", "linux-server"},
		{"RHEL 9", "linux-server"},
		{"CentOS 7", "linux-server"},
		{"Ubuntu 22.04 LTS", "linux-workstation"},
		{"Arch Linux", "linux-workstation"},
		{"Fedora Workstation 40", "linux-workstation"},
		{"FreeBSD 14", "bsd"},
		{"AIX 7.3", "unknown"},
		{"", "unknown"},
	}
	for _, c := range cases {
		got := ClassifyOS(c.in)
		if got != c.want {
			t.Errorf("ClassifyOS(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLooksLikeDomainController(t *testing.T) {
	yes := []string{"dc01", "DC02.corp.local", "ad-dc01", "pdc-east", "bdc-west", "DomainController", "dc-1"}
	no := []string{"workstation01", "web-server-1", "db1", "fileserver", "advert-host", "discord-bot"}
	for _, h := range yes {
		if !LooksLikeDomainController(h) {
			t.Errorf("expected %q to look like DC", h)
		}
	}
	for _, h := range no {
		if LooksLikeDomainController(h) {
			t.Errorf("expected %q NOT to look like DC", h)
		}
	}
}
