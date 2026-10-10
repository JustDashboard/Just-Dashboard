package netflows

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"
	"time"
)

type baseline struct {
	socket Socket
	at     time.Time
	owner  string
}
type differencer struct{ prev map[string]baseline }

func flowIdentity(boot, ns string, sk Socket) string {
	if boot == "" || ns == "" || sk.Cookie == "" || sk.Inode == "" {
		return ""
	}
	data, _ := json.Marshal([]string{boot, ns, sk.Protocol, sk.Cookie, sk.Inode, sk.LocalAddress, sk.RemoteAddress, sk.LocalEndpoint, sk.RemoteEndpoint, strconv.Itoa(sk.LocalPort), strconv.Itoa(sk.RemotePort)})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func ownerIdentity(o Owner) string {
	data, _ := json.Marshal(o)
	return string(data)
}

func delta(old, next *uint64) *uint64 {
	if old == nil || next == nil || *next < *old || *next-*old > math.MaxInt64 {
		return nil
	}
	n := *next - *old
	return &n
}

// Baselines are volatile. After restart or a missed source, lifetime counters
// cannot be assigned to the current interval, even for a newly seen cookie.
func (d *differencer) observe(c *Cycle, interval time.Duration) []Bucket {
	next := map[string]baseline{}
	rows := []Bucket{}
	for _, src := range c.Sources {
		if src.Status != "observed" && src.Status != "partial" {
			continue
		}
		for _, sk := range src.Values {
			id := flowIdentity(c.BootID, src.Namespace, sk)
			if id == "" {
				continue
			}
			b := Bucket{ID: id, Hour: src.ObservedAt.UTC().Truncate(time.Hour), FirstSeen: src.ObservedAt, LastSeen: src.ObservedAt, SourceID: src.ID, SourceName: src.Name, Namespace: src.Namespace, BootID: c.BootID, Socket: sk, Samples: 1, LostGaugeMax: sk.Lost}
			b.Socket.Tx, b.Socket.Rx, b.Socket.Retrans, b.Socket.Lost = nil, nil, nil, nil
			owner := ownerIdentity(sk.Owner)
			ownerHash := sha256.Sum256([]byte(owner))
			b.ID += ":" + hex.EncodeToString(ownerHash[:])
			old, exists := d.prev[id]
			valid := exists && old.owner == owner && src.ObservedAt.After(old.at) && src.ObservedAt.Sub(old.at) <= 2*interval && old.at.UTC().Truncate(time.Hour).Equal(b.Hour)
			if valid && sk.Protocol == "tcp" {
				b.TxBytes, b.RxBytes, b.Retransmissions = delta(old.socket.Tx, sk.Tx), delta(old.socket.Rx, sk.Rx), delta(old.socket.Retrans, sk.Retrans)
				if b.TxBytes != nil {
					b.TxIntervals = 1
				}
				if b.RxBytes != nil {
					b.RxIntervals = 1
				}
				if b.Retransmissions != nil {
					b.RetransIntervals = 1
				}
				if b.TxBytes != nil || b.RxBytes != nil || b.Retransmissions != nil {
					b.MeasuredIntervals = 1
				}
			}
			if b.MeasuredIntervals == 0 {
				b.SkippedIntervals = 1
				c.DiscardedIntervals++
			}
			rows = append(rows, b)
			next[id] = baseline{socket: sk, at: src.ObservedAt, owner: owner}
		}
	}
	d.prev = next
	return rows
}
