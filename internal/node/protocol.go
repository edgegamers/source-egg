package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/edgegamers/source-egg/internal/addons"
	"github.com/edgegamers/source-egg/internal/content"
	"github.com/edgegamers/source-egg/internal/game"
	"github.com/edgegamers/source-egg/internal/passwords"
)

const ProtocolVersion = 1
const SocketDir = "/run/source-node"
const SharedMount = "/opt/source-shared"
const CacheMount = "/opt/source-frameworks"

type Request struct {
	Version    int           `json:"version"`
	Action     string        `json:"action"`
	Identity   game.Identity `json:"identity"`
	Players    int           `json:"players,omitempty"`
	Map        string        `json:"map,omitempty"`
	Name       string        `json:"name,omitempty"`
	Ready      bool          `json:"ready,omitempty"`
	Generation string        `json:"generation,omitempty"`
	Revision   string        `json:"revision,omitempty"`
	ExitCode   int           `json:"exit_code,omitempty"`
	Console    []string      `json:"console,omitempty"`
}
type Framework struct {
	Package addons.Package `json:"package"`
	Archive string         `json:"archive"`
}
type Response struct {
	Error          string                `json:"error,omitempty"`
	Managed        bool                  `json:"managed"`
	Generation     content.Generation    `json:"generation"`
	Frameworks     []Framework           `json:"frameworks,omitempty"`
	Password       *passwords.Assignment `json:"password,omitempty"`
	RestartSeconds int                   `json:"restart_seconds,omitempty"`
}

func SocketClient(socket string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}, Timeout: 30 * time.Minute}
}
func Call(ctx context.Context, client *http.Client, request Request) (Response, error) {
	return CallURL(ctx, client, "http://sourcenode/", request)
}
func CallURL(ctx context.Context, client *http.Client, endpoint string, request Request) (Response, error) {
	var out Response
	request.Version = ProtocolVersion
	b, e := json.Marshal(request)
	if e != nil {
		return out, e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(b))
	if e != nil {
		return out, e
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := client.Do(req)
	if e != nil {
		return out, e
	}
	defer resp.Body.Close()
	if e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); e != nil {
		return out, e
	}
	if resp.StatusCode != 200 || out.Error != "" {
		return out, errors.New("node request failed: " + out.Error)
	}
	return out, nil
}
