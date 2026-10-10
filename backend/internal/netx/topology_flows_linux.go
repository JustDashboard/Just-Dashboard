package netx

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

var conntrackSeq atomic.Uint32

// dumpConntrack reads the connection-tracking table of the namespace the
// calling thread is in over ctnetlink: no subprocess, and nothing is written.
// It stops at limit entries and reports that it did.
func dumpConntrack(ctx context.Context, limit int) ([]TrackedFlow, bool, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_NETFILTER)
	if err != nil {
		if errors.Is(err, unix.EPROTONOSUPPORT) || errors.Is(err, unix.EAFNOSUPPORT) {
			return nil, false, &UnavailableError{Tool: "nf_conntrack"}
		}
		return nil, false, os.NewSyscallError("socket", err)
	}
	defer unix.Close(fd)
	deadline := time.Now().Add(3 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	timeout := unix.NsecToTimeval(time.Until(deadline).Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &timeout); err != nil {
		return nil, false, os.NewSyscallError("setsockopt", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, false, os.NewSyscallError("bind", err)
	}
	seq := conntrackSeq.Add(1)
	if err := unix.Sendto(fd, conntrackDumpRequest(seq), 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, false, os.NewSyscallError("sendto", err)
	}
	buf := make([]byte, 1<<16)
	var flows []TrackedFlow
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if time.Now().After(deadline) {
			return nil, false, errors.New("the connection-tracking dump did not finish in time")
		}
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if errors.Is(err, unix.EAGAIN) {
				return nil, false, errors.New("the connection-tracking dump did not finish in time")
			}
			return nil, false, os.NewSyscallError("recvfrom", err)
		}
		var done bool
		flows, done, err = parseConntrackMessages(buf[:n], seq, flows, limit)
		if errors.Is(err, errFlowLimit) {
			return flows, true, nil
		}
		if err != nil {
			return nil, false, err
		}
		if done {
			return flows, false, nil
		}
	}
}
