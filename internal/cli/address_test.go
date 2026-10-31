package cli

import "testing"

func TestReceiverListenIsLoopbackOnly(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8127", ":8127", "192.0.2.1:8127", "127.0.0.1:0", "127.0.0.1:99999", "example.com:8127"} {
		if e := validateListen(address); e == nil {
			t.Fatalf("unsafe listen address accepted: %s", address)
		}
	}
	for _, address := range []string{"127.0.0.1:8127", "[::1]:8127"} {
		if e := validateListen(address); e != nil {
			t.Fatal(e)
		}
	}
}
