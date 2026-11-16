package orderedroute

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/url"
	"ride-home-router/internal/database"
	"ride-home-router/internal/postgres/postgrestest"
	"sync"
	"sync/atomic"
	"testing"
)

func dropCommitAcknowledgements(t *testing.T, parsed *url.URL, limit int32) (string, *atomic.Int32) {
	t.Helper()
	upstreamAddress := parsed.Host
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var claimed atomic.Int32
	var dropped atomic.Int32
	var workers sync.WaitGroup
	var mu sync.Mutex
	connections := make(map[net.Conn]bool)
	workers.Go(func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[client] = true
			mu.Unlock()
			workers.Go(func() {
				defer func() { _ = client.Close(); mu.Lock(); delete(connections, client); mu.Unlock() }()
				server, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", upstreamAddress)
				if err != nil {
					t.Error(err)
					return
				}
				defer func() { _ = server.Close() }()
				var startup [4]byte
				if _, err := io.ReadFull(client, startup[:]); err != nil {
					return
				}
				payload := make([]byte, int(binary.BigEndian.Uint32(startup[:]))-4)
				if _, err := io.ReadFull(client, payload); err != nil {
					return
				}
				if _, err := server.Write(append(startup[:], payload...)); err != nil {
					return
				}
				var armed atomic.Bool
				finished := make(chan struct{})
				go func() {
					defer close(finished)
					defer func() { _ = client.Close(); _ = server.Close() }()
					for {
						packet, err := readPostgresPacket(client)
						if err != nil {
							return
						}
						if packet[0] == 'Q' && bytes.Equal(packet[5:], []byte("commit\x00")) && claimed.Add(1) <= limit {
							armed.Store(true)
						}
						if _, err := server.Write(packet); err != nil {
							return
						}
					}
				}()
				for {
					packet, err := readPostgresPacket(server)
					if err != nil {
						break
					}
					if armed.Load() && packet[0] == 'C' && bytes.Equal(packet[5:], []byte("COMMIT\x00")) {
						dropped.Add(1)
						break
					}
					if _, err := client.Write(packet); err != nil {
						break
					}
				}
				_ = client.Close()
				_ = server.Close()
				<-finished
			})
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		for conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	parsed.Host = listener.Addr().String()
	return parsed.String(), &dropped
}

func readPostgresPacket(conn net.Conn) ([]byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	packet := make([]byte, int(binary.BigEndian.Uint32(header[1:]))+1)
	copy(packet, header)
	_, err := io.ReadFull(conn, packet[5:])
	return packet, err
}

type lostAckOutcome struct {
	Successful uint
	Reserved   int
}

func runLostAckOutcome(t *testing.T, batch bool, firstCost int, faults ...int32) lostAckOutcome {
	t.Helper()
	databaseURL := postgrestest.DatabaseURL(t)
	ledger := postgrestest.OpenURL(t, databaseURL).GoogleUsage()
	if err := ledger.SetCeiling(t.Context(), database.UsageSKURoutes, 100); err != nil {
		t.Fatal(err)
	}
	limit := int32(1)
	if len(faults) > 0 {
		limit = faults[0]
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	proxyURL, dropped := dropCommitAcknowledgements(t, parsed, limit)
	proxyLedger := postgrestest.OpenURL(t, proxyURL).GoogleUsage()
	firstDone := make(chan struct{})
	server, seen, mu := fakeRoutes(t, 500, 60)
	var attempts atomic.Int32
	client := newClient(staticKey("synthetic"), func(ctx context.Context, units int) error {
		if units != 5 && units != firstCost && !batch {
			<-firstDone
		}
		attempts.Add(1)
		err := proxyLedger.Reserve(ctx, database.UsageSKURoutes, units)
		if units == firstCost {
			close(firstDone)
		}
		return err
	}, server.Client(), server.URL)
	measure := client.measureBeforeBatching
	if batch {
		measure = client.Measure
	}
	var outcome lostAckOutcome
	failed := 0
	for i, result := range measure(t.Context(), reservationRequests(0, 2, 3)) {
		if result.Err == nil {
			outcome.Successful |= 1 << i
		} else {
			failed++
			t.Logf("%s error=%s", result.ID, result.Err)
		}
	}
	used, err := ledger.Reserved(t.Context(), database.UsageSKURoutes)
	if err != nil {
		t.Fatal(err)
	}
	outcome.Reserved = used
	mu.Lock()
	calls := len(*seen)
	mu.Unlock()
	t.Logf("batch=%v firstCost=%d successful_result_mask=%04b failed_results=%d reserved=%d reserve_transactions=%d lost_commit_acks=%d provider_requests=%d", batch, firstCost, outcome.Successful, failed, used, attempts.Load(), dropped.Load(), calls)
	if dropped.Load() != limit || used != 5 || used > 100 {
		t.Fatal("fault or commit not reproduced")
	}
	return outcome
}

func TestReservationLostCommitAcknowledgement(t *testing.T) {
	for _, batch := range []bool{false, true} {
		for _, firstCost := range []int{2, 3} {
			t.Run(fmt.Sprintf("batch=%v/first=%d", batch, firstCost), func(t *testing.T) {
				outcome := runLostAckOutcome(t, batch, firstCost)
				want := uint(0)
				if !batch {
					want = 12
					if firstCost == 3 {
						want = 3
					}
				}
				if outcome.Successful != want {
					t.Fatalf("outcome=%v want successful mask=%04b", outcome, want)
				}
			})
		}
	}
}

func TestReservationUnknownCommitChargeBound(t *testing.T) {
	main := runLostAckOutcome(t, false, 2, 2)
	candidate := runLostAckOutcome(t, true, 2)
	if candidate.Successful != 0 || main.Successful != 0 || candidate.Reserved > main.Reserved {
		t.Fatalf("candidate=%v exceeds main with every car faulted=%v", candidate, main)
	}
}
