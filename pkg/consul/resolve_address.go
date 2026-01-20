package consul

import (
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/exp/rand"
)

// 如果 address 是域名，则解析替换为具体的ip
func resolveAddress(address *string, resolver func(string) ([]net.IP, error)) (err error) {
	// 获取 host
	var host = *address
	if strings.Contains(*address, "://") {
		host, err = parseHost(*address)
		if err != nil {
			return errors.WithMessage(err, "无法解析consul address host")
		}
	}

	// 如果是 ip 则不处理
	if isIP(host) {
		return nil
	}

	// 解析 host
	ips, err := resolver(host)
	if err != nil {
		return errors.WithMessage(err, "无法解析consul地址")
	}
	if len(ips) == 0 {
		return errors.New("无法解析consul地址: 无地址")
	}
	// 这里选取策略后面在改 是否要使用一个稳定性的标识作为选取策略还是要随机
	// 解析host成功，随机取一个作为consul节点
	ip := ips[rand.New(rand.NewSource(uint64(time.Now().UnixNano()))).Intn(len(ips))]
	*address, _ = replaceHostname(*address, ip.String())
	return nil
}

// 判断是否为 ip
func isIP(s string) bool {
	return net.ParseIP(s) != nil
}

// 解析 url 中的host
func parseHost(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	return u.Hostname(), nil
}

// 替换 url 中的 host 为 ip
func replaceHostname(rawURL, ip string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	// 保留原端口
	port := u.Port()

	if port != "" {
		u.Host = ip + ":" + port
	} else {
		u.Host = ip
	}

	return u.String(), nil
}
