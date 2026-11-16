package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// Only validates the generated configuration and a mixed-proxy child process.
// It never creates an adapter or alters the machine's routes/DNS.
func TestSingBoxConfigAndGracefulShutdown(t *testing.T) {
	binary := os.Getenv("WAYLINE_SINGBOX")
	if binary == "" {
		t.Skip("set WAYLINE_SINGBOX to validate bundled sing-box")
	}
	c := Config{ListenPort: 1080, After: []Hop{{Type: "socks5", Host: "proxy.example.com", Port: 1080}}}
	c.localExitExecutable = `C:\Program Files\v2rayN\bin\xray.exe`
	config := tunConfig(c, []string{"198.51.100.53"}, []string{"127.0.0.0/8", "203.0.113.1/32"}, 1090)
	var rule map[string]any
	for _, item := range config["route"].(map[string]any)["rules"].([]any) {
		candidate := item.(map[string]any)
		if candidate["process_path_regex"] != nil {
			rule = candidate
		}
	}
	if rule == nil {
		t.Fatal("missing local exit routing rule")
	}
	pattern := regexp.MustCompile(rule["process_path_regex"].([]string)[0])
	if rule["outbound"] != "raw-vpn" || !pattern.MatchString(`c:\program files\v2rayn\bin\XRAY.EXE`) || pattern.MatchString(`C:\other\xray.exe`) {
		t.Fatal("local exit must route the exact executable through the raw VPN")
	}
	data, _ := json.Marshal(config)
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, data, 0600)
	cmd := exec.Command(binary, "check", "-c", path)
	configureProcess(cmd)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("sing-box config invalid: %v %s", e, out)
	}
	// Exercise identical child console/shutdown handling with a route-free config.
	os.WriteFile(path, []byte(`{"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":0}],"outbounds":[{"type":"direct"}]}`), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd = exec.CommandContext(ctx, binary, "run", "-c", path)
	logs := newStartupLog()
	cmd.Stdout = logs
	cmd.Stderr = logs
	configureProcess(cmd)
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-logs.ready:
	case e := <-done:
		t.Fatalf("child failed to start: %v", e)
	case <-time.After(8 * time.Second):
		cmd.Process.Kill()
		<-done
		t.Fatal("child startup timed out")
	}
	stopProcess(cmd)
	select {
	case e := <-done:
		if e != nil {
			t.Fatalf("child did not exit cleanly: %v", e)
		}
	case <-time.After(3 * time.Second):
		cmd.Process.Kill()
		<-done
		t.Fatal("child ignored graceful shutdown")
	}
}

func TestAppFiltersPreserveControlAndLocalProxyRouting(t *testing.T) {
	for _, mode := range []string{"include", "exclude"} {
		c := Config{ListenPort: 1080, AppFilterMode: mode, AppFilterApps: []string{`C:\Apps\browser.exe`}, controlExecutable: `C:\Wayline\engine.exe`, localExitExecutable: `C:\Proxy\xray.exe`}
		config := tunConfig(c, []string{"198.51.100.53"}, []string{"127.0.0.0/8"}, 1090)
		rules := config["route"].(map[string]any)["rules"].([]any)
		if rules[0].(map[string]any)["outbound"] != "control-direct" {
			t.Fatal("engine control must bypass app filters")
		}
		exitIndex, filterIndex := -1, -1
		for i, value := range rules {
			rule := value.(map[string]any)
			if rule["outbound"] == "raw-vpn" {
				exitIndex = i
			}
			if rule["outbound"] == "control-direct" && rule["process_path_regex"] != nil {
				filterIndex = i
				if (rule["invert"] == true) != (mode == "include") {
					t.Fatal("wrong app filter polarity")
				}
			}
		}
		if exitIndex < 0 || filterIndex <= exitIndex {
			t.Fatal("local exit app must retain raw VPN routing before app exclusions")
		}
		if binary := os.Getenv("WAYLINE_SINGBOX"); binary != "" {
			data, _ := json.Marshal(config)
			path := filepath.Join(t.TempDir(), "apps.json")
			os.WriteFile(path, data, 0600)
			cmd := exec.Command(binary, "check", "-c", path)
			configureProcess(cmd)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("invalid %s app filter: %v %s", mode, err, output)
			}
		}
	}
}
