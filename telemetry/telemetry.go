package telemetry

import (
	"fmt"
	"net"
	"net/http"
	"sync"

	log "github.com/Sirupsen/logrus"
	"github.com/joyent/containerpilot/utils"
	"github.com/prometheus/client_golang/prometheus"
)

// Telemetry represents the service to advertise for finding the metrics
// endpoint, and the collection of Sensors.
type Telemetry struct {
	Port          int           `mapstructure:"port"`
	Interfaces    []interface{} `mapstructure:"interfaces"` // optional override
	Tags          []string      `mapstructure:"tags"`
	SensorConfigs []interface{} `mapstructure:"sensors"`
	Sensors       []*Sensor
	ServiceName   string
	URL           string
	TTL           int
	Poll          int
	mux           *http.ServeMux
	lock          sync.RWMutex
	listen        net.Listener
	addr          net.TCPAddr
	listening     bool
}

// NewTelemetry configures a new prometheus Telemetry server
func NewTelemetry(raw interface{}) (*Telemetry, error) {
	t := &Telemetry{
		Port:        9090,
		ServiceName: "containerpilot",
		URL:         "/metrics",
		TTL:         15,
		Poll:        5,
		lock:        sync.RWMutex{},
	}

	if err := utils.DecodeRaw(raw, t); err != nil {
		return nil, fmt.Errorf("Telemetry configuration error: %v", err)
	}
	ipAddress, err := utils.IPFromInterfaces(t.Interfaces)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(ipAddress)
	t.addr = net.TCPAddr{IP: ip, Port: t.Port}
	t.mux = http.NewServeMux()
	t.mux.Handle(t.URL, prometheus.Handler())
	// note that we don't return an error if there are no sensors
	// because the prometheus handler will still pick up metrics
	// internal to ContainerPilot (i.e. the golang runtime)
	if t.SensorConfigs != nil {
		sensors, err := NewSensors(t.SensorConfigs)
		if err != nil {
			return nil, err
		}
		t.Sensors = sensors
	}
	return t, nil
}

func (t *Telemetry) isListening() bool {
	t.lock.RLock()
	defer t.lock.RUnlock()
	return t.listening
}

// Serve starts serving the telemetry service
func (t *Telemetry) Serve() {
	if t.isListening() {
		log.Debugf("telemetry: Already listening on %s", t.addr.String())
		return
	}
	t.lock.Lock()
	defer t.lock.Unlock()
	ln, err := net.Listen(t.addr.Network(), t.addr.String())
	if err != nil {
		log.Fatalf("FATAL Error serving telemetry on %s: %v", t.addr.String(), err)
	}
	t.listen = ln
	t.listening = true
	go func() {
		if !t.isListening() {
			log.Debugf("telemetry: Is not listening")
			return
		}
		log.Debugf("telemetry: Listening on %s", t.addr.String())
		err := http.Serve(t.listen, t.mux)
		if !t.isListening() {
			// When Shutdown closes the underlying TCP listener, http.Serve will
			// throw an error: accept tcp xx.xx.xx.xx:9090: use of closed network connection
			// This is expected, so we can ignore it.
			err = nil
		}
		if err != nil {
			log.Fatalf("telemetry: FATAL error in telemetry HTTP server: %v", err)
		}
		log.Debugf("telemetry: Stopped listening on %s", t.addr.String())
	}()
}

// Shutdown shuts down the telemetry service
func (t *Telemetry) Shutdown() {
	t.lock.Lock()
	defer t.lock.Unlock()
	if t.listening {
		log.Debugf("telemetry: Shutdown listener %s", t.listen.Addr().String())
		t.listening = false
		if err := t.listen.Close(); err != nil {
			log.Errorf("telemetry: listener shutdown failed: %v", err)
			return
		}
		t.listen = nil
	}
}
