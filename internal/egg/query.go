package egg

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"time"
)

// QueryPlayers is local A2S_INFO only; unknown/malformed replies remain unknown.
func QueryPlayers(ctx context.Context, address string) (int, error) {
	conn, e := (&net.Dialer{}).DialContext(ctx, "udp", address)
	if e != nil {
		return -1, e
	}
	defer conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	if t, ok := ctx.Deadline(); ok {
		deadline = t
	}
	conn.SetDeadline(deadline)
	request := []byte("\xff\xff\xff\xffTSource Engine Query\x00")
	if _, e = conn.Write(request); e != nil {
		return -1, e
	}
	buf := make([]byte, 65536)
	for attempt := 0; attempt < 2; attempt++ {
		n, e := conn.Read(buf)
		if e != nil {
			return -1, e
		}
		data := buf[:n]
		if n < 6 || binary.LittleEndian.Uint32(data[:4]) != 0xffffffff {
			return -1, errors.New("invalid A2S response")
		}
		if data[4] == 0x41 && n == 9 {
			if _, e = conn.Write(append(request, data[5:9]...)); e != nil {
				return -1, e
			}
			continue
		}
		if data[4] != 0x49 {
			return -1, errors.New("unsupported A2S response")
		}
		i := 6
		for range 4 {
			for i < n && data[i] != 0 {
				i++
			}
			if i >= n {
				return -1, errors.New("truncated A2S response")
			}
			i++
		}
		if i+5 > n {
			return -1, errors.New("missing A2S player counts")
		}
		players, bots := int(data[i+2]), int(data[i+4])
		if bots > players || players > 128 {
			return -1, errors.New("invalid A2S player counts")
		}
		return players - bots, nil
	}
	return -1, errors.New("A2S challenge not resolved")
}
