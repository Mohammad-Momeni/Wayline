package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"wayline/native"
	"wayline/netstack"
	"wayline/ppp"
)

type Config struct {
	AppFilterMode       string    `json:"appFilterMode,omitempty"`
	AppFilterApps       []string  `json:"appFilterApps,omitempty"`
	Gateway             string    `json:"gateway"`
	Port                int       `json:"port"`
	Username            string    `json:"username"`
	Password            string    `json:"password"`
	Realm               string    `json:"realm"`
	TrustedCertificates []string  `json:"trustedCertificates,omitempty"`
	CertificateSHA256   string    `json:"certificateSha256"`
	Mode                string    `json:"mode"`
	ListenPort          int       `json:"listenPort"`
	Before              []Hop     `json:"before"`
	After               []Hop     `json:"after"`
	DNS                 []string  `json:"dns"`
	BootstrapDNS        []string  `json:"bootstrapDns"`
	TunName             string    `json:"tunName"`
	XrayExit            *XrayExit `json:"xrayExit,omitempty"`
	AndroidLocalExit    bool      `json:"androidLocalExit,omitempty"`
	AutoReconnect       bool      `json:"autoReconnect,omitempty"`
	localExitExecutable string
	controlExecutable   string
}

var outputMu sync.Mutex

func emit(kind, message string, fields ...map[string]any) {
	value := map[string]any{"type": kind, "message": message}
	for _, f := range fields {
		for k, v := range f {
			value[k] = v
		}
	}
	outputMu.Lock()
	defer outputMu.Unlock()
	json.NewEncoder(os.Stdout).Encode(value)
}
func main() {
	if len(os.Args) == 2 && os.Args[1] == "--import-xray" {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+1))
		if err == nil && len(data) <= 1<<20 {
			var config []byte
			config, err = importXray(data)
			if err == nil {
				os.Stdout.Write(config)
				return
			}
		}
		fmt.Fprintln(os.Stderr, "Use Xray JSON or a supported vmess://, vless://, trojan:// or ss:// share link (up to 1 MiB).")
		os.Exit(1)
	}
	if len(os.Args) != 2 || os.Args[1] != "--session" {
		fmt.Fprintln(os.Stderr, "Wayline engine 0.5.0. Start with --session; supply one config JSON line on stdin, close stdin to disconnect.")
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	reader := bufio.NewReaderSize(os.Stdin, 65536)
	line, e := reader.ReadSlice('\n')
	if e != nil || len(line) > 65535 {
		emit("error", "Session configuration must be one JSON line under 64 KiB")
		os.Exit(1)
	}
	var config Config
	e = json.Unmarshal(line, &config)
	clear(line)
	if e != nil {
		emit("error", "Invalid session JSON")
		os.Exit(1)
	}
	go func() { io.Copy(io.Discard, reader); cancel() }()
	e = runSession(ctx, config)
	config.Password = ""
	if e != nil && !errors.Is(e, context.Canceled) {
		var cert *native.CertificateRejected
		if errors.As(e, &cert) {
			emit("certificate", "Gateway certificate needs verification. Confirm its SHA-256 with your IT team before saving it.", map[string]any{"sha256": cert.Digest, "subject": cert.Subject, "issuer": cert.Issuer})
		} else {
			emit("error", e.Error(), map[string]any{"retryable": retryableSessionError(e)})
		}
		os.Exit(1)
	}
	emit("status", "Disconnected")
}
func (c Config) validate() error {
	pins := append([]string(nil), c.TrustedCertificates...)
	if c.CertificateSHA256 != "" {
		pins = append(pins, strings.ToLower(strings.ReplaceAll(c.CertificateSHA256, ":", "")))
	}
	if len(pins) > 32 {
		return errors.New("too many trusted fingerprints")
	}
	for _, value := range pins {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 32 || value != strings.ToLower(value) {
			return errors.New("invalid trusted SHA-256 fingerprint")
		}
	}

	if c.AppFilterMode != "" && c.AppFilterMode != "all" && c.AppFilterMode != "include" && c.AppFilterMode != "exclude" {
		return errors.New("invalid app filter mode")
	}
	if c.Mode == "vpn" && c.AppFilterMode == "include" && len(c.AppFilterApps) == 0 {
		return errors.New("select at least one app for Only selected apps")
	}
	if len(c.AppFilterApps) > 512 {
		return errors.New("too many app filter entries")
	}
	for _, app := range c.AppFilterApps {
		if app == "" || len(app) > 4096 || strings.ContainsRune(app, 0) {
			return errors.New("invalid app filter entry")
		}
	}

	c.Before, c.After = activeHops(c.Before), activeHops(c.After)
	if !validHost(c.Gateway) || c.Port < 1 || c.Port > 65535 || c.Username == "" || c.Password == "" {
		return errors.New("gateway, port, username and password are required")
	}
	if c.Mode != "proxy" && c.Mode != "vpn" {
		return errors.New("mode must be proxy or vpn")
	}
	if c.ListenPort < 1024 || c.ListenPort > 65535 {
		return errors.New("local proxy port must be 1024–65535")
	}
	if e := validateHops(c.Before); e != nil {
		return e
	}
	if e := validateHops(c.After); e != nil {
		return e
	}
	if c.XrayExit != nil {
		if err := c.XrayExit.validate(); err != nil {
			return err
		}
	}
	for i, hop := range c.After {
		if !isLoopbackHost(hop.Host) {
			continue
		}
		desktop := runtime.GOOS == "windows" || runtime.GOOS == "linux" || runtime.GOOS == "android" && c.AndroidLocalExit
		if i != 0 || c.XrayExit != nil || !desktop || c.Mode != "vpn" || hop.Type != "socks5" || hop.Port == c.ListenPort || net.ParseIP(hop.Host).To4() == nil && !strings.EqualFold(hop.Host, "localhost") {
			return errors.New("an existing local SOCKS5 exit must be the first After hop in desktop VPN mode, with its own IPv4 loopback port; use managed Xray for Proxy mode or Android")
		}
	}
	if c.Mode == "vpn" && len(c.Before) > 0 {
		host := c.Before[0].Host
		ip := net.ParseIP(host)
		if strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback() {
			return errors.New("Full VPN mode with a local before-VPN proxy can create a routing loop. Use Proxy mode for a local V2Ray/sing-box upstream, or use a remote proxy endpoint")
		}
	}
	for _, d := range c.DNS {
		if net.ParseIP(d).To4() == nil {
			return errors.New("VPN DNS overrides must be IPv4 addresses")
		}
	}
	for _, d := range c.BootstrapDNS {
		if _, err := netip.ParseAddr(d); err != nil {
			return errors.New("bootstrap DNS must be IP addresses")
		}
	}
	return nil
}

func isLoopbackHost(host string) bool {
	return strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") || net.ParseIP(host).IsLoopback()
}

func run(parent context.Context, c Config) error {
	c.Before, c.After = activeHops(c.Before), activeHops(c.After)
	if e := c.validate(); e != nil {
		return e
	}
	if c.XrayExit != nil {
		c.After = append([]Hop{{Type: "xray", Xray: c.XrayExit}}, c.After...)
		c.XrayExit = nil
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	outerDialer := &net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}
	if len(c.BootstrapDNS) > 0 {
		outerDialer.Resolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var last error
			for _, server := range c.BootstrapDNS {
				conn, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(server, "53"))
				if e == nil {
					return conn, nil
				}
				last = e
			}
			return nil, last
		}}
	}
	direct := outerDialer.DialContext
	if len(c.After) > 0 && isLoopbackHost(c.After[0].Host) {
		if strings.EqualFold(c.After[0].Host, "localhost") {
			c.After[0].Host = "127.0.0.1"
		}
		executable, err := localExitProcess(c.After[0])
		if runtime.GOOS == "android" && c.AndroidLocalExit {
			executable, err = "android-local-exit", nil
		}
		if err != nil {
			return err
		}
		c.localExitExecutable = executable
		emit("status", "Local exit proxy detected; its outgoing connections will use the VPN")
	}
	before, stopBefore, beforeErrors, e := prepareChain(ctx, direct, direct, c.Before)
	if e != nil {
		return e
	}
	defer stopBefore()
	client, e := native.NewClient(native.Options{Host: c.Gateway, Port: c.Port, TrustedCert: strings.ToLower(strings.ReplaceAll(c.CertificateSHA256, ":", "")), TrustedCerts: c.TrustedCertificates, DialContext: before, Timeout: 25 * time.Second})
	if e != nil {
		return e
	}
	emit("status", "Authenticating with VPN gateway")
	connection, e := client.Login(ctx, native.Credentials{Username: c.Username, Password: []byte(c.Password), Realm: c.Realm})
	c.Password = ""
	if e != nil {
		return e
	}
	defer func() {
		connection.Close()
		logout, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		connection.Logout(logout)
	}()
	transport := native.NewTransport(ctx, connection)
	defer transport.Close()
	cfg := ppp.Config{StopOnRenegotiation: true, WriteTimeout: 15 * time.Second}
	if len(connection.Config.DNS) > 0 {
		cfg.PrimaryDNS = connection.Config.DNS[0]
	}
	if len(connection.Config.DNS) > 1 {
		cfg.SecondaryDNS = connection.Config.DNS[1]
	}
	emit("status", "Negotiating VPN tunnel")
	info, e := ppp.Negotiate(ctx, transport, cfg)
	if e != nil {
		return e
	}
	defer func() {
		stop, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		info.Link.Close(stop)
	}()
	ns, e := netstack.New(info.LocalIP.String(), uint32(min(info.MRU, info.PeerMRU)), "")
	if e != nil {
		return e
	}
	var workers sync.WaitGroup
	defer func() { cancel(); connection.Close(); workers.Wait(); ns.Close() }()
	var in, out atomic.Int64
	failures := make(chan error, 4)
	workers.Add(2)
	go func() {
		defer workers.Done()
		for {
			packet, e := info.Link.ReadIPv4(ctx)
			if e != nil {
				failures <- e
				return
			}
			in.Add(int64(len(packet)))
			pb := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(packet)})
			ns.Link.InjectInbound(ipv4.ProtocolNumber, pb)
			pb.DecRef()
		}
	}()
	go func() {
		defer workers.Done()
		for {
			pb := ns.Link.ReadContext(ctx)
			if pb == nil {
				return
			}
			b := pb.ToBuffer()
			packet := append([]byte(nil), b.Flatten()...)
			b.Release()
			pb.DecRef()
			out.Add(int64(len(packet)))
			if e := info.Link.WriteIPv4(ctx, packet); e != nil {
				failures <- e
				return
			}
		}
	}()
	dns := append([]string(nil), c.DNS...)
	if len(dns) == 0 {
		for _, ip := range []string{info.PrimaryDNS.String(), info.SecondaryDNS.String()} {
			if net.ParseIP(ip) != nil {
				dns = append(dns, ip)
			}
		}
	}
	gateway := &vpnDialer{ns: ns, dns: dns, suffixes: connection.Config.Domains}
	listener, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", c.ListenPort))
	if e != nil {
		return fmt.Errorf("local proxy port unavailable: %w", e)
	}
	defer listener.Close()
	var rawPort int
	if c.XrayExit != nil || c.Mode == "vpn" && runtime.GOOS != "android" {
		rawListener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return err
		}
		defer rawListener.Close()
		rawPort = rawListener.Addr().(*net.TCPAddr).Port
		rawProxy := &ProxyServer{dial: gateway.DialContext, vpn: gateway, udpAllowed: true}
		workers.Add(1)
		go func() { defer workers.Done(); failures <- rawProxy.Serve(ctx, rawListener) }()
	}
	exitDial := Dial(gateway.DialContext)
	if c.localExitExecutable != "" {
		exitDial = direct
	}
	finalDial, stopAfter, afterErrors, err := prepareChain(ctx, exitDial, direct, c.After)
	if err != nil {
		return err
	}
	defer stopAfter()
	proxy := &ProxyServer{dial: finalDial, vpn: gateway, udpAllowed: len(c.After) == 0}
	var tunErrors <-chan error
	if c.Mode == "vpn" {
		if runtime.GOOS == "android" {
			stop, exited, e := startAndroidTUN(ctx, gateway, dns, proxy.dial, proxy.udpAllowed, c.AndroidLocalExit)
			if e != nil {
				return e
			}
			defer stop()
			tunErrors = exited
		} else {
			stop, exited, e := startTUN(ctx, c, dns, rawPort)
			if e != nil {
				return e
			}
			defer stop()
			tunErrors = exited
		}
	}
	// Start accepting only once the TUN rule is active, and drain exit sessions
	// before the TUN helper removes the route for an externally managed Xray.
	proxyDone := make(chan struct{})
	go func() { defer close(proxyDone); failures <- proxy.Serve(ctx, listener) }()
	defer func() { cancel(); listener.Close(); <-proxyDone }()
	emit("status", "Connected", map[string]any{"localIP": info.LocalIP.String()})
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-tunErrors:
			return e
		case e := <-beforeErrors:
			return e
		case e := <-afterErrors:
			return e
		case e := <-failures:
			cancel()
			return e
		case <-ticker.C:
			emit("stats", "", map[string]any{"bytesIn": in.Load(), "bytesOut": out.Load()})
		}
	}
}

type vpnDialer struct {
	ns            *netstack.NetStack
	dns, suffixes []string
}

func (v *vpnDialer) dialIP(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return nil, e
	}
	var p int
	if _, e = fmt.Sscan(port, &p); e != nil || p < 1 || p > 65535 {
		return nil, errors.New("invalid destination port")
	}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		return nil, errors.New("this SSL-VPN supports IPv4 destinations only")
	}
	a := &tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte(ip)), Port: uint16(p)}
	if network == "udp" {
		return v.ns.DialUDP(ctx, a)
	}
	return v.ns.DialTCP(ctx, a)
}
func (v *vpnDialer) resolve(ctx context.Context, host string) (string, error) {
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() == nil {
			return "", errors.New("IPv6 is unavailable through this VPN")
		}
		return host, nil
	}
	if len(v.dns) == 0 {
		return "", errors.New("gateway supplied no DNS server; set an IPv4 DNS override in the profile")
	}
	candidates := []string{host}
	if !strings.Contains(host, ".") {
		for _, s := range v.suffixes {
			candidates = append(candidates, host+"."+s)
		}
	}
	for _, name := range candidates {
		for _, server := range v.dns {
			for _, transport := range []string{"udp", "tcp"} {
				resolver := &net.Resolver{PreferGo: true, StrictErrors: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return v.dialIP(ctx, transport, net.JoinHostPort(server, "53"))
				}}
				timeout := 700 * time.Millisecond
				if transport == "tcp" {
					timeout = 4 * time.Second
				}
				query, done := context.WithTimeout(ctx, timeout)
				ips, e := resolver.LookupIP(query, "ip4", name)
				done()
				if e == nil && len(ips) > 0 {
					return ips[0].String(), nil
				}
			}
		}
	}
	return "", errors.New("VPN DNS lookup failed")
}
func (v *vpnDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return nil, e
	}
	ip, e := v.resolve(ctx, host)
	if e != nil {
		return nil, e
	}
	return v.dialIP(ctx, network, net.JoinHostPort(ip, port))
}
