package consul

import (
	"net"
	"net/url"
)

func isIP(s string) bool {
	return net.ParseIP(s) != nil
}

func parseHost(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	return u.Hostname(), nil
}

func replaceHostname(rawURL, newHost string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	// 保留原端口
	port := u.Port()

	if port != "" {
		u.Host = newHost + ":" + port
	} else {
		u.Host = newHost
	}

	return u.String(), nil
}
