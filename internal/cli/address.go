package cli

import (
	"errors"
	"net"
	"strconv"
)

func validateListen(address string) error {
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return errors.New("receiver listen address must be loopback")
	}
	ip := net.ParseIP(host)
	number, e := strconv.Atoi(port)
	if ip == nil || !ip.IsLoopback() || e != nil || number < 1 || number > 65535 {
		return errors.New("receiver listen address must be loopback with a valid port")
	}
	return nil
}
