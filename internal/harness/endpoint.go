package harness

import (
	"errors"
	"net"
	"net/url"
	"strconv"
)

func LocalEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Port() == "" {
		return errors.New("fixture endpoint must use loopback HTTP: http://127.0.0.1:<port> or http://[::1]:<port>")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return errors.New("fixture endpoint requires a valid port")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return errors.New("fixture endpoint must use a literal loopback IP")
	}
	return nil
}
