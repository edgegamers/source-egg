package egg

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"time"
)

// QueryPlayers is local A2S_INFO only; unknown/malformed replies remain unknown.
type ServerInfo struct {
	Name, Map string
	Players   int
}

func QueryPlayers(ctx context.Context, address string) (int, error) {
	info, err := QueryInfo(ctx, address)
	return info.Players, err
}
func QueryInfo(ctx context.Context, address string) (ServerInfo, error) {
	unknown := ServerInfo{Players: -1}
	conn, e := (&net.Dialer{}).DialContext(ctx, "udp", address)
	if e != nil {
		return unknown, e
	}
	defer conn.Close()
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
