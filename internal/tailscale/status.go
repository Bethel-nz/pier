package tailscale

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
)

// ErrInvalidStatus identifies malformed Tailscale Serve/Funnel status JSON.
var ErrInvalidStatus = errors.New("invalid Tailscale routing status")

// Route is one HTTPS proxy handler configured through Tailscale Serve or Funnel.
type Route struct {
	HTTPSPort uint16
	Path      string
	Target    string
	Public    bool
}

// ParseStatus decodes the route fields Pier needs from Tailscale Serve/Funnel status.
func ParseStatus(data []byte) (Status, error) {
	var response serveStatusResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return Status{}, fmt.Errorf("%w: %v", ErrInvalidStatus, err)
	}

	var status Status
	for hostPort, web := range response.Web {
		host, portText, err := net.SplitHostPort(hostPort)
		if err != nil {
			continue
		}
		portNumber, err := strconv.ParseUint(portText, 10, 16)
		if err != nil {
			continue
		}
		port := uint16(portNumber)
		listener, ok := response.TCP[strconv.FormatUint(portNumber, 10)]
		if !ok || !listener.HTTPS {
			continue
		}

		if status.DNSName == "" || host < status.DNSName {
			status.DNSName = host
		}
		for path, handler := range web.Handlers {
			if handler.Proxy == "" {
				continue
			}
			status.Routes = append(status.Routes, Route{
				HTTPSPort: port,
				Path:      path,
				Target:    handler.Proxy,
				Public:    response.AllowFunnel[hostPort],
			})
		}
	}

	sort.Slice(status.Routes, func(i, j int) bool {
		if status.Routes[i].HTTPSPort != status.Routes[j].HTTPSPort {
			return status.Routes[i].HTTPSPort < status.Routes[j].HTTPSPort
		}
		return status.Routes[i].Path < status.Routes[j].Path
	})
	return status, nil
}

type serveStatusResponse struct {
	TCP map[string]struct {
		HTTPS bool `json:"HTTPS"`
	} `json:"TCP"`
	Web map[string]struct {
		Handlers map[string]struct {
			Proxy string `json:"Proxy"`
		} `json:"Handlers"`
	} `json:"Web"`
	AllowFunnel map[string]bool `json:"AllowFunnel"`
}
