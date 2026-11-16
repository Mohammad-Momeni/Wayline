package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestShareLinkImportsIntoManagedXray(t *testing.T) {
	vmess := base64.StdEncoding.EncodeToString([]byte(`{"v":"2","add":"203.0.113.10","port":"20972","id":"00000000-0000-4000-8000-000000000001","aid":"0","scy":"auto","net":"tcp","type":"none","tls":"","insecure":"0"}`))
	for _, share := range []string{
		"vmess://" + vmess,
		"vmess\\://" + vmess,
		"vless://00000000-0000-4000-8000-000000000001@example.test:443?security=tls&type=ws&host=edge.example.test&path=%2Fvpn#name",
		"trojan://secret@example.test:443?security=tls&sni=example.test",
		"ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:secret")) + "@example.test:8388#name",
	} {
		config, err := importXray([]byte(share))
		if err != nil {
			t.Fatal(err)
		}
		runtime, err := xrayConfig(config, "", Hop{Host: "127.0.0.1", Port: 1090, Username: "test", Password: "secret"}, 1091)
		if err != nil {
			t.Fatal(err)
		}
		var parsed map[string]any
		if json.Unmarshal(runtime, &parsed) != nil || !strings.Contains(string(runtime), `"dialerProxy":"wayline-vpn"`) {
			t.Fatal("imported outbound lost its VPN detour")
		}
		if binary := os.Getenv("WAYLINE_XRAY"); binary != "" {
			command := exec.Command(binary, "run", "-test", "-config", "stdin:")
			command.Stdin = strings.NewReader(string(runtime))
			configureXrayProcess(command)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("imported config was rejected by Xray: %v %s", err, output)
			}
		}
	}
}

func TestShareImportRejectsUnsupportedOrInsecureOptions(t *testing.T) {
	for _, text := range []string{
		"vmess://bad",
		"vless://id@example.test:0",
		"trojan://secret@example.test:443?allowInsecure=1",
		"vless://id@example.test:443?type=quic",
		"vless://id@example.test:443?security=reality",
		"ss://invalid@example.test:443?plugin=unhandled",
		"https://example.test/subscription",
	} {
		if _, err := importXray([]byte(text)); err == nil {
			t.Fatal("unsupported share link was accepted")
		}
	}
}

func TestDisabledHopKeepsProfileOrderAndSkipsValidation(t *testing.T) {
	disabled := false
	c := Config{Gateway: "example.test", Port: 443, Username: "user", Password: "secret", Mode: "proxy", ListenPort: 1080,
		After: []Hop{{Enabled: &disabled, Host: "127.0.0.1"}, {Type: "socks5", Host: "exit.example.test", Port: 1080}}}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	hops := activeHops(c.After)
	if len(hops) != 1 || hops[0].Host != "exit.example.test" || len(c.After) != 2 {
		t.Fatal("active hops changed saved hop order")
	}
}

func TestXrayImportPreservesDisplayNames(t *testing.T) {
	vmess := base64.StdEncoding.EncodeToString([]byte(`{"ps":"My named endpoint","add":"203.0.113.10","port":"443","id":"00000000-0000-4000-8000-000000000001","aid":"0","net":"tcp"}`))
	for _, input := range []string{"vmess://" + vmess, "vless://00000000-0000-4000-8000-000000000001@example.test:443?security=tls#My%20named%20endpoint"} {
		data, err := importXray([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			Name string `json:"remarks"`
		}
		json.Unmarshal(data, &config)
		if config.Name != "My named endpoint" {
			t.Fatalf("display name lost: %q", config.Name)
		}
	}
}
