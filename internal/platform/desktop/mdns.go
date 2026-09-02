package desktop

import (
	"os"
	"strconv"

	"github.com/grandcat/zeroconf"
)

// ServiceType is the mDNS/DNS-SD service the desktop client browses for.
const ServiceType = "_varyaone._tcp"

// Advertiser publishes this server on the local network so the thin client can
// discover it without the user typing an IP.
type Advertiser struct{ server *zeroconf.Server }

// Advertise registers "<host> Varya One" on ServiceType at the given HTTP port.
// The returned Advertiser must be Shutdown on exit.
func Advertise(httpPort int) (*Advertiser, error) {
	host, _ := os.Hostname()
	if host == "" {
		host = "Varya One"
	}
	instance := host + " — Varya One"
	srv, err := zeroconf.Register(instance, ServiceType, "local.", httpPort,
		[]string{"path=/", "port=" + strconv.Itoa(httpPort)}, nil)
	if err != nil {
		return nil, err
	}
	return &Advertiser{server: srv}, nil
}

func (a *Advertiser) Shutdown() {
	if a != nil && a.server != nil {
		a.server.Shutdown()
	}
}
