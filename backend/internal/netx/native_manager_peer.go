package netx

import (
	"context"
	"errors"
	"net"
)

// An existing Ethernet profile can own a veth endpoint without owning creation
// of its pair. Both endpoints must remain in the inspected namespace; a name
// or a cross-namespace peer index alone does not establish that relationship.
type nativePeerIdentity struct {
	Device  string `json:"device"`
	IfIndex int    `json:"ifIndex"`
	MAC     string `json:"mac"`
}

func nativePeer(link ipLink, links []ipLink) (*nativePeerIdentity, error) {
	if nativeLinkKind(link) != "veth" {
		return nil, nil
	}
	for _, peer := range links {
		if peer.IfIndex != link.LinkIndex {
			continue
		}
		id := &nativePeerIdentity{Device: peer.IfName, IfIndex: peer.IfIndex, MAC: peer.Address}
		if nativeLinkKind(peer) != "veth" || peer.LinkIndex != link.IfIndex || nativeValidatePeer(link.IfName, link.IfIndex, id) != nil {
			break
		}
		return id, nil
	}
	return nil, errors.New("the existing veth peer is absent, outside this namespace or not reciprocally identified")
}

func nativeValidatePeer(device string, ifindex int, peer *nativePeerIdentity) error {
	if peer == nil || ValidIfName(peer.Device) != nil || peer.Device == device || peer.IfIndex < 1 || peer.IfIndex > 2147483647 || peer.IfIndex == ifindex || len(peer.MAC) != 17 {
		return errors.New("native veth peer identity is unreadable")
	}
	mac, err := net.ParseMAC(peer.MAC)
	if err != nil || len(mac) != 6 {
		return errors.New("native veth peer MAC identity is unreadable")
	}
	return nil
}

func nativePeersEqual(a, b *nativePeerIdentity) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func nativeVerifyPeer(ctx context.Context, u *nativeUndo) error {
	if u.Peer == nil {
		return nil
	}
	links, err := nativeLinks(ctx)
	if err != nil {
		return errors.New("native veth peer inventory could not be verified")
	}
	for _, link := range links {
		if link.IfName == u.Device && link.IfIndex == u.IfIndex && link.Address == u.MAC && nativeLinkKind(link) == u.Kind {
			peer, err := nativePeer(link, links)
			if err == nil && nativePeersEqual(peer, u.Peer) {
				return nil
			}
			break
		}
	}
	return errors.New("native veth endpoint or reciprocal peer changed; its recovery evidence was preserved")
}
