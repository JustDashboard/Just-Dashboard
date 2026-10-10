package netflows

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// This assembler has no request input. Its only relocations are the four maps
// created by this observer. The UAPI offsets and helper allowlist are documented
// in network-flow-observer.md; no instruction writes a packet or its context.
type bpfInstruction struct {
	Code, Registers uint8
	Offset          int16
	Immediate       int32
}

type fixedProgram struct {
	code   []bpfInstruction
	labels map[string]int
	jumps  map[int]string
}

func (p *fixedProgram) ins(code, dst, src uint8, off int16, imm int32) {
	p.code = append(p.code, bpfInstruction{code, dst | src<<4, off, imm})
}
func (p *fixedProgram) mark(name string) { p.labels[name] = len(p.code) }
func (p *fixedProgram) jump(code, dst, src uint8, imm int32, label string) {
	p.jumps[len(p.code)] = label
	p.ins(code, dst, src, 0, imm)
}
func (p *fixedProgram) mov(dst, src uint8)           { p.ins(0xbf, dst, src, 0, 0) }
func (p *fixedProgram) imm(dst uint8, n int32)       { p.ins(0xb7, dst, 0, 0, n) }
func (p *fixedProgram) alu(op, dst uint8, n int32)   { p.ins(op|7, dst, 0, 0, n) }
func (p *fixedProgram) call(id int32)                { p.ins(0x85, 0, 0, 0, id) }
func (p *fixedProgram) pointer(dst uint8, off int16) { p.mov(dst, 10); p.alu(0, dst, int32(off)) }
func (p *fixedProgram) mapFD(dst uint8, fd int) {
	p.ins(0x18, dst, 1, 0, int32(fd))
	p.ins(0, 0, 0, 0, 0)
}
func (p *fixedProgram) store(size uint8, off int16, src uint8) { p.ins(0x63|size, 10, src, off, 0) }
func (p *fixedProgram) load(size uint8, dst, src uint8, off int16) {
	p.ins(0x61|size, dst, src, off, 0)
}
func (p *fixedProgram) header(offsetReg uint8, offset int32, stack int16, size int32) {
	p.mov(1, 6)
	if offsetReg == 0 {
		p.imm(2, offset)
	} else {
		p.mov(2, offsetReg)
	}
	p.pointer(3, stack)
	p.imm(4, size)
	p.call(26) // skb_load_bytes, exact header bytes only.
	p.jump(0x55, 0, 0, 0, "header_gap")
}
func (p *fixedProgram) stat(mapfd int, index int32) {
	p.ins(0x62, 10, 0, -136, index)
	p.mapFD(1, mapfd)
	p.pointer(2, -136)
	p.call(1)
	p.jump(0x15, 0, 0, 0, "allow")
	p.imm(1, 1)
	p.ins(0xdb, 0, 1, 0, 0) // atomic add.
}

const (
	observerRecordBytes     = 120
	ObserverRingBytes       = 1 << 20
	ObserverEventsPerSecond = 5000
	ObserverMaxPendingRows  = 2048
)

// Event: monotonic ns/cookie/socket cgroup/netns cookies, skb length/GSO,
// direction/family/L4 offset/protocol, 40 IP-header bytes, 20 transport-header
// bytes, four zero padding bytes. IPv4's unused 20 and UDP's unused 12 are zero.
func observerProgram(ring, stats, budget, identities int, direction int32) ([]bpfInstruction, error) {
	p := fixedProgram{labels: map[string]int{}, jumps: map[int]string{}}
	p.mov(6, 1)
	for off := int16(-128); off < -8; off += 8 {
		p.ins(0x7a, 10, 0, off, 0)
	}
	p.header(0, 0, -144, 1)
	p.load(0x10, 7, 10, -144)
	p.alu(0x70, 7, 4)
	p.jump(0x15, 7, 0, 4, "ipv4")
	p.jump(0x15, 7, 0, 6, "ipv6")
	p.jump(0x05, 0, 0, 0, "header_gap")
	p.mark("ipv4")
	p.load(0x10, 7, 10, -144)
	p.alu(0x50, 7, 15)
	p.alu(0x60, 7, 2)
	p.jump(0xa5, 7, 0, 20, "header_gap")
	p.header(0, 0, -72, 20)
	// Reject every IPv4 fragment before reading any transport bytes.
	p.load(8, 1, 10, -66)
	p.ins(0xdc, 1, 0, 0, 16)
	p.alu(0x50, 1, 0x3fff)
	p.jump(0x55, 1, 0, 0, "header_gap")
	p.imm(1, 4)
	p.store(0, -84, 1)
	p.store(0, -80, 7)
	p.load(0x10, 8, 10, -63)
	p.jump(0x05, 0, 0, 0, "transport")
	p.mark("ipv6")
	p.header(0, 0, -72, 40)
	p.imm(7, 40)
	p.store(0, -80, 7)
	p.imm(1, 6)
	p.store(0, -84, 1)
	p.load(0x10, 8, 10, -66)
	p.mark("transport")
	p.store(0, -76, 8)
	p.jump(0x15, 8, 0, 6, "tcp")
	p.jump(0x15, 8, 0, 17, "udp")
	p.jump(0x05, 0, 0, 0, "header_gap")
	p.mark("tcp")
	p.header(7, 0, -32, 20)
	p.jump(0x05, 0, 0, 0, "identity")
	p.mark("udp")
	p.header(7, 0, -32, 8)
	p.mark("identity")
	p.mov(1, 6)
	p.call(46)
	p.store(0x18, -120, 0)
	p.jump(0x15, 0, 0, 0, "identity_gap")
	p.mov(1, 6)
	p.call(79)
	p.store(0x18, -112, 0)
	p.jump(0x15, 0, 0, 0, "identity_gap")
	// cgroup_skb cannot call get_netns_cookie. Socket-create stamps this map
	// using the supported cgroup_sock helper, before the first packet.
	p.mapFD(1, identities)
	p.pointer(2, -120)
	p.call(1)
	p.jump(0x15, 0, 0, 0, "identity_gap")
	p.load(0x18, 1, 0, 8)
	p.load(0x18, 2, 10, -112)
	p.jump(0x15, 1, 0, 0, "namespace_stamp")
	p.jump(0x5d, 1, 2, 0, "identity_gap")
	p.mark("namespace_stamp")
	p.load(0x18, 1, 0, 0)
	p.store(0x18, -104, 1)
	p.jump(0x15, 1, 0, 0, "identity_gap")
	p.call(5)
	p.store(0x18, -128, 0)
	p.mov(8, 0)
	p.alu(0x30, 8, 1000000000)
	p.alu(0x60, 8, 16)
	// One global CAS bucket caps emitted attempts to 5,000/s. Three unrolled
	// attempts bound contention cost; failed claims count as budget omissions.
	p.ins(0x62, 10, 0, -136, 0)
	p.mapFD(1, budget)
	p.pointer(2, -136)
	p.call(1)
	p.jump(0x15, 0, 0, 0, "budget_gap")
	p.mov(7, 0)
	for attempt := 0; attempt < 3; attempt++ {
		suffix := fmt.Sprint(attempt)
		p.load(0x18, 0, 7, 0)
		p.mov(1, 0)
		p.alu(0x50, 1, -65536)
		// A delayed packet cannot rewind the shared epoch and reopen its budget.
		p.jump(0x2d, 1, 8, 0, "budget_gap")
		p.jump(0xad, 1, 8, 0, "new_epoch"+suffix)
		p.mov(1, 0)
		p.alu(0x50, 1, 65535)
		p.jump(0x35, 1, 0, ObserverEventsPerSecond, "budget_gap")
		p.mov(2, 0)
		p.alu(0, 2, 1)
		p.jump(0x05, 0, 0, 0, "claim"+suffix)
		p.mark("new_epoch" + suffix)
		p.mov(2, 8)
		p.alu(0x40, 2, 1)
		p.mark("claim" + suffix)
		p.mov(9, 0)
		p.ins(0xdb, 7, 2, 0, 0xf1)
		p.jump(0x1d, 0, 9, 0, "emit")
	}
	p.jump(0x05, 0, 0, 0, "budget_gap")
	p.mark("emit")
	p.load(0, 1, 6, 0)
	p.store(0, -96, 1)
	p.load(0, 1, 6, 164)
	p.store(0, -92, 1)
	p.imm(1, direction)
	p.store(0, -88, 1)
	p.mapFD(1, ring)
	p.pointer(2, -128)
	p.imm(3, observerRecordBytes)
	p.imm(4, 0)
	p.call(130)
	p.jump(0x55, 0, 0, 0, "ring_drop")
	p.stat(stats, 0)
	p.jump(0x05, 0, 0, 0, "allow")
	p.mark("ring_drop")
	p.stat(stats, 1)
	p.jump(0x05, 0, 0, 0, "allow")
	p.mark("budget_gap")
	p.stat(stats, 2)
	p.jump(0x05, 0, 0, 0, "allow")
	p.mark("header_gap")
	p.stat(stats, 3)
	p.jump(0x05, 0, 0, 0, "allow")
	p.mark("identity_gap")
	p.stat(stats, 4)
	p.mark("allow")
	p.imm(0, 1)
	p.ins(0x95, 0, 0, 0, 0)
	return p.finish()
}
func (p *fixedProgram) finish() ([]bpfInstruction, error) {
	for index, label := range p.jumps {
		target, ok := p.labels[label]
		if !ok || target-index-1 < -32768 || target-index-1 > 32767 {
			return nil, fmt.Errorf("invalid fixed observer jump")
		}
		p.code[index].Offset = int16(target - index - 1)
	}
	return p.code, nil
}
func observerSocketProgram(identities, stats int, release bool) ([]bpfInstruction, error) {
	p := fixedProgram{labels: map[string]int{}, jumps: map[int]string{}}
	p.mov(6, 1)
	p.load(0, 1, 6, 4)
	p.jump(0x15, 1, 0, 2, "inet")
	p.jump(0x55, 1, 0, 10, "allow")
	p.mark("inet")
	p.load(0, 1, 6, 12)
	p.jump(0x15, 1, 0, 6, "protocol")
	p.jump(0x55, 1, 0, 17, "allow")
	p.mark("protocol")
	if release {
		// TCP release precedes some FIN/RST packets. Its bounded LRU stamp
		// remains until eviction; UDP can release its stamp immediately.
		p.load(0, 7, 6, 12)
		p.jump(0x15, 7, 0, 6, "allow")
	}
	p.mov(1, 6)
	p.call(46)
	p.store(0x18, -24, 0)
	p.jump(0x15, 0, 0, 0, "allow")
	if release {
		p.mapFD(1, identities)
		p.pointer(2, -24)
		p.call(3)
	} else {
		p.mov(1, 6)
		p.call(122)
		p.store(0x18, -16, 0)
		p.jump(0x15, 0, 0, 0, "state_gap")
		p.call(80)
		p.store(0x18, -8, 0)
		p.jump(0x15, 0, 0, 0, "state_gap")
		p.mapFD(1, identities)
		p.pointer(2, -24)
		p.pointer(3, -16)
		p.imm(4, 1)
		p.call(2)
		p.jump(0x15, 0, 0, 0, "allow")
		p.mark("state_gap")
		p.stat(stats, 5)
	}
	p.mark("allow")
	p.imm(0, 1)
	p.ins(0x95, 0, 0, 0, 0)
	return p.finish()
}
func observerProgramDigest() string {
	h := sha256.New()
	for _, direction := range []int32{0, 1} {
		code, _ := observerProgram(0, 0, 0, 0, direction)
		_ = binary.Write(h, binary.LittleEndian, code)
	}
	for _, release := range []bool{false, true} {
		code, _ := observerSocketProgram(0, 0, release)
		_ = binary.Write(h, binary.LittleEndian, code)
	}
	code, _ := observerEstablishedProgram(0, 0)
	_ = binary.Write(h, binary.LittleEndian, code)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// Accepted TCP child sockets do not invoke socket-create. Established callbacks
// stamp their socket namespace. The packet helper still supplies the actual
// socket cgroup; current-task cgroup is deliberately not used in this RX hook.
func observerEstablishedProgram(identities, stats int) ([]bpfInstruction, error) {
	p := fixedProgram{labels: map[string]int{}, jumps: map[int]string{}}
	p.mov(6, 1)
	p.load(0, 1, 6, 0)
	p.jump(0x15, 1, 0, 4, "established")
	p.jump(0x55, 1, 0, 5, "allow")
	p.mark("established")
	p.mov(1, 6)
	p.call(46)
	p.store(0x18, -24, 0)
	p.jump(0x15, 0, 0, 0, "allow")
	p.mapFD(1, identities)
	p.pointer(2, -24)
	p.call(1)
	p.jump(0x55, 0, 0, 0, "allow")
	p.mov(1, 6)
	p.call(122)
	p.store(0x18, -16, 0)
	p.ins(0x7a, 10, 0, -8, 0)
	p.jump(0x15, 0, 0, 0, "state_gap")
	p.mapFD(1, identities)
	p.pointer(2, -24)
	p.pointer(3, -16)
	p.imm(4, 1)
	p.call(2)
	p.jump(0x15, 0, 0, 0, "allow")
	p.jump(0x15, 0, 0, -17, "allow")
	p.mark("state_gap")
	p.stat(stats, 5)
	p.mark("allow")
	p.imm(0, 1)
	p.ins(0x95, 0, 0, 0, 0)
	return p.finish()
}
