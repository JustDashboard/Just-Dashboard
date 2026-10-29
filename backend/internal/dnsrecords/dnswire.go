package dnsrecords

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"strings"
	"time"
)

// The DNS message as RFC 1035 lays it out, the UPDATE opcode of RFC 2136
// and the TSIG signature of RFC 8945: as much of each as writing one record
// set to an authoritative server needs, so nothing is added to go.mod for
// it. Names are written uncompressed; answers are read with compression.

const (
	dnsTypeA     uint16 = 1
	dnsTypeCNAME uint16 = 5
	dnsTypeSOA   uint16 = 6
	dnsTypeAAAA  uint16 = 28
	dnsTypeTSIG  uint16 = 250

	dnsClassIN   uint16 = 1
	dnsClassNONE uint16 = 254
	dnsClassANY  uint16 = 255

	dnsOpQuery  = 0
	dnsOpUpdate = 5
)

var dnsTypeNumbers = map[Type]uint16{TypeA: dnsTypeA, TypeAAAA: dnsTypeAAAA, TypeCNAME: dnsTypeCNAME}

// dnsRcodes are the answers' codes in words, RFC 2136's among them.
var dnsRcodes = map[int]string{
	1: "FORMERR (the server could not read the request)", 2: "SERVFAIL (the server failed)",
	3: "NXDOMAIN (no such name)", 4: "NOTIMP (the server does not take updates)",
	5: "REFUSED (the server refused: the key may not update this name or type)",
	6: "YXDOMAIN (a name that should not exist does)", 7: "YXRRSET (the record set appeared since it was read)",
	8: "NXRRSET (the record set disappeared since it was read)", 9: "NOTAUTH (the server does not accept this key for the zone, or is not authoritative for it)",
	10: "NOTZONE (the name is outside the zone)",
	16: "BADSIG (the key's secret does not match the server's)", 17: "BADKEY (the server knows no key by this name and algorithm)",
	18: "BADTIME (this server's clock and the DNS server's differ by more than five minutes)",
}

func rcodeWords(code int) string {
	if words, ok := dnsRcodes[code]; ok {
		return words
	}
	return fmt.Sprintf("response code %d", code)
}

// appendName writes a name as labels, uncompressed. The root is "".
func appendName(out []byte, name string) ([]byte, error) {
	name = strings.TrimSuffix(name, ".")
	if name != "" {
		for _, label := range strings.Split(name, ".") {
			if label == "" || len(label) > 63 {
				return nil, fmt.Errorf("%q is not a domain name", name)
			}
			out = append(out, byte(len(label)))
			out = append(out, label...)
		}
	}
	return append(out, 0), nil
}

func appendUint16(out []byte, value uint16) []byte { return binary.BigEndian.AppendUint16(out, value) }
func appendUint32(out []byte, value uint32) []byte { return binary.BigEndian.AppendUint32(out, value) }

// appendRR writes one resource record.
func appendRR(out []byte, name string, kind, class uint16, ttl uint32, data []byte) ([]byte, error) {
	out, err := appendName(out, name)
	if err != nil {
		return nil, err
	}
	out = appendUint16(out, kind)
	out = appendUint16(out, class)
	out = appendUint32(out, ttl)
	out = appendUint16(out, uint16(len(data)))
	return append(out, data...), nil
}

// dnsHeader is the twelve bytes every message starts with.
func dnsHeader(id uint16, opcode int, recursion bool, counts [4]uint16) []byte {
	flags := uint16(opcode&0xf) << 11
	if recursion {
		flags |= 1 << 8
	}
	out := appendUint16(nil, id)
	out = appendUint16(out, flags)
	for _, count := range counts {
		out = appendUint16(out, count)
	}
	return out
}

// dnsRR is one record read from an answer.
type dnsRR struct {
	Name  string
	Type  uint16
	Class uint16
	TTL   uint32
	Data  []byte
	// dataAt is where Data starts in the message, for a name inside it
	// that points back with compression; start is where the record does.
	dataAt, start int
}

// dnsReply is an answer read whole.
type dnsReply struct {
	ID                             uint16
	Authoritative                  bool
	Rcode                          int
	Answers, Authority, Additional []dnsRR
	// raw is the message, which a name inside a record may point into.
	raw []byte
}

// readName reads a name at off, following compression pointers, and
// answers it with the offset just past where it was written.
func readName(message []byte, off int) (string, int, error) {
	labels := []string{}
	next := -1
	for jumps := 0; ; {
		if off >= len(message) {
			return "", 0, errors.New("a name runs past the end of the message")
		}
		length := int(message[off])
		switch {
		case length == 0:
			if next < 0 {
				next = off + 1
			}
			return strings.Join(labels, "."), next, nil
		case length&0xc0 == 0xc0:
			if off+1 >= len(message) {
				return "", 0, errors.New("a compression pointer runs past the end of the message")
			}
			if next < 0 {
				next = off + 2
			}
			jumps++
			if jumps > 64 {
				return "", 0, errors.New("a name's compression pointers loop")
			}
			off = int(binary.BigEndian.Uint16(message[off:]) & 0x3fff)
		case length > 63:
			return "", 0, errors.New("a label is longer than 63 bytes")
		default:
			if off+1+length > len(message) {
				return "", 0, errors.New("a label runs past the end of the message")
			}
			labels = append(labels, string(message[off+1:off+1+length]))
			off += 1 + length
		}
	}
}

// parseReply reads an answer's header and every record in it.
func parseReply(message []byte) (dnsReply, error) {
	if len(message) < 12 {
		return dnsReply{}, errors.New("the answer is shorter than a DNS header")
	}
	reply := dnsReply{ID: binary.BigEndian.Uint16(message), raw: message}
	flags := binary.BigEndian.Uint16(message[2:])
	reply.Authoritative = flags&(1<<10) != 0
	reply.Rcode = int(flags & 0xf)
	counts := [4]int{}
	for index := range counts {
		counts[index] = int(binary.BigEndian.Uint16(message[4+2*index:]))
	}
	off := 12
	for index := 0; index < counts[0]; index++ {
		_, next, err := readName(message, off)
		if err != nil {
			return dnsReply{}, err
		}
		off = next + 4
	}
	sections := []*[]dnsRR{&reply.Answers, &reply.Authority, &reply.Additional}
	for section, records := range sections {
		for index := 0; index < counts[section+1]; index++ {
			start := off
			name, next, err := readName(message, off)
			if err != nil {
				return dnsReply{}, err
			}
			if next+10 > len(message) {
				return dnsReply{}, errors.New("a record runs past the end of the message")
			}
			record := dnsRR{
				Name: strings.ToLower(name), Type: binary.BigEndian.Uint16(message[next:]), Class: binary.BigEndian.Uint16(message[next+2:]),
				TTL: binary.BigEndian.Uint32(message[next+4:]), start: start,
			}
			length := int(binary.BigEndian.Uint16(message[next+8:]))
			record.dataAt = next + 10
			if record.dataAt+length > len(message) {
				return dnsReply{}, errors.New("a record's data runs past the end of the message")
			}
			record.Data = message[record.dataAt : record.dataAt+length]
			off = record.dataAt + length
			*records = append(*records, record)
		}
	}
	return reply, nil
}

// tsigKey is a TSIG key: its name, algorithm and secret.
type tsigKey struct {
	name      string
	algorithm string
	secret    []byte
	hash      func() hash.Hash
}

// tsigAlgorithms are the HMACs RFC 8945 names, by the names certbot's
// plugin and BIND's configuration give them.
var tsigAlgorithms = map[string]struct {
	wire string
	hash func() hash.Hash
}{
	"HMAC-MD5":    {"hmac-md5.sig-alg.reg.int", md5.New},
	"HMAC-SHA1":   {"hmac-sha1", sha1.New},
	"HMAC-SHA224": {"hmac-sha224", sha256.New224},
	"HMAC-SHA256": {"hmac-sha256", sha256.New},
	"HMAC-SHA384": {"hmac-sha384", sha512.New384},
	"HMAC-SHA512": {"hmac-sha512", sha512.New},
}

// tsigFudge is how far apart the two clocks may be: RFC 8945's five minutes.
const tsigFudge = 300

// variables is the TSIG half of what the MAC is computed over, after the
// message: the key's name, class ANY, TTL 0, the algorithm, the time, the
// fudge, the error and the other data, names in canonical form.
func (k tsigKey) variables(signed uint64, fudge uint16, code uint16, other []byte) ([]byte, error) {
	out, err := appendName(nil, strings.ToLower(k.name))
	if err != nil {
		return nil, err
	}
	out = appendUint16(out, dnsClassANY)
	out = appendUint32(out, 0)
	if out, err = appendName(out, k.algorithm); err != nil {
		return nil, err
	}
	out = append(out, byte(signed>>40), byte(signed>>32), byte(signed>>24), byte(signed>>16), byte(signed>>8), byte(signed))
	out = appendUint16(out, fudge)
	out = appendUint16(out, code)
	out = appendUint16(out, uint16(len(other)))
	return append(out, other...), nil
}

func (k tsigKey) mac(parts ...[]byte) []byte {
	mac := hmac.New(k.hash, k.secret)
	for _, part := range parts {
		mac.Write(part)
	}
	return mac.Sum(nil)
}

// sign appends a TSIG record to a whole message and counts it in the
// header, answering the signed message and its MAC, which the answer's own
// signature covers.
func (k tsigKey) sign(message []byte, now time.Time) ([]byte, []byte, error) {
	signed := uint64(now.Unix())
	variables, err := k.variables(signed, tsigFudge, 0, nil)
	if err != nil {
		return nil, nil, err
	}
	mac := k.mac(message, variables)
	data, err := appendName(nil, k.algorithm)
	if err != nil {
		return nil, nil, err
	}
	data = append(data, byte(signed>>40), byte(signed>>32), byte(signed>>24), byte(signed>>16), byte(signed>>8), byte(signed))
	data = appendUint16(data, tsigFudge)
	data = appendUint16(data, uint16(len(mac)))
	data = append(data, mac...)
	data = appendUint16(data, binary.BigEndian.Uint16(message))
	data = appendUint16(data, 0)
	data = appendUint16(data, 0)
	out := append([]byte(nil), message...)
	binary.BigEndian.PutUint16(out[10:], binary.BigEndian.Uint16(out[10:])+1)
	out, err = appendRR(out, strings.ToLower(k.name), dnsTypeTSIG, dnsClassANY, 0, data)
	return out, mac, err
}

// tsigFields are a TSIG record's data, read.
type tsigFields struct {
	algorithm string
	signed    uint64
	fudge     uint16
	mac       []byte
	original  uint16
	code      uint16
	other     []byte
}

// readTSIG reads the data of a TSIG record of message.
func readTSIG(message []byte, record dnsRR) (tsigFields, error) {
	algorithm, off, err := readName(message, record.dataAt)
	if err != nil {
		return tsigFields{}, err
	}
	data := message[off : record.dataAt+len(record.Data)]
	truncated := errors.New("the signature is truncated")
	if len(data) < 10 {
		return tsigFields{}, truncated
	}
	fields := tsigFields{
		algorithm: algorithm,
		signed:    uint64(data[0])<<40 | uint64(data[1])<<32 | uint64(data[2])<<24 | uint64(data[3])<<16 | uint64(data[4])<<8 | uint64(data[5]),
		fudge:     binary.BigEndian.Uint16(data[6:]),
	}
	size := int(binary.BigEndian.Uint16(data[8:]))
	if len(data) < 10+size+6 {
		return tsigFields{}, truncated
	}
	fields.mac = data[10 : 10+size]
	fields.original = binary.BigEndian.Uint16(data[10+size:])
	fields.code = binary.BigEndian.Uint16(data[12+size:])
	otherLength := int(binary.BigEndian.Uint16(data[14+size:]))
	if len(data) < 16+size+otherLength {
		return tsigFields{}, truncated
	}
	fields.other = data[16+size : 16+size+otherLength]
	return fields, nil
}

// unsigned is a message as it was before its TSIG record was added: the
// record cut off, the count of additional records one less, and the id the
// signer gave it.
func unsigned(message []byte, record dnsRR, original uint16) []byte {
	out := append([]byte(nil), message[:record.start]...)
	binary.BigEndian.PutUint16(out, original)
	binary.BigEndian.PutUint16(out[10:], binary.BigEndian.Uint16(out[10:])-1)
	return out
}

// verify checks an answer's TSIG against the request's MAC: the answer
// must be signed with this key, within the fudge of now, and its MAC must
// be the one computed over the request's MAC, the answer as it was before
// the signature was added, and the signature's variables.
func (k tsigKey) verify(message []byte, reply dnsReply, requestMAC []byte, now time.Time) error {
	if len(reply.Additional) == 0 || reply.Additional[len(reply.Additional)-1].Type != dnsTypeTSIG {
		return errors.New("the answer is not signed, so it cannot be told from a forged one")
	}
	record := reply.Additional[len(reply.Additional)-1]
	if !strings.EqualFold(strings.TrimSuffix(record.Name, "."), strings.TrimSuffix(k.name, ".")) {
		return fmt.Errorf("the answer is signed with the key %s, not %s", record.Name, k.name)
	}
	fields, err := readTSIG(message, record)
	if err != nil {
		return err
	}
	if fields.code != 0 {
		return fmt.Errorf("the server refused the signature: %s", rcodeWords(int(fields.code)))
	}
	if !strings.EqualFold(strings.TrimSuffix(fields.algorithm, "."), strings.TrimSuffix(k.algorithm, ".")) {
		return fmt.Errorf("the answer is signed with %s, not %s", fields.algorithm, k.algorithm)
	}
	variables, err := k.variables(fields.signed, fields.fudge, fields.code, fields.other)
	if err != nil {
		return err
	}
	prefix := appendUint16(nil, uint16(len(requestMAC)))
	if !hmac.Equal(fields.mac, k.mac(prefix, requestMAC, unsigned(message, record, fields.original), variables)) {
		return errors.New("the answer's signature does not verify: it was not signed with this key")
	}
	if skew := now.Unix() - int64(fields.signed); skew > int64(fields.fudge) || -skew > int64(fields.fudge) {
		return fmt.Errorf("the answer was signed %d seconds from this server's clock, outside the %d allowed", skew, fields.fudge)
	}
	return nil
}
