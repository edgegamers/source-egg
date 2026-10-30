package egg

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/edgegamers/source-egg/internal/supervisor"
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

func TestWatchMarksReadyWhenGameIgnoresLoopbackQueries(t *testing.T) {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	hasIPv4 := false
	for _, address := range addresses {
		if ip, ok := address.(*net.IPNet); ok && ip.IP.To4() != nil && !ip.IP.IsLoopback() {
			hasIPv4 = true
		}
	}
	if !hasIPv4 {
		t.Skip("requires a non-loopback IPv4 interface")
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	serving := make(chan struct{})
	go func() {
		defer close(serving)
		buf := make([]byte, 1400)
		request := "\xff\xff\xff\xffTSource Engine Query\x00"
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			// Reproduce the live TF2 server: its game socket is bound to all
			// interfaces but only non-loopback A2S queries receive replies.
			if addr.IP.IsLoopback() {
				continue
			}
			switch string(buf[:n]) {
			case request:
				_, _ = conn.WriteToUDP([]byte{255, 255, 255, 255, 0x41, 1, 2, 3, 4}, addr)
			case request + "\x01\x02\x03\x04":
				data := []byte("\xff\xff\xff\xffI\x11EdgeGamers\x00cp_dustbowl\x00tf\x00Team Fortress\x00")
				data = append(data, 184, 1, 0, 32, 0, 'd', 'l', 0, 1)
				data = append(data, []byte("11185272\x00\x00")...)
				_, _ = conn.WriteToUDP(data, addr)
			}
		}
	}()
	defer func() { conn.Close(); <-serving }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ready := make(chan struct{}, 1)
	watchDone := make(chan struct{})
	e := Egg{Config: Config{Port: conn.LocalAddr().(*net.UDPAddr).Port}}
	go func() {
		defer close(watchDone)
		e.watch(ctx, &supervisor.Server{}, Prepared{}, func(line string) {
			if line == panelReadyLine {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
		})
	}()
	defer func() { cancel(); <-watchDone }()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatalf("no readiness marker: TF2 answers A2S on its interface address, but not on localhost (port %d)", e.Config.Port)
	}
}

func TestQueryLocalInfoSupportsLoopbackOnlyServer(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	serving := make(chan struct{})
	go func() {
		defer close(serving)
		buf := make([]byte, 1400)
		_, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		data := []byte("\xff\xff\xff\xffI\x11local server\x00cp_dustbowl\x00tf\x00Team Fortress\x00")
		data = append(data, 184, 1, 5, 32, 2, 'd', 'l', 0, 1)
		data = append(data, []byte("11185272\x00\x00")...)
		_, _ = conn.WriteToUDP(data, addr)
	}()
	defer func() { conn.Close(); <-serving }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	info, err := QueryLocalInfo(ctx, conn.LocalAddr().(*net.UDPAddr).Port)
	if err != nil || info.Name != "local server" || info.Map != "cp_dustbowl" || info.Players != 3 {
		t.Fatal(info, err)
	}
}

func TestQueryLocalInfoKeepsUnansweredQueriesUnknown(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	info, err := QueryLocalInfo(ctx, conn.LocalAddr().(*net.UDPAddr).Port)
	if err == nil || info.Players != -1 || info.Name != "" || info.Map != "" {
		t.Fatalf("unanswered query must remain unknown: %+v, %v", info, err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("query exceeded the caller's deadline: %v", elapsed)
	}
}

func TestQueryInfoCancelsPendingRead(t *testing.T) {
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := QueryInfo(ctx, conn.LocalAddr().String())
		done <- err
	}()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadFrom(make([]byte, 1400)); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled query succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled query left its UDP read running")
	}
}
