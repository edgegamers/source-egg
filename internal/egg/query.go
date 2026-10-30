package egg

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// ServerInfo contains A2S_INFO metadata; unknown/malformed replies remain unknown.
type ServerInfo struct {
	Name, Map string
	Players   int
}

func QueryPlayers(ctx context.Context, address string) (int, error) {
	info, err := QueryInfo(ctx, address)
	return info.Players, err
}

// QueryLocalInfo probes only this network namespace's IPv4 addresses. Some
// Source servers ignore loopback queries even when listening on 0.0.0.0.
func QueryLocalInfo(ctx context.Context, port int) (ServerInfo, error) {
	addresses := []string{fmt.Sprintf("127.0.0.1:%d", port)}
	seen := map[string]bool{addresses[0]: true}
	var errs []error
	interfaces, err := net.InterfaceAddrs()
	if err != nil {
		errs = append(errs, fmt.Errorf("list local query addresses: %w", err))
	}
	for _, address := range interfaces {
		ipnet, ok := address.(*net.IPNet)
		if !ok || ipnet.IP.To4() == nil || ipnet.IP.IsLoopback() {
			continue
		}
		address := (&net.UDPAddr{IP: ipnet.IP.To4(), Port: port}).String()
		if !seen[address] {
			seen[address] = true
			addresses = append(addresses, address)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	type result struct {
		info ServerInfo
		err  error
	}
	results := make(chan result, len(addresses))
	for _, address := range addresses {
		workers.Go(func() {
			info, err := QueryInfo(ctx, address)
			results <- result{info, err}
		})
	}
	for range addresses {
		select {
		case <-ctx.Done():
			return ServerInfo{Players: -1}, ctx.Err()
		case r := <-results:
			if r.err == nil {
				return r.info, nil
			}
			errs = append(errs, r.err)
		}
	}
	return ServerInfo{Players: -1}, errors.Join(errs...)
}

func QueryInfo(ctx context.Context, address string) (ServerInfo, error) {
	unknown := ServerInfo{Players: -1}
	conn, e := (&net.Dialer{}).DialContext(ctx, "udp", address)
	if e != nil {
		return unknown, e
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline := time.Now().Add(2 * time.Second)
	if t, ok := ctx.Deadline(); ok {
		deadline = t
	}
	conn.SetDeadline(deadline)
	request := []byte("\xff\xff\xff\xffTSource Engine Query\x00")
	if _, e = conn.Write(request); e != nil {
		return unknown, e
	}
	buf := make([]byte, 65536)
	for attempt := 0; attempt < 2; attempt++ {
		n, e := conn.Read(buf)
		if e != nil {
			return unknown, e
		}
		data := buf[:n]
		if n < 6 || binary.LittleEndian.Uint32(data[:4]) != 0xffffffff {
			return unknown, errors.New("invalid A2S response")
		}
		if data[4] == 0x41 && n == 9 {
			if _, e = conn.Write(append(request, data[5:9]...)); e != nil {
				return unknown, e
			}
			continue
		}
		if data[4] != 0x49 {
			return unknown, errors.New("unsupported A2S response")
		}
		i := 6
		values := []string{}
		for range 4 {
			start := i
			for i < n && data[i] != 0 {
				i++
			}
			if i >= n {
				return unknown, errors.New("truncated A2S response")
			}
			values = append(values, string(data[start:i]))
			i++
		}
		if i+5 > n {
			return unknown, errors.New("missing A2S player counts")
		}
		players, bots := int(data[i+2]), int(data[i+4])
		if bots > players || players > 128 {
			return unknown, errors.New("invalid A2S player counts")
		}
		if len(values[0]) > 128 || len(values[1]) > 128 {
			return unknown, errors.New("oversized A2S metadata")
		}
		return ServerInfo{Name: values[0], Map: values[1], Players: players - bots}, nil
	}
	return unknown, errors.New("A2S challenge not resolved")
}
