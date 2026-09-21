package validation

import (
	"errors"
	"fmt"
	"net"
	"testing"
)

func TestAddressValidation(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  error
	}{
		{"8.8.8.8", nil}, {"2001:4860:4860::8888", nil},
		{"0.0.0.0", ErrUnsafeIP}, {"::", ErrUnsafeIP}, {"127.0.0.1", ErrUnsafeIP}, {"::1", ErrUnsafeIP},
		{"10.1.2.3", ErrUnsafeIP}, {"172.16.0.1", ErrUnsafeIP}, {"192.168.1.1", ErrUnsafeIP},
		{"169.254.1.1", ErrUnsafeIP}, {"fe80::1", ErrUnsafeIP}, {"ff02::1", ErrUnsafeIP},
		{"::ffff:127.0.0.1", ErrUnsafeIP},
	} {
		if err := IsSafeIP(net.ParseIP(tt.input)); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v", tt.input, err)
		}
	}
	// 使用字面 IP，避免测试依赖公网 DNS 或开发者机器上的服务。
	for _, tt := range []struct {
		input string
		want  error
	}{
		{"", nil}, {"https://8.8.8.8/path", nil}, {"http://[2001:4860:4860::8888]", nil},
		{"http://127.0.0.1", ErrUnsafeIP}, {"http://10.0.0.1", ErrUnsafeIP},
		{"https://localhost", ErrCallbackURLLocalhost}, {"http://LOCALHOST", ErrCallbackURLLocalhost},
		{"ftp://8.8.8.8", ErrCallbackURLInvalidScheme}, {"https:///path", ErrCallbackURLMissingHost}, {"http://%", ErrCallbackURLInvalidFormat},
	} {
		err := IsSafeCallbackURL(tt.input)
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: %v", tt.input, err)
		}
		if err != nil && !errors.Is(fmt.Errorf("validate callback: %w", err), tt.want) {
			t.Fatal("wrapped error lost its identity")
		}
	}
}
