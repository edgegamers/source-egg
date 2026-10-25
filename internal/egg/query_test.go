package egg

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestQueryPlayersHandlesChallengeAndSubtractsBots(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	go func() {
		buf := make([]byte, 1400)
		_, addr, _ := conn.ReadFrom(buf)
		conn.WriteTo([]byte{255, 255, 255, 255, 0x41, 1, 2, 3, 4}, addr)
		n, addr, _ := conn.ReadFrom(buf)
		if n < 4 || string(buf[n-4:n]) != "\x01\x02\x03\x04" {
			return
		}
		data := []byte("\xff\xff\xff\xffI\x11name\x00map\x00cstrike\x00Counter-Strike Source\x00")
		data = append(data, 240, 0, 5, 32, 2)
		conn.WriteTo(data, addr)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	info, err := QueryInfo(ctx, conn.LocalAddr().String())
	if err != nil || info.Players != 3 || info.Name != "name" || info.Map != "map" {
		t.Fatal(info, err)
	}
}
