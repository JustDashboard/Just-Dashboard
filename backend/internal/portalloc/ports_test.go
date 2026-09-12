package portalloc

import (
	"errors"
	"net"
	"syscall"
	"testing"
)

func TestSelectWrapsAndReservesGroupPorts(t *testing.T) {
	got, err := Select(65535, 1024, map[int]bool{1024: true}, func(port int) error {
		if port == 65535 {
			return syscall.EADDRINUSE
		}
		return nil
	})
	if err != nil || got != 1025 {
		t.Fatalf("selected %d: %v", got, err)
	}
	got, err = Select(5000, 1024, nil, func(int) error { return nil })
	if err != nil || got != 5000 {
		t.Fatalf("preferred %d: %v", got, err)
	}
}

func TestAvailableRespectsProtocolAndBindScope(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := Available("127.0.0.1", "tcp", port); !IsConflict(err) {
		t.Fatalf("missed TCP owner: %v", err)
	}
	if err := Available("127.0.0.1", "udp", port); err != nil {
		t.Fatalf("TCP should not occupy UDP: %v", err)
	}
	if err := Available("127.0.0.1", "sctp", port); err == nil {
		t.Fatal("unsupported protocol accepted")
	}
	if IsConflict(errors.New("permission denied")) {
		t.Fatal("unrelated failure treated as conflict")
	}
}
