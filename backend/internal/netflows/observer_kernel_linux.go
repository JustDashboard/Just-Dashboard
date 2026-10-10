package netflows

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

func bpfCall(command int, attr unsafe.Pointer, size uintptr) (int, error) {
	n, _, errno := unix.Syscall(unix.SYS_BPF, uintptr(command), uintptr(attr), size)
	runtime.KeepAlive(attr)
	if errno != 0 {
		return -1, errno
	}
	return int(n), nil
}
func bpfMap(kind, key, value, entries uint32, name string) (int, error) {
	attr := struct {
		Kind, Key, Value, Entries, Flags, Inner, NUMA uint32
		Name                                          [16]byte
		Ifindex, BTFFD, BTFKey, BTFValue, BTFVmlinux  uint32
		Extra                                         uint64
	}{Kind: kind, Key: key, Value: value, Entries: entries}
	copy(attr.Name[:15], name)
	fd, err := bpfCall(unix.BPF_MAP_CREATE, unsafe.Pointer(&attr), unsafe.Sizeof(attr))
	if err == nil {
		unix.CloseOnExec(fd)
	}
	return fd, err
}
func bpfLoad(code []bpfInstruction, kind, attach uint32) (int, error) {
	if len(code) == 0 {
		return -1, errors.New("empty fixed observer program")
	}
	license := []byte("GPL\x00")
	log := make([]byte, 65536)
	var pin runtime.Pinner
	pin.Pin(&code[0])
	pin.Pin(&license[0])
	pin.Pin(&log[0])
	defer pin.Unpin()
	attr := struct {
		Kind, Count       uint32
		Code, License     uint64
		LogLevel, LogSize uint32
		Log               uint64
		Version, Flags    uint32
		Name              [16]byte
		Ifindex, Attach   uint32
	}{Kind: kind, Count: uint32(len(code)), Code: uint64(uintptr(unsafe.Pointer(&code[0]))), License: uint64(uintptr(unsafe.Pointer(&license[0]))), LogLevel: 1, LogSize: uint32(len(log)), Log: uint64(uintptr(unsafe.Pointer(&log[0]))), Attach: attach}
	copy(attr.Name[:], "jd_flow_v1")
	fd, err := bpfCall(unix.BPF_PROG_LOAD, unsafe.Pointer(&attr), unsafe.Sizeof(attr))
	runtime.KeepAlive(code)
	runtime.KeepAlive(license)
	runtime.KeepAlive(log)
	if err != nil {
		return -1, fmt.Errorf("fixed observer verifier refused: %w: %s", err, clip(strings.TrimRight(string(log), "\x00"), 8192))
	}
	unix.CloseOnExec(fd)
	return fd, nil
}
func bpfInfo(fd int) ([]byte, error) {
	buf := make([]byte, 256)
	var pin runtime.Pinner
	pin.Pin(&buf[0])
	defer pin.Unpin()
	attr := struct {
		FD, Length uint32
		Info       uint64
	}{uint32(fd), uint32(len(buf)), uint64(uintptr(unsafe.Pointer(&buf[0])))}
	_, err := bpfCall(unix.BPF_OBJ_GET_INFO_BY_FD, unsafe.Pointer(&attr), unsafe.Sizeof(attr))
	runtime.KeepAlive(buf)
	return buf, err
}
func bpfAttach(program, target int, attach uint32) (int, error) {
	attr := struct{ Program, Target, Attach, Flags uint32 }{uint32(program), uint32(target), attach, 0}
	fd, err := bpfCall(unix.BPF_LINK_CREATE, unsafe.Pointer(&attr), unsafe.Sizeof(attr))
	if err == nil {
		unix.CloseOnExec(fd)
	}
	return fd, err
}
func bpfStats(fd int) ([6]uint64, error) {
	var out [6]uint64
	for i := range out {
		key := uint32(i)
		var value uint64
		var pin runtime.Pinner
		pin.Pin(&key)
		pin.Pin(&value)
		attr := struct {
			FD, Pad           uint32
			Key, Value, Flags uint64
		}{FD: uint32(fd), Key: uint64(uintptr(unsafe.Pointer(&key))), Value: uint64(uintptr(unsafe.Pointer(&value)))}
		_, err := bpfCall(unix.BPF_MAP_LOOKUP_ELEM, unsafe.Pointer(&attr), unsafe.Sizeof(attr))
		runtime.KeepAlive(&key)
		runtime.KeepAlive(&value)
		pin.Unpin()
		if err != nil {
			return out, err
		}
		out[i] = value
	}
	return out, nil
}
func cgroupHandle(file *os.File) (uint64, error) {
	h, _, err := unix.NameToHandleAt(int(file.Fd()), "", unix.AT_EMPTY_PATH)
	if err != nil || len(h.Bytes()) != 8 {
		return 0, fmt.Errorf("cgroup kernel handle is unavailable (type=%d size=%d): %v", h.Type(), len(h.Bytes()), err)
	}
	return binary.LittleEndian.Uint64(h.Bytes()), nil
}

type ownedObserverLink struct {
	fd                  int
	id, program, attach uint32
	cgroup              uint64
}

func (l *ownedObserverLink) verify() error {
	buf, err := bpfInfo(l.fd)
	if err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(buf[0:4]) != unix.BPF_LINK_TYPE_CGROUP || binary.LittleEndian.Uint32(buf[4:8]) != l.id || binary.LittleEndian.Uint32(buf[8:12]) != l.program || binary.LittleEndian.Uint64(buf[16:24]) != l.cgroup || binary.LittleEndian.Uint32(buf[24:28]) != l.attach {
		return errors.New("observer link identity changed")
	}
	return nil
}

type kernelSession struct {
	ring, stats, budget, identities int
	programs                        []int
	links                           []ownedObserverLink
	consumer, producer              []byte
	closed                          bool
	verifyLink                      func(*ownedObserverLink) error
	closeFD                         func(int) error
	unmap                           func([]byte) error
}

func openKernelSession(file *os.File, ringBytes int) (k *kernelSession, err error) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return nil, errors.New("fixed observer supports little-endian Linux amd64 and arm64 only")
	}
	if ringBytes < 4096 || ringBytes > ObserverRingBytes || ringBytes&(ringBytes-1) != 0 {
		return nil, errors.New("invalid fixed ring size")
	}
	var fs unix.Statfs_t
	if err = unix.Fstatfs(int(file.Fd()), &fs); err != nil || fs.Type != unix.CGROUP2_SUPER_MAGIC {
		return nil, errors.New("observer requires an open cgroup v2 directory")
	}
	cg, err := cgroupHandle(file)
	if err != nil {
		return nil, err
	}
	k = &kernelSession{ring: -1, stats: -1, budget: -1, identities: -1}
	owned := k
	defer func() {
		if err != nil {
			if cleanupErr := owned.close(); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("partial observer cleanup: %w", cleanupErr))
				// Failed owned cleanup stays reachable; another attachment must not
				// hide its resource budget or close a subsequently reused descriptor.
				k = owned
			}
		}
	}()
	if k.ring, err = bpfMap(unix.BPF_MAP_TYPE_RINGBUF, 0, 0, uint32(ringBytes), "jd_flow_ring"); err != nil {
		return nil, fmt.Errorf("ring-buffer capability refused: %w", err)
	}
	if k.stats, err = bpfMap(unix.BPF_MAP_TYPE_ARRAY, 4, 8, 6, "jd_flow_quality"); err != nil {
		return nil, err
	}
	if k.budget, err = bpfMap(unix.BPF_MAP_TYPE_ARRAY, 4, 8, 1, "jd_flow_budget"); err != nil {
		return nil, err
	}
	if k.identities, err = bpfMap(unix.BPF_MAP_TYPE_LRU_HASH, 8, 16, 4096, "jd_flow_identity"); err != nil {
		return nil, err
	}
	// Creation stamps precede packet attachments. Release comes last so a
	// partial setup can never leave an unbounded or unowned attachment.
	for _, attachment := range []struct{ kind, attach uint32 }{{unix.BPF_PROG_TYPE_CGROUP_SOCK, unix.BPF_CGROUP_INET_SOCK_CREATE}, {unix.BPF_PROG_TYPE_SOCK_OPS, unix.BPF_CGROUP_SOCK_OPS}, {unix.BPF_PROG_TYPE_CGROUP_SKB, unix.BPF_CGROUP_INET_INGRESS}, {unix.BPF_PROG_TYPE_CGROUP_SKB, unix.BPF_CGROUP_INET_EGRESS}, {unix.BPF_PROG_TYPE_CGROUP_SOCK, unix.BPF_CGROUP_INET_SOCK_RELEASE}} {
		direction := attachment.attach
		var code []bpfInstruction
		var e error
		if attachment.kind == unix.BPF_PROG_TYPE_CGROUP_SOCK {
			code, e = observerSocketProgram(k.identities, k.stats, direction == unix.BPF_CGROUP_INET_SOCK_RELEASE)
		} else if attachment.kind == unix.BPF_PROG_TYPE_SOCK_OPS {
			code, e = observerEstablishedProgram(k.identities, k.stats)
		} else {
			code, e = observerProgram(k.ring, k.stats, k.budget, k.identities, int32(direction))
		}
		if e != nil {
			return nil, e
		}
		prog, e := bpfLoad(code, attachment.kind, direction)
		if e != nil {
			return nil, e
		}
		k.programs = append(k.programs, prog)
		info, e := bpfInfo(prog)
		if e != nil {
			return nil, e
		}
		programID := binary.LittleEndian.Uint32(info[4:8])
		fd, e := bpfAttach(prog, int(file.Fd()), direction)
		if e != nil {
			return nil, fmt.Errorf("cgroup multi-link attach refused without replacing foreign programs: %w", e)
		}
		info, e = bpfInfo(fd)
		if e != nil {
			_ = unix.Close(fd)
			return nil, e
		}
		link := ownedObserverLink{fd: fd, id: binary.LittleEndian.Uint32(info[4:8]), program: programID, attach: direction, cgroup: cg}
		k.links = append(k.links, link)
		if e = link.verify(); e != nil {
			return nil, e
		}
	}
	page := os.Getpagesize()
	k.consumer, err = unix.Mmap(k.ring, 0, page, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return nil, err
	}
	k.producer, err = unix.Mmap(k.ring, int64(page), page+2*ringBytes, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return nil, err
	}
	return k, nil
}
func (k *kernelSession) close() error {
	if k == nil || k.closed {
		return nil
	}
	verify := k.verifyLink
	if verify == nil {
		verify = (*ownedObserverLink).verify
	}
	closeFD := k.closeFD
	if closeFD == nil {
		closeFD = unix.Close
	}
	unmap := k.unmap
	if unmap == nil {
		unmap = unix.Munmap
	}
	var failures []error
	for i := range k.links {
		link := &k.links[i]
		if link.fd < 0 {
			continue
		}
		if err := verify(link); err != nil {
			failures = append(failures, err)
			continue
		}
		fd := link.fd
		// Linux consumes close descriptors even on EINTR. Retrying the
		// integer after close could release somebody else's reused descriptor.
		link.fd = -1
		if err := closeFD(fd); err != nil {
			failures = append(failures, err)
		}
	}
	for _, link := range k.links {
		if link.fd >= 0 {
			return errors.Join(failures...)
		}
	}
	// Preserve the maps and reader until every owned attachment is released.
	if k.consumer != nil {
		if err := unmap(k.consumer); err != nil {
			failures = append(failures, err)
		} else {
			k.consumer = nil
		}
	}
	if k.producer != nil {
		if err := unmap(k.producer); err != nil {
			failures = append(failures, err)
		} else {
			k.producer = nil
		}
	}
	for i, fd := range k.programs {
		if fd >= 0 {
			k.programs[i] = -1
			failures = append(failures, closeFD(fd))
		}
	}
	for _, fd := range []*int{&k.ring, &k.stats, &k.budget, &k.identities} {
		if *fd >= 0 {
			saved := *fd
			*fd = -1
			failures = append(failures, closeFD(saved))
		}
	}
	k.closed = k.consumer == nil && k.producer == nil
	return errors.Join(failures...)
}

// read copies one fixed header record. The kernel's doubled data mapping makes
// wrapping contiguous. Only this goroutine owns the consumer pointer.
func (k *kernelSession) read(dst []byte) (bool, error) {
	if len(dst) != observerRecordBytes || len(k.consumer) == 0 || len(k.producer) == 0 {
		return false, errors.New("invalid observer ring reader")
	}
	cons := atomic.LoadUint64((*uint64)(unsafe.Pointer(&k.consumer[0])))
	prod := atomic.LoadUint64((*uint64)(unsafe.Pointer(&k.producer[0])))
	ringBytes := (len(k.producer) - os.Getpagesize()) / 2
	if prod < cons || prod-cons > uint64(ringBytes) {
		return false, errors.New("observer ring bounds changed")
	}
	if prod == cons {
		return false, nil
	}
	offset := int(cons&uint64(ringBytes-1)) + os.Getpagesize()
	header := atomic.LoadUint32((*uint32)(unsafe.Pointer(&k.producer[offset])))
	if header&(1<<31) != 0 {
		return false, nil
	}
	size := header & ((1 << 30) - 1)
	advance := (uint64(size) + 8 + 7) &^ 7
	if advance > uint64(ringBytes) || advance > prod-cons {
		return false, errors.New("observer record bounds changed")
	}
	if header&(1<<30) != 0 {
		atomic.StoreUint64((*uint64)(unsafe.Pointer(&k.consumer[0])), cons+advance)
		return false, nil
	}
	if size != observerRecordBytes {
		return false, errors.New("observer record does not match the fixed provenance")
	}
	copy(dst, k.producer[offset+8:offset+8+int(size)])
	atomic.StoreUint64((*uint64)(unsafe.Pointer(&k.consumer[0])), cons+advance)
	return true, nil
}
func monotonicNow() (time.Time, uint64, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		return time.Time{}, 0, err
	}
	return time.Now().UTC(), uint64(ts.Nano()), nil
}
