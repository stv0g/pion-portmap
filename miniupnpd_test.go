// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package portmap_test

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type Permission struct {
	Allow                              bool
	Mask                               net.IPNet
	ExternalPortStart, ExternalPortEnd int
	InternalPortStart, InternalPortEnd int
}

func (p Permission) String() string {
	var action string
	if p.Allow {
		action = "allow"
	} else {
		action = "deny"
	}

	return fmt.Sprintf("%s %d-%d %s %d-%d",
		action,
		p.ExternalPortStart, p.ExternalPortEnd,
		p.Mask.String(),
		p.InternalPortStart, p.InternalPortEnd,
	)
}

type STUNOptions struct {
	Enable bool

	// Specify STUN server, either hostname or IP address
	Host string

	// Specify STUN UDP port, by default it is standard port 3478.
	Port int
}

type SSDPOptions struct {
	// Name or IP address of the interface used to listen to SSDP packets coming on port 1900, multicast address 239.255.255.250.
	Interfaces []string

	// TTL of the package.
	TTL int

	// Path to the UNIX socket used to communicate with MiniSSDPd
	// If running, MiniSSDPd will manage M-SEARCH answering.
	// Default is /var/run/minissdpd.sock
	Socket string
}

type PCPOptions struct {
	// Configure the minimum and maximum lifetime of a port mapping in seconds
	// 120s and 86400s (24h) are suggested values from PCP-base
	LifetimeMin, LifetimeMax time.Duration
}

type MiniUPNPdServerOptions struct {
	UUID uuid.UUID

	// WAN network interface
	ExternalInterface string

	// If the WAN interface has several IP addresses, you
	// can specify the one to use below.
	// Setting ext_ip is also useful in double NAT setup, you can declare here
	// the public IP address.
	ExternalIP net.IP

	// LAN network interfaces IPs / networks
	// There can be multiple listening IPs for SSDP traffic.
	// It can be IP address or network interface name (ie. "eth0")
	// It is mandatory to use the network interface name in order to enable IPv6
	// HTTP is available on all interfaces.
	ListeningIP []string

	// WAN interface must have public IP address. Otherwise it is behind NAT
	// and port forwarding is impossible. In some cases WAN interface can be
	// behind unrestricted full-cone NAT 1:1 when all incoming traffic is NAT-ed and
	// routed to WAN interfaces without any filtering. In this cases miniupnpd
	// needs to know public IP address and it can be learnt by asking external
	// server via STUN protocol. Following option enable retrieving external
	// public IP address from STUN server and detection of NAT type. You need
	// to specify also external STUN server in stun_host option below.
	// This option is disabled by default.
	STUN STUNOptions
	SSDP SSDPOptions
	PCP  PCPOptions
	// Lease file location
	LeaseFile string

	// Enable NAT-PMP support
	EnableNATPMP bool
	// Enable UPNP support
	EnableUPNP  bool
	DisableIPv6 bool

	// Notify interval in seconds.
	// Default is 30 seconds
	IntervalNotify time.Duration

	// Clean process work interval in seconds.
	// Default is 0 (disabled).
	// A 600 seconds (10 minutes) interval makes sense
	IntervalCleanRuleset time.Duration

	// Unused rules cleaning.
	// Never remove any rule before this threshold for the number
	// of redirections is exceeded.
	// Default is 20 seconds
	ThresholdCleanRuleset time.Duration

	// Port for HTTP (descriptions and SOAP) traffic. Set to 0 for autoselect.
	PortHTTP int

	// Port for HTTPS. Set to 0 for autoselect.
	PortHTTPS int

	// Secure Mode, UPnP clients can only add mappings to their own IP
	Secure bool

	// Report system uptime instead of daemon uptime
	SystemUptime bool

	// UPnP permission rules
	// It is advised to only allow redirection of port >= 1024
	// and end the rule set with a deny all rule.
	Permissions []Permission
}

type MiniUPNPdServer struct {
	cmdUPNP *exec.Cmd
	cmdSSDP *exec.Cmd

	opts MiniUPNPdServerOptions
}

func NewMiniUPNPdServer(opts MiniUPNPdServerOptions) *MiniUPNPdServer {
	s := &MiniUPNPdServer{
		opts: opts,
	}

	emptyUUID := uuid.UUID{}
	if opts.UUID == emptyUUID {
		opts.UUID = uuid.New()
	}

	if opts.SSDP.Socket == "" {
		opts.SSDP.Socket = "/var/run/minissdpd.sock"
	}

	if opts.LeaseFile == "" {
		opts.LeaseFile = "/var/log/upnp.leases"
	}

	if opts.SSDP.TTL == 0 {
		opts.SSDP.TTL = 2
	}

	return s
}

func (s *MiniUPNPdServer) Start() error {
	if err := s.startMiniSSDPd(); err != nil {
		return err
	}

	return s.startMiniUPNPd()
}

func (s *MiniUPNPdServer) Close() error {
	if c := s.cmdSSDP; c != nil {
		if err := c.Process.Signal(os.Interrupt); err != nil {
			return err
		}

		if err := c.Wait(); err != nil {
			return err
		}
	}

	if c := s.cmdUPNP; c != nil {
		if err := c.Process.Signal(os.Interrupt); err != nil {
			return err
		}

		if err := c.Wait(); err != nil {
			return err
		}
	}

	return nil
}

func (s *MiniUPNPdServer) createMiniUPNPdConfig() (*os.File, error) {
	f, err := os.CreateTemp("", "miniupnp-*.conf")
	if err != nil {
		return nil, err
	}

	opts := map[string]any{
		"enable_natpmp":   s.opts.EnableNATPMP,
		"enable_upnp":     s.opts.EnableUPNP,
		"ext_ifname":      s.opts.ExternalInterface,
		"secure_mode":     s.opts.Secure,
		"system_uptime":   s.opts.SystemUptime,
		"ipv6_disable":    s.opts.DisableIPv6,
		"minissdpdsocket": s.opts.SSDP.Socket,
		"lease_file":      s.opts.LeaseFile,
	}

	if s.opts.PortHTTP != 0 {
		opts["http_port"] = s.opts.PortHTTP
	}
	if s.opts.PortHTTPS != 0 {
		opts["http_ports"] = s.opts.PortHTTPS
	}

	if s.opts.EnableNATPMP {
		if s.opts.PCP.LifetimeMax != 0 {
			opts["max_lifetime"] = s.opts.PCP.LifetimeMax
		}
		if s.opts.PCP.LifetimeMin != 0 {
			opts["min_lifetime"] = s.opts.PCP.LifetimeMin
		}
	}

	if s.opts.IntervalNotify != 0 {
		opts["notify_interval"] = s.opts.IntervalNotify
	}
	if s.opts.IntervalCleanRuleset != 0 {
		opts["clean_ruleset_interval"] = s.opts.IntervalCleanRuleset
	}
	if s.opts.ThresholdCleanRuleset != 0 {
		opts["clean_ruleset_threshold"] = s.opts.ThresholdCleanRuleset
	}

	if s.opts.STUN.Enable || s.opts.ExternalIP.IsUnspecified() {
		opts["ext_perform_stun"] = true

		if s.opts.STUN.Host != "" {
			opts["ext_stun_host"] = s.opts.STUN.Host
		}
		if s.opts.STUN.Port != 0 {
			opts["ext_stun_port"] = s.opts.STUN.Port
		}
	} else {
		opts["ext_ip"] = s.opts.ExternalIP
	}

	for key, val := range opts {
		switch valt := val.(type) {
		case time.Duration:
			val = valt.Seconds()
		case bool:
			if valt {
				val = "yes"
			} else {
				val = "no"
			}
		}

		fmt.Fprintf(f, "%s=%v\n", key, val)
	}

	for _, lip := range s.opts.ListeningIP {
		fmt.Fprintf(f, "listening_ip=%s\n", lip)
	}

	for _, perm := range s.opts.Permissions {
		fmt.Fprintln(f, perm)
	}

	if err := f.Close(); err != nil {
		return nil, err
	}

	return f, nil
}

func (s *MiniUPNPdServer) startMiniSSDPd() error {
	args := []string{
		"-d",                     // Debug mode: do not go to background, output messages to console and do not filter out low priority messages.
		"-6",                     // IPv6: Enable IPv6 in addition to IPv4.
		"-s", s.opts.SSDP.Socket, // Path of the UNIX socket open for communicating with other processes.
		"-t", fmt.Sprintf("%d", s.opts.SSDP.TTL), // TTL of the package
		// "-f", "", // Search/filter a specific device type.
	}
	for _, intf := range s.opts.SSDP.Interfaces {
		args = append(args, "-i", intf)
	}

	s.cmdSSDP = exec.Command("minissdpd", args...) //nolint:gosec
	s.cmdSSDP.Stdout = os.Stdout
	s.cmdSSDP.Stderr = os.Stderr

	return s.cmdSSDP.Start()
}

func (s *MiniUPNPdServer) startMiniUPNPd() error {
	cfgFile, err := s.createMiniUPNPdConfig()
	if err != nil {
		return err
	}

	s.cmdUPNP = exec.Command("miniupnpd", //nolint:gosec
		"-d",                 // Debug mode : do not go to background, output messages on console and do not filter out low priority messages.
		"-f", cfgFile.Name(), // Load the config from file.
	)
	s.cmdUPNP.Stdout = os.Stdout
	s.cmdUPNP.Stderr = os.Stderr

	return s.cmdUPNP.Start()
}

func TestMiniUPNPd(t *testing.T) {
	s := NewMiniUPNPdServer(MiniUPNPdServerOptions{
		EnableNATPMP:      true,
		EnableUPNP:        true,
		ExternalInterface: "enp0s5",
		ExternalIP:        net.ParseIP("109.42.177.161"),
		ListeningIP:       []string{"lo"},
		STUN: STUNOptions{
			Enable: false,
			Host:   "stun.l.google.com",
			Port:   19302,
		},
		SSDP: SSDPOptions{
			Interfaces: []string{"lo"},
		},
	})

	err := s.Start()
	require.NoError(t, err)

	time.Sleep(10 * time.Second)

	err = s.Close()
	require.NoError(t, err)
}
