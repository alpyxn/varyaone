package desktop

import (
	"fmt"
	"net"
	"sort"
)

// LANURLs returns the http URLs other machines on the network can use to reach
// this server, one per non-loopback IPv4 interface address.
func LANURLs(port int) []string {
	var urls []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return urls
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		ip4 := ipNet.IP.To4()
		if ip4 == nil {
			continue
		}
		urls = append(urls, fmt.Sprintf("http://%s:%d", ip4.String(), port))
	}
	sort.Strings(urls)
	return urls
}
