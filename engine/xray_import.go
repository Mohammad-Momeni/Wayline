package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func decodeShare(value string) ([]byte, error) {
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if data, err := encoding.DecodeString(value); err == nil {
			return data, nil
		}
	}
	return nil, errors.New("invalid share link encoding")
}

// The import command is shared by all frontends. It never contacts the endpoint.
func importXray(data []byte) ([]byte, error) {
	text := strings.TrimSpace(string(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	text = strings.ReplaceAll(text, "\\://", "://")
	if strings.HasPrefix(text, "{") {
		var config map[string]any
		if json.Unmarshal([]byte(text), &config) != nil {
			return nil, errors.New("invalid JSON")
		}
		if outbounds, ok := config["outbounds"].([]any); !ok || len(outbounds) == 0 {
			return nil, errors.New("config needs outbounds")
		}
		return json.Marshal(config)
	}
	var outbound map[string]any
	var err error
	if strings.HasPrefix(strings.ToLower(text), "vmess://") {
		outbound, err = importVMess(text[len("vmess://"):])
	} else {
		outbound, err = importURI(text)
	}
	if err != nil {
		return nil, err
	}
	name := ""
	if strings.HasPrefix(strings.ToLower(text), "vmess://") {
		if decoded, e := decodeShare(text[len("vmess://"):]); e == nil {
			var v struct {
				Name string `json:"ps"`
			}
			json.Unmarshal(decoded, &v)
			name = v.Name
		}
	} else if parsed, e := url.Parse(text); e == nil {
		name = parsed.Fragment
	}
	if strings.TrimSpace(name) == "" {
		name, _ = outbound["tag"].(string)
	}
	return json.Marshal(map[string]any{"remarks": name, "outbounds": []any{outbound}})
}

func sharePort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("invalid share port")
	}
	return port, nil
}

func importVMess(encoded string) (map[string]any, error) {
	data, err := decodeShare(encoded)
	if err != nil {
		return nil, err
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(data, &values) != nil {
		return nil, errors.New("invalid VMess share JSON")
	}
	get := func(key string) string {
		var text string
		if json.Unmarshal(values[key], &text) == nil {
			return text
		}
		var number json.Number
		if json.Unmarshal(values[key], &number) == nil {
			return number.String()
		}
		return ""
	}
	port, err := sharePort(get("port"))
	if err != nil || !validHost(get("add")) || get("id") == "" {
		return nil, errors.New("invalid VMess endpoint")
	}
	if aid := get("aid"); aid != "" && aid != "0" {
		return nil, errors.New("legacy VMess alterId is unsupported")
	}
	if get("insecure") == "1" || strings.EqualFold(get("insecure"), "true") {
		return nil, errors.New("share link requests insecure TLS")
	}
	query := url.Values{"type": {get("net")}, "headerType": {get("type")}, "host": {get("host")},
		"path": {get("path")}, "security": {get("tls")}, "sni": {get("sni")}, "alpn": {get("alpn")}, "fp": {get("fp")}}
	stream, err := shareStream(query)
	if err != nil {
		return nil, err
	}
	security := get("scy")
	if security == "" {
		security = "auto"
	}
	return map[string]any{"tag": "imported", "protocol": "vmess", "settings": map[string]any{
		"vnext": []any{map[string]any{"address": get("add"), "port": port, "users": []any{map[string]any{"id": get("id"), "security": security}}}},
	}, "streamSettings": stream}, nil
}

func importURI(text string) (map[string]any, error) {
	uri, err := url.Parse(text)
	if err != nil || uri.User == nil {
		return nil, errors.New("invalid share URI")
	}
	protocol := strings.ToLower(uri.Scheme)
	if protocol != "vless" && protocol != "trojan" && protocol != "ss" {
		return nil, errors.New("unsupported share protocol")
	}
	if uri.Path != "" && uri.Path != "/" {
		return nil, errors.New("unexpected share URI path")
	}
	port, err := sharePort(uri.Port())
	if err != nil || !validHost(uri.Hostname()) {
		return nil, errors.New("invalid share endpoint")
	}
	query, err := url.ParseQuery(uri.RawQuery)
	if err != nil {
		return nil, errors.New("invalid share query encoding")
	}
	for key := range query {
		switch key {
		case "type", "security", "sni", "peer", "fp", "alpn", "host", "path", "headerType", "serviceName", "mode", "flow", "encryption", "pbk", "sid", "spx", "allowInsecure", "insecure":
		default:
			return nil, fmt.Errorf("unsupported share option %s", key)
		}
	}
	if query.Get("security") == "" && protocol == "trojan" {
		query.Set("security", "tls")
	}
	stream, err := shareStream(query)
	if err != nil {
		return nil, err
	}
	outbound := map[string]any{"tag": "imported", "protocol": protocol, "streamSettings": stream}
	user := uri.User.Username()
	switch protocol {
	case "vless":
		if user == "" {
			return nil, errors.New("missing VLESS id")
		}
		encryption := query.Get("encryption")
		if encryption == "" {
			encryption = "none"
		}
		if encryption != "none" {
			return nil, errors.New("unsupported VLESS encryption")
		}
		outbound["settings"] = map[string]any{"vnext": []any{map[string]any{"address": uri.Hostname(), "port": port,
			"users": []any{map[string]any{"id": user, "encryption": encryption, "flow": query.Get("flow")}}}}}
	case "trojan":
		if user == "" {
			return nil, errors.New("missing Trojan password")
		}
		outbound["settings"] = map[string]any{"servers": []any{map[string]any{"address": uri.Hostname(), "port": port, "password": user}}}
	case "ss":
		password, plain := uri.User.Password()
		if !plain {
			decoded, err := decodeShare(user)
			if err != nil {
				return nil, err
			}
			user, password, plain = strings.Cut(string(decoded), ":")
		}
		if !plain || user == "" || password == "" {
			return nil, errors.New("invalid Shadowsocks credentials")
		}
		outbound["protocol"] = "shadowsocks"
		outbound["settings"] = map[string]any{"servers": []any{map[string]any{"address": uri.Hostname(), "port": port, "method": user, "password": password}}}
	}
	return outbound, nil
}

func shareStream(query url.Values) (map[string]any, error) {
	for _, key := range []string{"allowInsecure", "insecure"} {
		if value := strings.ToLower(query.Get(key)); value != "" && value != "0" && value != "false" {
			return nil, errors.New("share link requests insecure TLS")
		}
	}
	network := query.Get("type")
	stream := map[string]any{}
	switch network {
	case "", "tcp", "raw":
		network = "raw"
		header := query.Get("headerType")
		if header == "http" {
			request := map[string]any{"path": []string{"/"}}
			if query.Get("path") != "" {
				request["path"] = strings.Split(query.Get("path"), ",")
			}
			if query.Get("host") != "" {
				request["headers"] = map[string]any{"Host": strings.Split(query.Get("host"), ",")}
			}
			stream["rawSettings"] = map[string]any{"header": map[string]any{"type": "http", "request": request}}
		} else if header != "" && header != "none" {
			return nil, errors.New("unsupported TCP header")
		}
	case "ws":
		stream["wsSettings"] = map[string]any{"path": query.Get("path"), "headers": map[string]string{"Host": query.Get("host")}}
	case "grpc":
		stream["grpcSettings"] = map[string]any{"serviceName": query.Get("serviceName"), "multiMode": query.Get("mode") == "multi"}
	case "httpupgrade":
		stream["httpupgradeSettings"] = map[string]any{"path": query.Get("path"), "host": query.Get("host")}
	case "xhttp", "splithttp":
		network = "xhttp"
		mode := query.Get("mode")
		if mode == "" {
			mode = "auto"
		}
		stream["xhttpSettings"] = map[string]any{"path": query.Get("path"), "host": query.Get("host"), "mode": mode}
	default:
		return nil, errors.New("unsupported share transport")
	}
	stream["network"] = network
	security := query.Get("security")
	if security == "" {
		security = "none"
	}
	switch security {
	case "none":
	case "tls", "reality":
		sni := query.Get("sni")
		if sni == "" {
			sni = query.Get("peer")
		}
		settings := map[string]any{"serverName": sni}
		if fp := query.Get("fp"); fp != "" {
			settings["fingerprint"] = fp
		}
		if security == "tls" {
			if alpn := query.Get("alpn"); alpn != "" {
				settings["alpn"] = strings.Split(alpn, ",")
			}
		} else {
			if query.Get("pbk") == "" {
				return nil, errors.New("REALITY requires a public key")
			}
			settings["publicKey"], settings["shortId"], settings["spiderX"] = query.Get("pbk"), query.Get("sid"), query.Get("spx")
		}
		stream[security+"Settings"] = settings
	default:
		return nil, errors.New("unsupported share security")
	}
	stream["security"] = security
	return stream, nil
}
