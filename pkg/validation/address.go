package validation

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

var (
	// ErrUnsafeIP 表示 IP 属于未指定、回环、私网或链路本地地址。
	ErrUnsafeIP = errors.New("address is private or local")
	// ErrCallbackURLInvalidFormat 表示回调 URL 语法无效。
	ErrCallbackURLInvalidFormat = errors.New("invalid callback url format")
	// ErrCallbackURLInvalidScheme 表示回调 URL 协议不是 HTTP(S)。
	ErrCallbackURLInvalidScheme = errors.New("callback url scheme must be http or https")
	// ErrCallbackURLMissingHost 表示回调 URL 缺少主机名。
	ErrCallbackURLMissingHost = errors.New("callback url hostname is empty")
	// ErrCallbackURLLocalhost 表示回调目标为 localhost。
	ErrCallbackURLLocalhost = errors.New("callback to localhost is not allowed")
	// ErrCallbackURLDNSLookup 表示回调主机名解析失败。
	ErrCallbackURLDNSLookup = errors.New("callback url dns lookup failed")
	// ErrCallbackURLNoIP 表示回调主机名未解析出 IP。
	ErrCallbackURLNoIP = errors.New("no ip found for callback hostname")
)

// IsSafeIP 拒绝未指定、回环、私网及链路本地地址，失败返回 ErrUnsafeIP。
// 调用方须传入有效 IP；本函数不是完整的公网地址分类器。
func IsSafeIP(ip net.IP) error {
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast() {
		return ErrUnsafeIP
	}

	// 防止IPv4映射的IPv6地址
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 127 {
			return ErrUnsafeIP
		}
	}
	return nil
}

// IsSafeCallbackURL 校验 HTTP(S) URL 并检查解析时的 IP；空字符串允许，不能替代请求时的 SSRF 防护。
// 失败返回可通过 errors.Is 识别的 ErrCallbackURL 系列错误或 ErrUnsafeIP。
func IsSafeCallbackURL(rawURL string) error {
	if rawURL == "" {
		return nil
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		// URL 解析错误可能包含凭据或查询参数，不直接透传。
		return ErrCallbackURLInvalidFormat
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return ErrCallbackURLInvalidScheme
	}

	hostname := u.Hostname()
	if hostname == "" {
		return ErrCallbackURLMissingHost
	}

	if strings.ToLower(hostname) == "localhost" {
		return ErrCallbackURLLocalhost
	}

	// 解析IP
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCallbackURLDNSLookup, err)
	}

	if len(ips) == 0 {
		return ErrCallbackURLNoIP
	}

	for _, ip := range ips {
		if err := IsSafeIP(ip); err != nil {
			return err
		}
	}

	return nil
}
